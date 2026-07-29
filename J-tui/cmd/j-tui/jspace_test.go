package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-tui/internal/observe"
)

type jspaceModelFunc func(
	context.Context,
	agent.ModelRequest,
	func(agent.ModelDelta),
) (agent.ModelResponse, error)

func (function jspaceModelFunc) Complete(
	ctx context.Context,
	request agent.ModelRequest,
	emit func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	return function(ctx, request, emit)
}

type jspaceConversation struct {
	model agent.Model
}

func (conversation jspaceConversation) Run(
	ctx context.Context,
	prompt string,
	handler observe.Handler,
) (agent.RunResult, error) {
	handler(observe.Event{Runtime: agent.Event{Type: agent.EventAgentStarted}})
	response, err := conversation.model.Complete(ctx, agent.ModelRequest{
		Messages: []agent.Message{agent.TextMessage(agent.RoleUser, prompt)},
	}, func(agent.ModelDelta) {})
	if err != nil {
		return agent.RunResult{}, err
	}
	handler(observe.Event{Runtime: agent.Event{
		Type: agent.EventAgentCompleted,
		Model: &agent.ModelObservation{
			Model:      response.Model,
			StopReason: response.StopReason,
		},
	}})
	return agent.RunResult{Message: response.Message, Turns: 1}, nil
}

type jspaceRoundTripFunc func(*http.Request) (*http.Response, error)

func (function jspaceRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestJSpaceRunnerUploadsModelFramesAndEvents(t *testing.T) {
	inner := jspaceModelFunc(func(
		_ context.Context,
		_ agent.ModelRequest,
		_ func(agent.ModelDelta),
	) (agent.ModelResponse, error) {
		return agent.ModelResponse{
			Message:    agent.TextMessage(agent.RoleAssistant, "hello"),
			Model:      "qwen",
			StopReason: agent.StopReasonStop,
		}, nil
	})
	captured := &jspaceModel{inner: inner}
	var uploaded jspacePayload
	sink, err := newJSpaceSink(
		"https://model.example/jspace/api/captures",
		"capture-secret",
		t.TempDir(),
	)
	if err != nil {
		t.Fatal(err)
	}
	sink.httpClient.Transport = jspaceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer capture-secret" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		content, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(content, &uploaded); err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       io.NopCloser(http.NoBody),
		}, nil
	})
	runner := &jspaceRunner{
		runner:  jspaceConversation{model: captured},
		model:   captured,
		sink:    sink,
		label:   "J-tui test",
		modelID: "qwen",
	}
	if _, err := runner.Run(context.Background(), "hi", nil); err != nil {
		t.Fatal(err)
	}
	if uploaded.SchemaVersion != jspaceCaptureSchema ||
		uploaded.Label != "J-tui test" ||
		len(uploaded.Frames) != 1 ||
		len(uploaded.Events) != 2 ||
		uploaded.Frames[0].Request.Messages[0].Text() != "hi" ||
		uploaded.Frames[0].Response.Message.Text() != "hello" {
		t.Fatalf("uploaded=%#v", uploaded)
	}
}

func TestJSpaceRunnerQueuesCaptureWhenDeliveryFails(t *testing.T) {
	inner := jspaceModelFunc(func(
		_ context.Context,
		_ agent.ModelRequest,
		_ func(agent.ModelDelta),
	) (agent.ModelResponse, error) {
		return agent.ModelResponse{
			Message: agent.TextMessage(agent.RoleAssistant, "hello"),
			Model:   "qwen",
		}, nil
	})
	captured := &jspaceModel{inner: inner}
	outbox := t.TempDir()
	sink, err := newJSpaceSink(
		"https://model.example/jspace/api/captures",
		"capture-secret",
		outbox,
	)
	if err != nil {
		t.Fatal(err)
	}
	sink.httpClient.Transport = jspaceRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})
	runner := &jspaceRunner{
		runner:  jspaceConversation{model: captured},
		model:   captured,
		sink:    sink,
		label:   "J-tui test",
		modelID: "qwen",
	}
	if _, err := runner.Run(context.Background(), "hi", nil); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(outbox)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("outbox entries=%v", entries)
	}
	info, err := os.Stat(filepath.Join(outbox, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestJSpaceRunnerReportsRejectedCaptureWithoutQueueing(t *testing.T) {
	inner := jspaceModelFunc(func(
		_ context.Context,
		_ agent.ModelRequest,
		_ func(agent.ModelDelta),
	) (agent.ModelResponse, error) {
		return agent.ModelResponse{
			Message: agent.TextMessage(agent.RoleAssistant, "hello"),
			Model:   "qwen",
		}, nil
	})
	captured := &jspaceModel{inner: inner}
	outbox := t.TempDir()
	sink, err := newJSpaceSink(
		"https://model.example/jspace/api/captures",
		"wrong-secret",
		outbox,
	)
	if err != nil {
		t.Fatal(err)
	}
	sink.httpClient.Transport = jspaceRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":"invalid"}`)),
		}, nil
	})
	runner := &jspaceRunner{
		runner:  jspaceConversation{model: captured},
		model:   captured,
		sink:    sink,
		label:   "J-tui test",
		modelID: "qwen",
	}
	if _, err := runner.Run(context.Background(), "hi", nil); err == nil ||
		!strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("error=%v", err)
	}
	entries, err := os.ReadDir(outbox)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("outbox entries=%v", entries)
	}
}
