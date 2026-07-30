// Package observer adapts the experimental J observer run protocol to
// J-Space's authenticated capture API. Transport, credentials, and retry policy
// remain owned by J-Space rather than by J-tui or J-agent.
package observer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/z-chenhao/J/J-space/internal/artifact"
	"github.com/z-chenhao/J/J-space/internal/capture"
	"github.com/z-chenhao/J/J-space/internal/replay"
)

const (
	SchemaVersion      = "j.observer.run.v0.1"
	maxResponseBytes   = 64 << 10
	defaultHTTPTimeout = 15 * time.Second
)

// Run is one complete, permission-projected product observation.
type Run struct {
	SchemaVersion string                `json:"schemaVersion"`
	ID            string                `json:"id"`
	Label         string                `json:"label"`
	Product       string                `json:"product"`
	Commit        string                `json:"commit"`
	Profile       string                `json:"profile,omitempty"`
	Model         string                `json:"model"`
	Succeeded     bool                  `json:"succeeded"`
	Events        []artifact.AgentEvent `json:"events,omitempty"`
	Frames        []replay.Frame        `json:"frames,omitempty"`
}

// Config is J-Space-owned delivery policy.
type Config struct {
	URL        string
	Token      string
	Outbox     string
	HTTPClient *http.Client
}

// Deliver validates one observer run, flushes durable retries, and submits the
// current run. Retryable failures queue the current run without failing.
func Deliver(ctx context.Context, config Config, run Run) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	if err := validateConfig(&config); err != nil {
		return err
	}
	if err := validateRun(run); err != nil {
		return err
	}
	current := capture.Run{
		SchemaVersion: capture.SchemaVersion,
		ID:            run.ID,
		Label:         run.Label,
		Agent: artifact.Agent{
			Commit: run.Commit,
			Model:  run.Model,
		},
		Events: append([]artifact.AgentEvent(nil), run.Events...),
		Frames: append([]replay.Frame(nil), run.Frames...),
	}
	if err := flush(ctx, config); err != nil {
		if isRetryable(err) {
			if queueErr := queue(config.Outbox, current); queueErr != nil {
				return errors.Join(err, queueErr)
			}
			return nil
		}
		return err
	}
	if err := send(ctx, config, current); err != nil {
		if !isRetryable(err) {
			return err
		}
		if queueErr := queue(config.Outbox, current); queueErr != nil {
			return errors.Join(err, queueErr)
		}
	}
	return nil
}

func validateConfig(config *Config) error {
	config.URL = strings.TrimSpace(config.URL)
	config.Token = strings.TrimSpace(config.Token)
	config.Outbox = filepath.Clean(config.Outbox)
	target, err := url.Parse(config.URL)
	if err != nil || target.Host == "" || target.User != nil ||
		target.RawQuery != "" || target.Fragment != "" {
		return errors.New("capture URL must be absolute without credentials, query, or fragment")
	}
	loopback := target.Hostname() == "127.0.0.1" ||
		strings.EqualFold(target.Hostname(), "localhost") ||
		target.Hostname() == "::1"
	if !strings.EqualFold(target.Scheme, "https") &&
		!(strings.EqualFold(target.Scheme, "http") && loopback) {
		return errors.New("capture URL must use HTTPS, except for loopback HTTP")
	}
	if target.Path != "/jspace/api/captures" {
		return errors.New("capture URL must end at /jspace/api/captures")
	}
	if config.Token == "" {
		return errors.New("capture token is required")
	}
	if config.Outbox == "." {
		return errors.New("capture outbox is required")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return nil
}

func validateRun(run Run) error {
	if run.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported observer schema %q", run.SchemaVersion)
	}
	if strings.TrimSpace(run.ID) == "" ||
		strings.TrimSpace(run.Label) == "" ||
		strings.TrimSpace(run.Product) == "" ||
		strings.TrimSpace(run.Model) == "" {
		return errors.New("observer id, label, product, and model are required")
	}
	if len(run.Frames) == 0 {
		return errors.New("observer run has no authorized model frames")
	}
	return nil
}

func flush(ctx context.Context, config Config) error {
	entries, err := os.ReadDir(config.Outbox)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(config.Outbox, name)
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var current capture.Run
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&current); err != nil {
			return fmt.Errorf("decode queued capture %s: %w", name, err)
		}
		if err := send(ctx, config, current); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func send(ctx context.Context, config Config, current capture.Run) error {
	content, err := json.Marshal(current)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		config.URL,
		bytes.NewReader(content),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+config.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := config.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if readErr != nil {
		return readErr
	}
	if response.StatusCode != http.StatusAccepted {
		return &httpError{
			status: response.StatusCode,
			body:   string(bytes.TrimSpace(body)),
		}
	}
	return nil
}

type httpError struct {
	status int
	body   string
}

func (err *httpError) Error() string {
	return fmt.Sprintf("capture HTTP %d: %s", err.status, err.body)
}

func isRetryable(err error) bool {
	var responseErr *httpError
	if !errors.As(err, &responseErr) {
		return true
	}
	return responseErr.status == http.StatusTooManyRequests ||
		responseErr.status >= http.StatusInternalServerError
}

func queue(outbox string, current capture.Run) error {
	content, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outbox, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(outbox, ".jspace-*.json")
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
	return os.Rename(path, filepath.Join(outbox, current.ID+".json"))
}
