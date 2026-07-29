package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-tui/internal/observe"
)

const (
	jspaceCaptureSchema = "jspace.capture.v0.1"
	jspaceSendTimeout   = 15 * time.Second
	jspaceMaxResponse   = 64 << 10
)

type jspaceFrame struct {
	Request  agent.ModelRequest  `json:"request"`
	Response agent.ModelResponse `json:"response"`
}

type jspaceEvent struct {
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

type jspaceAgent struct {
	Commit string `json:"commit"`
	Model  string `json:"model"`
}

type jspacePayload struct {
	SchemaVersion string        `json:"schemaVersion"`
	ID            string        `json:"id"`
	Label         string        `json:"label"`
	Agent         jspaceAgent   `json:"agent"`
	Events        []jspaceEvent `json:"events"`
	Frames        []jspaceFrame `json:"frames"`
}

type jspaceModel struct {
	inner  agent.Model
	mu     sync.Mutex
	active bool
	frames []jspaceFrame
}

func (model *jspaceModel) begin() {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.active = true
	model.frames = nil
}

func (model *jspaceModel) finish() []jspaceFrame {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.active = false
	frames := append([]jspaceFrame(nil), model.frames...)
	model.frames = nil
	return frames
}

func (model *jspaceModel) Complete(
	ctx context.Context,
	request agent.ModelRequest,
	emit func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	response, err := model.inner.Complete(ctx, request, emit)
	if err != nil {
		return response, err
	}
	model.mu.Lock()
	if model.active {
		model.frames = append(model.frames, jspaceFrame{
			Request:  request,
			Response: response,
		})
	}
	model.mu.Unlock()
	return response, nil
}

type jspaceSink struct {
	url        string
	token      string
	outbox     string
	httpClient *http.Client
}

type jspaceRunner struct {
	runner  conversationRunner
	model   *jspaceModel
	sink    *jspaceSink
	label   string
	modelID string
}

func newJSpaceSink(endpoint, token, outbox string) (*jspaceSink, error) {
	endpoint = strings.TrimSpace(endpoint)
	token = strings.TrimSpace(token)
	target, err := url.Parse(endpoint)
	if err != nil || target.Host == "" || target.User != nil ||
		target.RawQuery != "" || target.Fragment != "" {
		return nil, errors.New("J-Space capture URL must be an absolute URL without credentials, query, or fragment")
	}
	loopback := target.Hostname() == "127.0.0.1" ||
		strings.EqualFold(target.Hostname(), "localhost") ||
		target.Hostname() == "::1"
	if !strings.EqualFold(target.Scheme, "https") &&
		!(strings.EqualFold(target.Scheme, "http") && loopback) {
		return nil, errors.New("J-Space capture URL must use HTTPS, except for loopback HTTP")
	}
	if target.Path != "/jspace/api/captures" {
		return nil, errors.New("J-Space capture URL must end at /jspace/api/captures")
	}
	if token == "" {
		return nil, errors.New("J-Space capture token is required")
	}
	return &jspaceSink{
		url:        target.String(),
		token:      token,
		outbox:     outbox,
		httpClient: &http.Client{Timeout: jspaceSendTimeout},
	}, nil
}

func (runner *jspaceRunner) Run(
	ctx context.Context,
	prompt string,
	handler observe.Handler,
) (agent.RunResult, error) {
	id, err := newJSpaceID()
	if err != nil {
		return agent.RunResult{}, err
	}
	started := time.Now()
	runner.model.begin()
	events := make([]jspaceEvent, 0, 32)
	var sequence int64
	result, runErr := runner.runner.Run(ctx, prompt, func(observed observe.Event) {
		if observed.Runtime.Type != agent.EventMessageDelta {
			sequence++
			events = append(events, projectJSpaceEvent(
				sequence,
				time.Since(started),
				observed,
			))
		}
		if handler != nil {
			handler(observed)
		}
	})
	frames := runner.model.finish()
	if len(frames) > 0 {
		payload := jspacePayload{
			SchemaVersion: jspaceCaptureSchema,
			ID:            id,
			Label:         runner.label,
			Agent: jspaceAgent{
				Commit: buildRevision(),
				Model:  runner.modelID,
			},
			Events: events,
			Frames: frames,
		}
		deliveryContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			jspaceSendTimeout,
		)
		deliveryErr := runner.sink.deliver(deliveryContext, payload)
		if deliveryErr != nil && isRetryableCaptureError(deliveryErr) {
			// The Agent result remains authoritative. A mode-0600 outbox is the
			// durable signal that observation delivery needs retrying.
			if queueErr := runner.sink.queue(payload); queueErr != nil {
				deliveryErr = errors.Join(deliveryErr, queueErr)
			} else {
				deliveryErr = nil
			}
		}
		cancel()
		if deliveryErr != nil {
			return result, errors.Join(
				runErr,
				fmt.Errorf("deliver J-Space observation: %w", deliveryErr),
			)
		}
	}
	return result, runErr
}

