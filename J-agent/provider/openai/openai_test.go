package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/J/J-agent/agent"
)

func TestCompleteHandlesOpenAICompatibleToolStreamAndCacheUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%q", request.URL.Path)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer secret" {
			t.Fatalf("authorization=%q", authorization)
		}
		var payload chatRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "local-model" || !payload.Stream ||
			!payload.StreamOptions.IncludeUsage || payload.ReasoningEffort != "high" {
			t.Fatalf("payload=%#v", payload)
		}
		if len(payload.Messages) != 2 ||
			payload.Messages[0].ReasoningContent != "prior thought" ||
			payload.Messages[0].Reasoning != "" {
			t.Fatalf("messages=%#v", payload.Messages)
		}
		if len(payload.Tools) != 1 || payload.Tools[0].Function.Name != "collect" {
			t.Fatalf("tools=%#v", payload.Tools)
		}

		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(
			"data: " + `{"id":"keepalive","model":"keepalive","choices":[{"delta":{"content":""}}]}` + "\n\n" +
				"data: " + `{"id":"resp-1","model":"local-model","choices":[{"delta":{"reasoning_content":"check "}}]}` + "\n\n" +
				"data: " + `{"id":"resp-1","model":"local-model","choices":[{"delta":{"content":"using tool"}}]}` + "\n\n" +
				"data: " + `{"id":"resp-1","model":"local-model","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"collect","arguments":"{\"text\":"}}]}}]}` + "\n\n" +
				"data: " + `{"id":"resp-1","model":"local-model","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ok\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
				"data: " + `{"id":"resp-1","model":"local-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14,"prompt_tokens_details":{"cached_tokens":7}}}` + "\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer server.Close()

	model, err := New(Config{
		APIKey:          "secret",
		Model:           "local-model",
		BaseURL:         server.URL + "/v1",
		ReasoningField:  ReasoningFieldReasoningContent,
		ReasoningEffort: ReasoningEffortHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []agent.ModelDelta
	response, err := model.Complete(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{
			{
				Role: agent.RoleAssistant,
				Content: []agent.Content{
					{Type: agent.ContentReasoning, Text: "prior thought"},
					{Type: agent.ContentText, Text: "prior answer"},
				},
			},
			agent.TextMessage(agent.RoleUser, "use the tool"),
		},
		Tools: []agent.ToolSpec{{
			Name:        "collect",
			Description: "collect text",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
	}, func(delta agent.ModelDelta) {
		deltas = append(deltas, delta)
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != "openai" || response.Model != "local-model" ||
		response.ResponseID != "resp-1" || response.StopReason != agent.StopReasonToolCalls {
		t.Fatalf("response=%#v", response)
	}
	calls := response.Message.ToolCalls()
	if response.Message.Text() != "using tool" || len(calls) != 1 ||
		calls[0].ID != "call-1" || calls[0].Name != "collect" ||
		string(calls[0].Arguments) != `{"text":"ok"}` {
		t.Fatalf("message=%#v", response.Message)
	}
	if response.Usage == nil || response.Usage.CachedInputTokens == nil ||
		*response.Usage.CachedInputTokens != 7 {
		t.Fatalf("usage=%#v", response.Usage)
	}
	if len(deltas) != 4 || deltas[0].Type != agent.DeltaReasoning ||
		deltas[1].Type != agent.DeltaText ||
		deltas[2].Type != agent.DeltaToolCall ||
		deltas[3].Type != agent.DeltaToolCall {
		t.Fatalf("deltas=%#v", deltas)
	}
}

func TestCompleteHandlesOllamaReasoningField(t *testing.T) {
	var received chatRequest
	server := streamServer(t, &received,
		`{"id":"ollama-1","model":"qwen3","choices":[{"delta":{"reasoning":"inspect "}}]}`,
		`{"id":"ollama-1","model":"qwen3","choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`,
		`{"id":"ollama-1","model":"qwen3","choices":[],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`,
	)
	defer server.Close()

	model, err := New(Config{
		Model:          "qwen3",
		BaseURL:        server.URL + "/v1",
		ReasoningField: ReasoningFieldReasoning,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := model.Complete(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{{
			Role: agent.RoleAssistant,
			Content: []agent.Content{
				{Type: agent.ContentReasoning, Text: "retained"},
				{Type: agent.ContentText, Text: "answer"},
			},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(received.Messages) != 1 || received.Messages[0].Reasoning != "retained" ||
		received.Messages[0].ReasoningContent != "" {
		t.Fatalf("messages=%#v", received.Messages)
	}
	if len(response.Message.Content) != 2 ||
		response.Message.Content[0].Type != agent.ContentReasoning ||
		response.Message.Content[0].Text != "inspect " ||
		response.Message.Text() != "done" {
		t.Fatalf("message=%#v", response.Message)
	}
	if response.Usage == nil || response.Usage.CachedInputTokens != nil {
		t.Fatalf("usage=%#v", response.Usage)
	}
}

func TestCompleteHandlesDeepSeekCacheUsage(t *testing.T) {
	server := streamServer(t, nil,
		`{"id":"deepseek-1","model":"deepseek-chat","choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`,
		`{"id":"deepseek-1","model":"deepseek-chat","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":2,"total_tokens":14,"prompt_cache_hit_tokens":9,"prompt_cache_miss_tokens":3}}`,
	)
	defer server.Close()

	model, err := New(Config{Model: "deepseek-chat", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	response, err := model.Complete(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{agent.TextMessage(agent.RoleUser, "hello")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Usage == nil || response.Usage.CachedInputTokens == nil ||
		*response.Usage.CachedInputTokens != 9 {
		t.Fatalf("usage=%#v", response.Usage)
	}
}

func TestReasoningFieldOmitDoesNotReplayReasoning(t *testing.T) {
	message, err := mapMessage(agent.Message{
		Role: agent.RoleAssistant,
		Content: []agent.Content{
			{Type: agent.ContentReasoning, Text: "private"},
			{Type: agent.ContentText, Text: "visible"},
		},
	}, ReasoningFieldOmit)
	if err != nil {
		t.Fatal(err)
	}
	if message.ReasoningContent != "" || message.Reasoning != "" {
		t.Fatalf("message=%#v", message)
	}
}

func TestNewValidatesConfiguration(t *testing.T) {
	if _, err := New(Config{}); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("missing model error=%v", err)
	}
	if _, err := New(Config{Model: "local"}); err == nil || !strings.Contains(err.Error(), "base URL") {
		t.Fatalf("missing base URL error=%v", err)
	}
	for _, baseURL := range []string{
		"file://localhost/v1",
		"http://user:secret@localhost/v1",
		"http://localhost/v1?key=value",
		"http://localhost/v1#fragment",
	} {
		if _, err := New(Config{Model: "local", BaseURL: baseURL}); err == nil {
			t.Fatalf("base URL %q was accepted", baseURL)
		}
	}
	if _, err := New(Config{
		Model:          "local",
		BaseURL:        "http://localhost/v1",
		ReasoningField: "sometimes",
	}); err == nil {
		t.Fatal("invalid reasoning field was accepted")
	}
	if _, err := New(Config{
		Model:           "local",
		BaseURL:         "http://localhost/v1",
		ReasoningEffort: "extreme",
	}); err == nil {
		t.Fatal("invalid reasoning effort was accepted")
	}
}

func streamServer(t *testing.T, received *chatRequest, chunks ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if received != nil {
			if err := json.NewDecoder(request.Body).Decode(received); err != nil {
				t.Fatal(err)
			}
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			_, _ = writer.Write([]byte("data: " + chunk + "\n\n"))
		}
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	}))
}
