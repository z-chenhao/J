package main

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/J/J-agent/agent"
	jpackages "github.com/z-chenhao/J/J-packages"
	"github.com/z-chenhao/J/J-tui/internal/observe"
)

type observerModelFunc func(
	context.Context,
	agent.ModelRequest,
	func(agent.ModelDelta),
) (agent.ModelResponse, error)

func (function observerModelFunc) Complete(
	ctx context.Context,
	request agent.ModelRequest,
	emit func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	return function(ctx, request, emit)
}

type observerConversation struct {
	model agent.Model
}

func (conversation observerConversation) Run(
	ctx context.Context,
	prompt string,
	handler observe.Handler,
) (agent.RunResult, error) {
	if handler != nil {
		handler(observe.Event{Runtime: agent.Event{Type: agent.EventAgentStarted}})
	}
	response, err := conversation.model.Complete(
		ctx,
		agent.ModelRequest{Messages: []agent.Message{
			agent.TextMessage(agent.RoleUser, prompt),
		}},
		func(agent.ModelDelta) {},
	)
	if err != nil {
		return agent.RunResult{}, err
	}
	if handler != nil {
		handler(observe.Event{Runtime: agent.Event{
			Type: agent.EventTurnCompleted,
			Model: &agent.ModelObservation{
				Model:      response.Model,
				StopReason: response.StopReason,
				Usage:      response.Usage,
			},
		}})
	}
	return agent.RunResult{Message: response.Message, Turns: 1}, nil
}

func TestObserverRunnerProjectsPermissionsAndIsolatesFailure(t *testing.T) {
	base := observerModelFunc(func(
		_ context.Context,
		_ agent.ModelRequest,
		_ func(agent.ModelDelta),
	) (agent.ModelResponse, error) {
		return agent.ModelResponse{
			Message:    agent.TextMessage(agent.RoleAssistant, "done"),
			Provider:   "test",
			Model:      "qwen",
			StopReason: agent.StopReasonStop,
		}, nil
	})
	captured := &observerModel{inner: base}
	var received = make(map[string]observerRun)
	var receivedMu sync.Mutex
	var diagnostics bytes.Buffer
	runner := &observerRunner{
		runner:  observerConversation{model: captured},
		model:   captured,
		label:   "test",
		modelID: "qwen",
		specs: []jpackages.ObserverSpec{
			{Name: "events", Permissions: []string{permissionEvents}},
			{Name: "frames", Permissions: []string{permissionFrames}},
		},
		diagnostics: &diagnostics,
		dispatch: func(
			_ context.Context,
			spec jpackages.ObserverSpec,
			run observerRun,
		) error {
			receivedMu.Lock()
			received[spec.Name] = run
			receivedMu.Unlock()
			if spec.Name == "events" {
				return errors.New("observer rejected run")
			}
			return nil
		},
	}

	result, err := runner.Run(context.Background(), "inspect", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.Text() != "done" {
		t.Fatalf("result=%q", result.Message.Text())
	}
	receivedMu.Lock()
	defer receivedMu.Unlock()
	if len(received["events"].Events) != 2 || len(received["events"].Frames) != 0 {
		t.Fatalf("events projection=%+v", received["events"])
	}
	if len(received["frames"].Frames) != 1 || len(received["frames"].Events) != 0 {
		t.Fatalf("frames projection=%+v", received["frames"])
	}
	if !bytes.Contains(diagnostics.Bytes(), []byte("observer events failed")) {
		t.Fatalf("diagnostics=%q", diagnostics.String())
	}
}

func TestDispatchObserverWritesOneBoundedRun(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a child process")
	}
	spec := jpackages.ObserverSpec{
		Name:    "fixture",
		Command: "/bin/sh",
		Args:    []string{"-c", "read line; test \"${line#*j.observer.run.v0.1}\" != \"$line\""},
		Env:     []string{"PATH=/bin:/usr/bin"},
	}
	if err := dispatchObserver(context.Background(), spec, observerRun{
		SchemaVersion: observerRunSchema,
		ID:            "run",
		Label:         "fixture",
		Product:       "J-tui",
		Model:         "qwen",
		Succeeded:     true,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestObserverRunIDIsUniqueWithoutExternalState(t *testing.T) {
	started := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	first := newObserverRunID(started)
	second := newObserverRunID(started)
	if first == second {
		t.Fatalf("duplicate observer run ID %q", first)
	}
}