func projectJSpaceEvent(
	sequence int64,
	offset time.Duration,
	observed observe.Event,
) jspaceEvent {
	event := observed.Runtime
	projected := jspaceEvent{
		Sequence:    sequence,
		OffsetMS:    offset.Milliseconds(),
		Type:        string(event.Type),
		Subagent:    observed.Subagent,
		IsError:     event.IsError,
		OutputBytes: len(event.Output),
	}
	if event.Duration > 0 {
		duration := event.Duration.Milliseconds()
		projected.DurationMS = &duration
	}
	if event.ToolCall != nil {
		projected.Tool = event.ToolCall.Name
	}
	if event.Model != nil {
		projected.Model = event.Model.Model
		projected.StopReason = string(event.Model.StopReason)
		if event.Model.Usage != nil {
			content, _ := json.Marshal(event.Model.Usage)
			_ = json.Unmarshal(content, &projected.Usage)
		}
	}
	return projected
}

func (sink *jspaceSink) deliver(ctx context.Context, current jspacePayload) error {
	if err := sink.flush(ctx); err != nil {
		return err
	}
	return sink.send(ctx, current)
}

func (sink *jspaceSink) flush(ctx context.Context) error {
	entries, err := os.ReadDir(sink.outbox)
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
		path := filepath.Join(sink.outbox, name)
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var payload jspacePayload
		if err := json.Unmarshal(content, &payload); err != nil {
			return err
		}
		if err := sink.send(ctx, payload); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func (sink *jspaceSink) send(ctx context.Context, payload jspacePayload) error {
	content, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		sink.url,
		bytes.NewReader(content),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+sink.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := sink.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, jspaceMaxResponse))
	if readErr != nil {
		return readErr
	}
	if response.StatusCode != http.StatusAccepted {
		return &jspaceHTTPError{
			status: response.StatusCode,
			body:   string(bytes.TrimSpace(body)),
		}
	}
	return nil
}

type jspaceHTTPError struct {
	status int
	body   string
}

func (err *jspaceHTTPError) Error() string {
	return fmt.Sprintf("J-Space capture HTTP %d: %s", err.status, err.body)
}

func isRetryableCaptureError(err error) bool {
	var httpErr *jspaceHTTPError
	if !errors.As(err, &httpErr) {
		return true
	}
	return httpErr.status == http.StatusTooManyRequests ||
		httpErr.status >= http.StatusInternalServerError
}

func (sink *jspaceSink) queue(payload jspacePayload) error {
	content, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sink.outbox, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(sink.outbox, ".jspace-*.json")
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
	return os.Rename(path, filepath.Join(sink.outbox, payload.ID+".json"))
}

func newJSpaceID() (string, error) {
	var entropy [6]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405Z") +
		"-" + hex.EncodeToString(entropy[:]), nil
}

func buildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			if len(setting.Value) > 12 {
				return setting.Value[:12]
			}
			return setting.Value
		}
	}
	return "unknown"
}
