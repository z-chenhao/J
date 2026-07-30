package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/z-chenhao/J/J-agent/agent"
	jpackages "github.com/z-chenhao/J/J-packages"
	"github.com/z-chenhao/J/J-tui/internal/observe"
)

const (
	observerRunSchema  = "j.observer.run.v0.1"
	observerRunTimeout = 15 * time.Second
	observerMaxPayload = 8 << 20
	observerMaxStderr  = 16 << 10
	permissionEvents   = "agent.events"
	permissionFrames   = "model.frames"
)

var observerRunSequence atomic.Uint64

type observerFrame struct {
	Request  agent.ModelRequest  `json:"request"`
	Response agent.ModelResponse `json:"response"`
}

type observerEvent struct {
	Sequence    int64        `json:"sequence"`
	OffsetMS    int64        `json:"offsetMs"`
	Type        string       `json:"type"`
	Subagent    string       `json:"subagent,omitempty"`
	DurationMS  *int64       `json:"durationMs,omitempty"`
	IsError     bool         `json:"isError,omitempty"`
	Tool        string       `json:"tool,omitempty"`
	OutputBytes int          `json:"outputBytes,omitempty"`
	Model       string       `json:"model,omitempty"`
	StopReason  string       `json:"stopReason,omitempty"`
	Usage       *agent.Usage `json:"usage,omitempty"`
}

type observerRun struct {
	SchemaVersion string          `json:"schemaVersion"`
	ID            string          `json:"id"`
	Label         string          `json:"label"`
	Product       string          `json:"product"`
	Commit        string          `json:"commit"`
	Profile       string          `json:"profile,omitempty"`
	Model         string          `json:"model"`
	Succeeded     bool            `json:"succeeded"`
	Events        []observerEvent `json:"events,omitempty"`
	Frames        []observerFrame `json:"frames,omitempty"`
}

type observerModel struct {
	inner  agent.Model
	mu     sync.Mutex
	active bool
	frames []observerFrame
}

func (model *observerModel) begin() {
	if model == nil {
		return
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	model.active = true
	model.frames = nil
}

func (model *observerModel) finish() []observerFrame {
	if model == nil {
		return nil
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	model.active = false
	frames := append([]observerFrame(nil), model.frames...)
	model.frames = nil
	return frames
}

func (model *observerModel) Complete(
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
		model.frames = append(model.frames, observerFrame{
			Request:  request,
			Response: response,
		})
	}
	model.mu.Unlock()
	return response, nil
}

type observerRunner struct {
	runner      conversationRunner
	model       *observerModel
	specs       []jpackages.ObserverSpec
	label       string
	profile     string
	modelID     string
	diagnostics io.Writer
	dispatch    func(context.Context, jpackages.ObserverSpec, observerRun) error
}

func (runner *observerRunner) Run(
	ctx context.Context,
	prompt string,
	handler observe.Handler,
) (agent.RunResult, error) {
	started := time.Now()
	runner.model.begin()
	events := make([]observerEvent, 0, 32)
	var sequence int64
	result, runErr := runner.runner.Run(ctx, prompt, func(observed observe.Event) {
		if observed.Runtime.Type != agent.EventMessageDelta {
			sequence++
			events = append(events, projectObserverEvent(
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
	current := observerRun{
		SchemaVersion: observerRunSchema,
		ID:            newObserverRunID(started),
		Label:         runner.label,
		Product:       "J-tui",
		Commit:        buildRevision(),
		Profile:       runner.profile,
		Model:         runner.modelID,
		Succeeded:     runErr == nil,
		Events:        events,
		Frames:        frames,
	}
	runner.notify(ctx, current)
	return result, runErr
}

func (runner *observerRunner) notify(ctx context.Context, current observerRun) {
	dispatch := runner.dispatch
	if dispatch == nil {
		dispatch = dispatchObserver
	}
	type notification struct {
		name string
		err  error
	}
	results := make(chan notification, len(runner.specs))
	var wait sync.WaitGroup
	for _, spec := range runner.specs {
		spec := spec
		projected := projectObserverRun(current, spec.Permissions)
		wait.Add(1)
		go func() {
			defer wait.Done()
			deliveryContext, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				observerRunTimeout,
			)
			defer cancel()
			results <- notification{
				name: spec.Name,
				err:  dispatch(deliveryContext, spec, projected),
			}
		}()
	}
	wait.Wait()
	close(results)
	for result := range results {
		if result.err != nil && runner.diagnostics != nil {
			_, _ = fmt.Fprintf(
				runner.diagnostics,
				"warning: observer %s failed: %v\n",
				result.name,
				result.err,
			)
		}
	}
}

func projectObserverRun(current observerRun, permissions []string) observerRun {
	projected := current
	projected.Events = nil
	projected.Frames = nil
	for _, permission := range permissions {
		switch permission {
		case permissionEvents:
			projected.Events = append([]observerEvent(nil), current.Events...)
		case permissionFrames:
			projected.Frames = append([]observerFrame(nil), current.Frames...)
		}
	}
	return projected
}

func projectObserverEvent(
	sequence int64,
	offset time.Duration,
	observed observe.Event,
) observerEvent {
	event := observed.Runtime
	projected := observerEvent{
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
			usage := *event.Model.Usage
			projected.Usage = &usage
		}
	}
	return projected
}

func dispatchObserver(
	ctx context.Context,
	spec jpackages.ObserverSpec,
	current observerRun,
) error {
	content, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if len(content) > observerMaxPayload {
		return fmt.Errorf("run payload exceeds %d bytes", observerMaxPayload)
	}
	command := exec.CommandContext(ctx, spec.Command, spec.Args...)
	command.Dir = spec.Dir
	command.Env = append([]string(nil), spec.Env...)
	command.Stdin = bytes.NewReader(append(content, '\n'))
	command.Stdout = io.Discard
	stderr := &boundedObserverStderr{remaining: observerMaxStderr}
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		details := strings.TrimSpace(stderr.String())
		if details != "" {
			return fmt.Errorf("%w: %s", err, details)
		}
		return err
	}
	return nil
}

type boundedObserverStderr struct {
	buffer    bytes.Buffer
	remaining int
}

func (writer *boundedObserverStderr) Write(content []byte) (int, error) {
	original := len(content)
	if writer.remaining <= 0 {
		return original, nil
	}
	if len(content) > writer.remaining {
		content = content[:writer.remaining]
	}
	written, err := writer.buffer.Write(content)
	writer.remaining -= written
	if err != nil {
		return written, err
	}
	return original, nil
}

func (writer *boundedObserverStderr) String() string {
	return writer.buffer.String()
}

func newObserverRunID(started time.Time) string {
	return fmt.Sprintf(
		"%s-%d-%d",
		started.UTC().Format("20060102T150405.000000000Z"),
		os.Getpid(),
		observerRunSequence.Add(1),
	)
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

func hasObserverPermission(specs []jpackages.ObserverSpec, permission string) bool {
	for _, spec := range specs {
		for _, current := range spec.Permissions {
			if current == permission {
				return true
			}
		}
	}
	return false
}

var _ agent.Model = (*observerModel)(nil)
