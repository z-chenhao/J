package deepseek

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
		if got := request.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization=%q", got)
		}
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return nil, err
		}
		if !payload.Stream || !payload.StreamOptions.IncludeUsage {
			t.Errorf("stream options=%#v", payload)
		}
		if payload.Thinking == nil || payload.Thinking.Type != "enabled" {
			t.Errorf("thinking=%#v", payload.Thinking)
		}
		body := "data: " + `{"id":"resp-1","model":"deepseek-v4-pro","choices":[{"delta":{"reasoning_content":"think "},"finish_reason":""}]}` + "\n\n" +
			"data: " + `{"id":"resp-1","model":"deepseek-v4-pro","choices":[{"delta":{"content":"checking"},"finish_reason":""}]}` + "\n\n" +
			"data: " + `{"id":"resp-1","model":"deepseek-v4-pro","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"weather","arguments":"{\"city\":\"HZ\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14,"prompt_cache_hit_tokens":3,"completion_tokens_details":{"reasoning_tokens":2}}}` + "\n\n" +
			"data: [DONE]\n\n"
		return response(http.StatusOK, body), nil
	})}

	model, err := New(Config{
		APIKey:     "secret",
		Model:      "deepseek-v4-pro",
		BaseURL:    "https://deepseek.example",
		Thinking:   ThinkingEnabled,
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	var deltas []agent.ModelDelta
	response, err := model.Complete(
		context.Background(),
		agent.ModelRequest{
			Messages: []agent.Message{agent.TextMessage(agent.RoleUser, "weather?")},
			Tools: []agent.ToolSpec{{
				Name:        "weather",
				Description: "weather by city",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			}},
		},
		func(delta agent.ModelDelta) { deltas = append(deltas, delta) },
	)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if response.Provider != "deepseek" || response.Model != "deepseek-v4-pro" || response.ResponseID != "resp-1" {
		t.Fatalf("identity=%#v", response)
	}
	if response.StopReason != agent.StopReasonToolCalls {
		t.Fatalf("stop reason=%q", response.StopReason)
	}
	if response.Usage == nil || response.Usage.TotalTokens != 14 ||
		response.Usage.CachedInputTokens == nil || *response.Usage.CachedInputTokens != 3 ||
		response.Usage.ReasoningTokens == nil || *response.Usage.ReasoningTokens != 2 {
		t.Fatalf("usage=%#v", response.Usage)
	}
	calls := response.Message.ToolCalls()
	if response.Message.Text() != "checking" || len(calls) != 1 ||
		calls[0].ID != "call-1" || string(calls[0].Arguments) != `{"city":"HZ"}` {
		t.Fatalf("message=%#v", response.Message)
	}
	if len(deltas) != 3 || deltas[0].Type != agent.DeltaReasoning ||
		deltas[2].Type != agent.DeltaToolCall {
		t.Fatalf("deltas=%#v", deltas)
	}
}

func TestRequestPreservesReasoningAndToolIdentity(t *testing.T) {
	model, err := New(Config{APIKey: "secret", Model: "deepseek-v4-pro"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	call := agent.ToolCall{ID: "call-1", Name: "weather", Arguments: json.RawMessage(`{"city":"HZ"}`)}
	payload, err := model.request(agent.ModelRequest{Messages: []agent.Message{
		{
			Role: agent.RoleAssistant,
			Content: []agent.Content{
				{Type: agent.ContentReasoning, Text: "private continuation"},
				{Type: agent.ContentToolCall, ToolCall: &call},
			},
		},
		{
			Role:       agent.RoleTool,
			Content:    []agent.Content{{Type: agent.ContentText, Text: "sunny"}},
			ToolCallID: "call-1",
			ToolName:   "weather",
		},
	}})
	if err != nil {
		t.Fatalf("request() error: %v", err)
	}
	if payload.Messages[0].ReasoningContent != "private continuation" ||
		payload.Messages[0].Content != nil ||
		payload.Messages[0].ToolCalls[0].ID != "call-1" ||
		payload.Messages[1].ToolCallID != "call-1" {
		t.Fatalf("payload=%#v", payload.Messages)
	}
}

func TestCompleteReportsBoundedHTTPError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusBadRequest, "bad request"), nil
	})}
	model, err := New(Config{
		APIKey:     "secret",
		Model:      "deepseek-v4-pro",
		BaseURL:    "https://deepseek.example",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	_, err = model.Complete(context.Background(), agent.ModelRequest{}, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error=%v", err)
	}
	var statusError *HTTPError
	if !errors.As(err, &statusError) || statusError.StatusCode != http.StatusBadRequest ||
		statusError.Message != "bad request" {
		t.Fatalf("typed HTTP error=%#v", statusError)
	}
}

func TestNewValidatesAndJoinsBaseURL(t *testing.T) {
	model, err := New(Config{
		APIKey:  "secret",
		Model:   "deepseek-v4-pro",
		BaseURL: "https://deepseek.example/gateway/",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if model.endpoint != "https://deepseek.example/gateway/chat/completions" {
		t.Fatalf("endpoint=%q", model.endpoint)
	}
	for _, baseURL := range []string{
		"ftp://deepseek.example",
		"https://user:secret@deepseek.example",
		"https://deepseek.example?region=cn",
		"https://deepseek.example#fragment",
	} {
		if _, err := New(Config{APIKey: "secret", Model: "model", BaseURL: baseURL}); err == nil {
			t.Fatalf("New() accepted base URL %q", baseURL)
		}
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
