// Package capture owns the experimental authenticated handoff from a remote
// J-tui run to the local J-Space replay worker.
package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/z-chenhao/J/J-space/internal/artifact"
	"github.com/z-chenhao/J/J-space/internal/replay"
)

const SchemaVersion = "jspace.capture.v0.1"

var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

type Run struct {
	SchemaVersion string                `json:"schemaVersion"`
	ID            string                `json:"id"`
	Label         string                `json:"label"`
	Agent         artifact.Agent        `json:"agent"`
	Events        []artifact.AgentEvent `json:"events"`
	Frames        []replay.Frame        `json:"frames"`
}

type Config struct {
	StateDir       string
	SupportedModel string
	Replay         replay.Config
}

type Service struct {
	config  Config
	queue   chan string
	measure func(context.Context, replay.Config, []replay.Frame) (replay.Output, error)
}

func New(config Config) (*Service, error) {
	config.StateDir = filepath.Clean(config.StateDir)
	config.SupportedModel = strings.TrimSpace(config.SupportedModel)
	if config.StateDir == "." || config.SupportedModel == "" {
		return nil, errors.New("capture state directory and supported model are required")
	}
	return &Service{
		config:  config,
		queue:   make(chan string, 64),
		measure: replay.Run,
	}, nil
}

func (service *Service) Enqueue(ctx context.Context, run Run) error {
	run.Label = strings.TrimSpace(run.Label)
	if err := validate(run, service.config.SupportedModel); err != nil {
		return err
	}
	content, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if err := writePrivateAtomic(service.inboxDirectory(), run.ID+".json", content); err != nil {
		return fmt.Errorf("persist capture inbox: %w", err)
	}
	now := time.Now().UTC()
	trace := artifact.Trace{
		SchemaVersion: artifact.SchemaVersion,
		ID:            run.ID,
		Label:         run.Label,
		CreatedAt:     now,
		UpdatedAt:     now,
		Status:        "probing",
		Agent:         run.Agent,
		Measurement: artifact.Measurement{
			Kind:                "pending",
			Probe:               "mlx-jacobian-lens",
			ModelCheckpoint:     service.config.Replay.ModelRepo,
			RuntimeQuantization: "oQ4e/oQ5e mixed affine",
			LensRepository:      service.config.Replay.LensRepo,
			ContextFidelity:     "captured J-tui model frames; post-hoc full-prefix replay",
			Layers:              40,
			ResidualWidth:       2048,
			VocabularySize:      248320,
		},
		Events: run.Events,
		Notes: []string{
			"received from authenticated remote J-tui capture",
			"J-lens turns cover root-model frames; the timeline may also contain subagent events",
		},
	}
	if err := artifact.WriteAtomic(service.runsDirectory(), trace); err != nil {
		return fmt.Errorf("create capture artifact: %w", err)
	}
	select {
	case service.queue <- run.ID:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (service *Service) Run(ctx context.Context) {
	service.scan()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-service.queue:
			service.process(ctx, id)
		case <-ticker.C:
			service.scan()
		}
	}
}

func (service *Service) scan() {
	entries, err := os.ReadDir(service.inboxDirectory())
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Printf("jspace_capture_scan error=%q", err)
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			names = append(names, strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	sort.Strings(names)
	for _, id := range names {
		select {
		case service.queue <- id:
		default:
			return
		}
	}
}

func (service *Service) process(ctx context.Context, id string) {
	path := filepath.Join(service.inboxDirectory(), id+".json")
	run, err := load(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Printf("jspace_capture_load id=%s error=%q", id, err)
		_ = os.Remove(path)
		return
	}
	if err := validate(run, service.config.SupportedModel); err != nil {
		log.Printf("jspace_capture_validate id=%s error=%q", id, err)
		_ = os.Remove(path)
		return
	}
	measured, probeErr := service.measure(ctx, service.config.Replay, run.Frames)
	trace, loadErr := artifact.Load(filepath.Join(service.runsDirectory(), id+".json"))
	if loadErr != nil {
		log.Printf("jspace_capture_artifact id=%s error=%q", id, loadErr)
		return
	}
	if probeErr != nil {
		trace.Status = "partial"
		trace.Measurement.Kind = "unavailable"
		trace.Failure = "J-lens replay failed"
		trace.Notes = append(trace.Notes, "probe failure retained in private service log")
		log.Printf("jspace_capture_probe id=%s error=%q", id, probeErr)
	} else {
		trace.Status = "completed"
		trace.Measurement = measured.Measurement
		trace.Turns = measured.Turns
		trace.Notes = append(trace.Notes, measured.Notes...)
	}
	if err := artifact.WriteAtomic(service.runsDirectory(), trace); err != nil {
		log.Printf("jspace_capture_write id=%s error=%q", id, err)
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("jspace_capture_remove id=%s error=%q", id, err)
	}
}

func validate(run Run, supportedModel string) error {
	if run.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported capture schema %q", run.SchemaVersion)
	}
	if !validID.MatchString(run.ID) {
		return errors.New("capture id is invalid")
	}
	if run.Label == "" || len(run.Label) > 120 {
		return errors.New("capture label must contain 1 to 120 bytes")
	}
	if run.Agent.Model != supportedModel {
		return fmt.Errorf("capture model %q is not supported", run.Agent.Model)
	}
	if len(run.Frames) == 0 || len(run.Frames) > 32 {
		return errors.New("capture must contain 1 to 32 model frames")
	}
	if len(run.Events) > 4096 {
		return errors.New("capture contains too many events")
	}
	return nil
}

func load(path string) (Run, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Run{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var run Run
	if err := decoder.Decode(&run); err != nil {
		return Run{}, err
	}
	return run, nil
}

func writePrivateAtomic(directory, name string, content []byte) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".capture-*.json")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer os.Remove(path)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(content, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(directory, name))
}

func (service *Service) inboxDirectory() string {
	return filepath.Join(service.config.StateDir, "inbox")
}

func (service *Service) runsDirectory() string {
	return filepath.Join(service.config.StateDir, "runs")
}
