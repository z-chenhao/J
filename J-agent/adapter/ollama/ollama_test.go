package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/z-chenhao/J/J-agent/agent"
)

func TestCompleteStreamsToolCallAndUsage(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return nil, err
		}
		if !payload.Stream || payload.Think == nil || !*payload.Think {
			t.Errorf("stream config=%#v", payload)
		}
		body := `{"model":"qwen3","message":{"role":"assistant","thinking":"think "},"done":false}` + "\n" +
			`{"model":"qwen3","message":{"role":"assistant","content":"checking"},"done":false}` + "\n" +
			`{"model":"qwen3","message":{"role":"assistant","tool_calls":[{"type":"function","function":{"index":0,"name":"weather","arguments":{"city":"HZ"}}}]},"done":false}` + "\n" +
			`{"model":"qwen3","message":{"role":"assistant"},"done":true,"done_reason":"stop","prompt_eval_count":8,"eval_count":3}` + "\n"
		return response(http.StatusOK, body), nil
	})}

	model, err := New(Config{
		Model:      "qwen3",
		BaseURL:    "http://ollama.example",
		Thinking:   ThinkingEnabled,
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	var deltas []agent.ModelDelta
	response, err := model.Complete(
		context.Background(),
		agent.ModelRequest{Messages: []agent.Message{agent.TextMessage(agent.RoleUser, "weather?")}},
		func(delta agent.ModelDelta) { deltas = append(deltas, delta) },
	)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if response.Provider != "ollama" || response.Model != "qwen3" ||
		response.StopReason != agent.StopReasonToolCalls {
		t.Fatalf("response=%#v", response)
	}
	if response.Usage == nil || response.Usage.InputTokens != 8 ||
		response.Usage.OutputTokens != 3 || response.Usage.TotalTokens != 11 {
		t.Fatalf("usage=%#v", response.Usage)
	}
	calls := response.Message.ToolCalls()
	if response.Message.Text() != "checking" || len(calls) != 1 ||
		calls[0].Name != "weather" || string(calls[0].Arguments) != `{"city":"HZ"}` ||
		calls[0].ID == "" {
		t.Fatalf("message=%#v", response.Message)
	}
	if len(deltas) != 3 || deltas[0].Type != agent.DeltaReasoning ||
		deltas[2].Type != agent.DeltaToolCall {
		t.Fatalf("deltas=%#v", deltas)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestRequestPreservesThinkingAndToolName(t *testing.T) {
	model, err := New(Config{Model: "qwen3"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	call := agent.ToolCall{ID: "local-id", Name: "weather", Arguments: json.RawMessage(`{"city":"HZ"}`)}
	payload, err := model.request(agent.ModelRequest{Messages: []agent.Message{
		{
			Role: agent.RoleAssistant,
			Content: []agent.Content{
				{Type: agent.ContentReasoning, Text: "thinking continuation"},
				{Type: agent.ContentToolCall, ToolCall: &call},
			},
		},
		{
			Role:     agent.RoleTool,
			Content:  []agent.Content{{Type: agent.ContentText, Text: "sunny"}},
			ToolName: "weather",
		},
	}})
	if err != nil {
		t.Fatalf("request() error: %v", err)
	}
	if payload.Messages[0].Thinking != "thinking continuation" ||
		payload.Messages[0].ToolCalls[0].Function.Name != "weather" ||
		payload.Messages[0].ToolCalls[0].Function.Index == nil ||
		*payload.Messages[0].ToolCalls[0].Function.Index != 0 ||
		payload.Messages[1].ToolName != "weather" {
		t.Fatalf("payload=%#v", payload.Messages)
	}
}

func TestCompleteReturnsTypedHTTPError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, "not ready"), nil
	})}
	model, err := New(Config{
		Model:      "qwen3",
		BaseURL:    "http://ollama.example",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	_, err = model.Complete(context.Background(), agent.ModelRequest{}, nil)
	var statusError *HTTPError
	if !errors.As(err, &statusError) || statusError.StatusCode != http.StatusServiceUnavailable ||
		statusError.Message != "not ready" {
		t.Fatalf("typed HTTP error=%#v, raw=%v", statusError, err)
	}
}

func TestNewValidatesAndJoinsBaseURL(t *testing.T) {
	model, err := New(Config{Model: "qwen3", BaseURL: "http://ollama.example/gateway/"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if model.endpoint != "http://ollama.example/gateway/api/chat" {
		t.Fatalf("endpoint=%q", model.endpoint)
	}
	for _, baseURL := range []string{
		"file://ollama.example",
		"http://user:secret@ollama.example",
		"http://ollama.example?node=1",
		"http://ollama.example#fragment",
	} {
		if _, err := New(Config{Model: "qwen3", BaseURL: baseURL}); err == nil {
			t.Fatalf("New() accepted base URL %q", baseURL)
		}
	}
}
