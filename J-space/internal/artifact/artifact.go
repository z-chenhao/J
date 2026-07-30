package artifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = "jspace.trace.v0.1"

var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

type Trace struct {
	SchemaVersion string       `json:"schemaVersion"`
	ID            string       `json:"id"`
	Label         string       `json:"label"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
	Status        string       `json:"status"`
	Agent         Agent        `json:"agent"`
	Measurement   Measurement  `json:"measurement"`
	Events        []AgentEvent `json:"events"`
	Turns         []Turn       `json:"turns,omitempty"`
	Failure       string       `json:"failure,omitempty"`
	Transcript    []Message    `json:"transcript,omitempty"`
	Notes         []string     `json:"notes,omitempty"`
}

type Agent struct {
	Commit string `json:"commit"`
	Model  string `json:"model"`
}

type Measurement struct {
	Kind                string `json:"kind"`
	Probe               string `json:"probe"`
	ModelCheckpoint     string `json:"modelCheckpoint"`
	RuntimeQuantization string `json:"runtimeQuantization"`
	LensRepository      string `json:"lensRepository"`
	LensSHA256          string `json:"lensSha256,omitempty"`
	ContextFidelity     string `json:"contextFidelity"`
	Layers              int    `json:"layers"`
	ResidualWidth       int    `json:"residualWidth"`
	VocabularySize      int    `json:"vocabularySize"`
}

type AgentEvent struct {
	Sequence    int64          `json:"sequence"`
	OffsetMS    int64          `json:"offsetMs"`
	Type        string         `json:"type"`
	Subagent    string         `json:"subagent,omitempty"`
	DurationMS  *int64         `json:"durationMs,omitempty"`
	IsError     bool           `json:"isError,omitempty"`
	Tool        string         `json:"tool,omitempty"`
	OutputBytes int            `json:"outputBytes,omitempty"`
	Model       string         `json:"model,omitempty"`
	StopReason  string         `json:"stopReason,omitempty"`
	Usage       map[string]any `json:"usage,omitempty"`
}

type Turn struct {
	Index             int        `json:"index"`
	SelectedPositions []Position `json:"selectedPositions"`
}

type Position struct {
	Index  int         `json:"index"`
	Token  string      `json:"token"`
	Role   string      `json:"role"`
	Layers []LayerRead `json:"layers"`
}

type LayerRead struct {
	Layer  int       `json:"layer"`
	Region string    `json:"region"`
	Top    []Concept `json:"top"`
}

type Concept struct {
	Token string  `json:"token"`
	Rank  int     `json:"rank"`
	Score float64 `json:"score,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Summary struct {
	ID              string    `json:"id"`
	Label           string    `json:"label"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	Status          string    `json:"status"`
	Model           string    `json:"model"`
	MeasurementKind string    `json:"measurementKind"`
	Turns           int       `json:"turns"`
	Failure         string    `json:"failure,omitempty"`
}

func (trace *Trace) Normalize(now time.Time) {
	if trace.SchemaVersion == "" {
		trace.SchemaVersion = SchemaVersion
	}
	if trace.CreatedAt.IsZero() {
		trace.CreatedAt = now.UTC()
	}
	trace.UpdatedAt = now.UTC()
	if trace.Events == nil {
		trace.Events = []AgentEvent{}
	}
	if trace.Status == "" {
		trace.Status = "pending"
	}
}

func (trace Trace) Validate() error {
	if trace.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %q", trace.SchemaVersion)
	}
	if !validID.MatchString(trace.ID) {
		return errors.New("artifact id must contain only letters, numbers, dot, underscore, or dash")
	}
	if strings.TrimSpace(trace.Label) == "" {
		return errors.New("artifact label is required")
	}
	if trace.CreatedAt.IsZero() || trace.UpdatedAt.IsZero() {
		return errors.New("artifact timestamps are required")
	}
	switch trace.Status {
	case "pending", "running", "probing", "completed", "partial", "failed":
	default:
		return fmt.Errorf("unsupported artifact status %q", trace.Status)
	}
	if strings.TrimSpace(trace.Agent.Model) == "" {
		return errors.New("agent model is required")
	}
	switch trace.Measurement.Kind {
	case "pending", "posthoc_replay", "illustrative", "unavailable":
	default:
		return fmt.Errorf("unsupported measurement kind %q", trace.Measurement.Kind)
	}
	for turnIndex, turn := range trace.Turns {
		if turn.Index < 0 {
			return fmt.Errorf("turn %d has a negative index", turnIndex)
		}
		for positionIndex, position := range turn.SelectedPositions {
			// Token positions use Python-style indexing relative to the end of
			// the replayed prefix, so negative values are both valid and
			// intentional.
			for layerIndex, layer := range position.Layers {
				if layer.Layer < 0 {
					return fmt.Errorf(
						"turn %d position %d layer %d has a negative layer",
						turnIndex,
						positionIndex,
						layerIndex,
					)
				}
				for conceptIndex, concept := range layer.Top {
					if strings.TrimSpace(concept.Token) == "" || concept.Rank < 1 {
						return fmt.Errorf(
							"turn %d position %d layer %d concept %d is invalid",
							turnIndex,
							positionIndex,
							layerIndex,
							conceptIndex,
						)
					}
				}
			}
		}
	}
	return nil
}

func (trace Trace) Summary() Summary {
	return Summary{
		ID:              trace.ID,
		Label:           trace.Label,
		CreatedAt:       trace.CreatedAt,
		UpdatedAt:       trace.UpdatedAt,
		Status:          trace.Status,
		Model:           trace.Agent.Model,
		MeasurementKind: trace.Measurement.Kind,
		Turns:           len(trace.Turns),
		Failure:         trace.Failure,
	}
}

// Public returns the read-only HTTP projection. A transcript may be retained
// locally for an explicitly private experiment, but it is never served by the
// workbench.
func (trace Trace) Public() Trace {
	trace.Transcript = nil
	return trace
}

func Load(path string) (Trace, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Trace{}, err
	}
	var trace Trace
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&trace); err != nil {
		return Trace{}, fmt.Errorf("decode artifact %s: %w", filepath.Base(path), err)
	}
	if err := trace.Validate(); err != nil {
		return Trace{}, fmt.Errorf("validate artifact %s: %w", filepath.Base(path), err)
	}
	return trace, nil
}

func LoadAll(directory string) ([]Trace, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []Trace{}, nil
	}
	if err != nil {
		return nil, err
	}
	traces := make([]Trace, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		trace, err := Load(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		traces = append(traces, trace)
	}
	sort.Slice(traces, func(i, j int) bool {
		if traces[i].UpdatedAt.Equal(traces[j].UpdatedAt) {
			return traces[i].ID > traces[j].ID
		}
		return traces[i].UpdatedAt.After(traces[j].UpdatedAt)
	})
	return traces, nil
}

func WriteAtomic(directory string, trace Trace) error {
	trace.Normalize(time.Now())
	if err := trace.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	content, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".jspace-*.json")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
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
	return os.Rename(name, filepath.Join(directory, trace.ID+".json"))
}
