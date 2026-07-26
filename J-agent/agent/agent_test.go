package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type scriptedModel struct {
	outputs  []ModelResponse
	deltas   [][]ModelDelta
	requests []ModelRequest
}

type modelFunc func(context.Context, ModelRequest, func(ModelDelta)) (ModelResponse, error)

func (function modelFunc) Complete(
	ctx context.Context,
	request ModelRequest,
	emit func(ModelDelta),
) (ModelResponse, error) {
	return function(ctx, request, emit)
}

func (m *scriptedModel) Complete(
	_ context.Context,
	request ModelRequest,
	emit func(ModelDelta),
) (ModelResponse, error) {
	m.requests = append(m.requests, request)
	if len(m.outputs) == 0 {
		return ModelResponse{}, errors.New("no scripted output")
	}
	if len(m.deltas) > 0 {
		for _, delta := range m.deltas[0] {
			if emit != nil {
				emit(delta)
			}
		}
		m.deltas = m.deltas[1:]
	}
	output := m.outputs[0]
	m.outputs = m.outputs[1:]
	return output, nil
}

type collectTool struct {
	arguments map[string]string
	calls     int
}

func (t *collectTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "collect",
		Description: "returns a text value",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
	}
}

func (t *collectTool) Call(_ context.Context, arguments json.RawMessage) (string, error) {
	t.calls++
	if err := json.Unmarshal(arguments, &t.arguments); err != nil {
		return "", err
	}
	return t.arguments["text"], nil
}

func response(message Message, reason StopReason) ModelResponse {
	return ModelResponse{
		Message:    message,
		Provider:   "test",
		Model:      "test-model",
		StopReason: reason,
		Usage: &Usage{
			InputTokens:  2,
			OutputTokens: 1,
			TotalTokens:  3,
		},
	}
}

func toolMessage(id, name, arguments string) Message {
	call := ToolCall{ID: id, Name: name, Arguments: json.RawMessage(arguments)}
	return Message{
		Role: RoleAssistant,
		Content: []Content{{
			Type:     ContentToolCall,
			ToolCall: &call,
		}},
	}
}

func TestRunProvidesToolSpecsAndExecutesTool(t *testing.T) {
	model := &scriptedModel{outputs: []ModelResponse{
		response(toolMessage("call-1", "collect", `{"text":"hello"}`), StopReasonToolCalls),
		response(TextMessage(RoleAssistant, "done"), StopReasonStop),
	}}
	tool := &collectTool{}
	runner, err := New(model, WithTools(tool), WithSystemPrompt("system"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	result, err := runner.Run(context.Background(), "use the tool", nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.Message.Text() != "done" {
		t.Fatalf("result=%q, want done", result.Message.Text())
	}
	if result.Turns != 2 || result.Usage == nil || result.Usage.TotalTokens != 6 {
		t.Fatalf("unexpected run diagnostics: %#v", result)
	}
	if len(model.requests) != 2 || len(model.requests[0].Tools) != 1 {
		t.Fatalf("model did not receive tool specs: %#v", model.requests)
	}
	if tool.arguments["text"] != "hello" {
		t.Fatalf("tool arguments=%v", tool.arguments)
	}

	history := runner.History()
	if len(history) != 5 {
		t.Fatalf("history length=%d, want 5", len(history))
	}
	if history[3].Role != RoleTool || history[3].ToolCallID != "call-1" || history[3].Text() != "hello" {
		t.Fatalf("unexpected tool result: %#v", history[3])
	}
}

func TestRunEmitsStreamingLifecycleAndObservation(t *testing.T) {
	model := &scriptedModel{
		outputs: []ModelResponse{response(TextMessage(RoleAssistant, "done"), StopReasonStop)},
		deltas:  [][]ModelDelta{{{Type: DeltaText, Index: 0, Delta: "done"}}},
	}
	runner, err := New(model)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	var events []Event
	_, err = runner.Run(context.Background(), "hello", func(event Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	want := []EventType{
		EventAgentStarted,
		EventTurnStarted,
		EventMessageStarted,
		EventMessageDelta,
		EventMessageCompleted,
		EventTurnCompleted,
		EventAgentCompleted,
	}
	if len(events) != len(want) {
		t.Fatalf("events=%v, want %v", events, want)
	}
	for i := range want {
		if events[i].Type != want[i] {
			t.Fatalf("events[%d]=%q, want %q", i, events[i].Type, want[i])
		}
	}
	observation := events[5].Model
	if observation == nil || observation.Provider != "test" || observation.StopReason != StopReasonStop {
		t.Fatalf("turn observation=%#v", observation)
	}
}

func TestRunTerminatesStartedMessageAndTurnOnStreamFailure(t *testing.T) {
	model := modelFunc(func(
		_ context.Context,
		_ ModelRequest,
		emit func(ModelDelta),
	) (ModelResponse, error) {
		emit(ModelDelta{Type: DeltaText, Index: 0, Delta: "partial"})
		return ModelResponse{}, errors.New("stream failed")
	})
	runner, err := New(model)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	var events []EventType
	_, err = runner.Run(context.Background(), "hello", func(event Event) {
		events = append(events, event.Type)
	})
	if err == nil {
		t.Fatal("expected stream failure")
	}
	want := []EventType{
		EventAgentStarted,
		EventTurnStarted,
		EventMessageStarted,
		EventMessageDelta,
		EventMessageFailed,
		EventTurnFailed,
		EventAgentFailed,
	}
	if len(events) != len(want) {
		t.Fatalf("events=%v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events[%d]=%q, want %q", index, events[index], want[index])
		}
	}
}

func TestHistoryIsDeepCopy(t *testing.T) {
	model := &scriptedModel{outputs: []ModelResponse{
		response(toolMessage("call-1", "missing", `{"value":"original"}`), StopReasonToolCalls),
		response(TextMessage(RoleAssistant, "done"), StopReasonStop),
	}}
	runner, err := New(model)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if _, err := runner.Run(context.Background(), "hello", nil); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	first := runner.History()
	first[1].Content[0].ToolCall.Arguments[0] = 'x'
	second := runner.History()
	if got := string(second[1].Content[0].ToolCall.Arguments); got != `{"value":"original"}` {
		t.Fatalf("history aliases caller memory: %s", got)
	}
}

func TestWithHistoryRestoresAndContinuesConversation(t *testing.T) {
	history := []Message{
		TextMessage(RoleSystem, "system"),
		TextMessage(RoleUser, "first"),
		TextMessage(RoleAssistant, "first answer"),
	}
	model := &scriptedModel{outputs: []ModelResponse{
		response(TextMessage(RoleAssistant, "second answer"), StopReasonStop),
	}}
	runner, err := New(model, WithHistory(history...))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	history[2].Content[0].Text = "mutated"
	result, err := runner.Run(context.Background(), "second", nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.Message.Text() != "second answer" {
		t.Fatalf("result=%q, want second answer", result.Message.Text())
	}
	if len(model.requests) != 1 || len(model.requests[0].Messages) != 4 {
		t.Fatalf("request messages=%#v", model.requests)
	}
	if got := model.requests[0].Messages[2].Text(); got != "first answer" {
		t.Fatalf("restored history aliases caller memory: %q", got)
	}
	if got := model.requests[0].Messages[3].Text(); got != "second" {
		t.Fatalf("new prompt=%q, want second", got)
	}
}

func TestWithHistoryRestoresCompletedToolRound(t *testing.T) {
	history := []Message{
		TextMessage(RoleUser, "collect"),
		toolMessage("call-1", "collect", `{"text":"saved"}`),
		func() Message {
			message := TextMessage(RoleTool, "saved")
			message.ToolCallID = "call-1"
			message.ToolName = "collect"
			return message
		}(),
		TextMessage(RoleAssistant, "done"),
	}
	runner, err := New(&scriptedModel{}, WithHistory(history...))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if got := runner.History(); len(got) != len(history) || got[2].ToolCallID != "call-1" {
		t.Fatalf("restored history=%#v", got)
	}
}

func TestWithHistoryRejectsAmbiguousSystemPrompt(t *testing.T) {
	_, err := New(
		&scriptedModel{},
		WithSystemPrompt("new system"),
		WithHistory(TextMessage(RoleUser, "saved")),
	)
	if err == nil {
		t.Fatal("expected system prompt and history conflict")
	}
}

func TestWithHistoryRejectsInvalidTranscript(t *testing.T) {
	toolResult := func(id, name string) Message {
		message := TextMessage(RoleTool, "result")
		message.ToolCallID = id
		message.ToolName = name
		return message
	}
	tests := []struct {
		name    string
		history []Message
	}{
		{
			name: "system message after user",
			history: []Message{
				TextMessage(RoleUser, "hello"),
				TextMessage(RoleSystem, "late"),
			},
		},
		{
			name: "assistant without input",
			history: []Message{
				TextMessage(RoleAssistant, "hello"),
			},
		},
		{
			name: "orphan tool result",
			history: []Message{
				TextMessage(RoleUser, "hello"),
				toolResult("call-1", "collect"),
			},
		},
		{
			name: "unresolved tool call",
			history: []Message{
				TextMessage(RoleUser, "hello"),
				toolMessage("call-1", "collect", `{}`),
			},
		},
		{
			name: "mismatched tool result name",
			history: []Message{
				TextMessage(RoleUser, "hello"),
				toolMessage("call-1", "collect", `{}`),
				toolResult("call-1", "other"),
			},
		},
		{
			name: "duplicate tool call identity",
			history: []Message{
				TextMessage(RoleUser, "first"),
				toolMessage("call-1", "collect", `{}`),
				toolResult("call-1", "collect"),
				TextMessage(RoleAssistant, "done"),
				TextMessage(RoleUser, "second"),
				toolMessage("call-1", "collect", `{}`),
				toolResult("call-1", "collect"),
			},
		},
		{
			name: "tool call in user message",
			history: []Message{
				{
					Role: RoleUser,
					Content: []Content{{
						Type: ContentToolCall,
						ToolCall: &ToolCall{
							ID:        "call-1",
							Name:      "collect",
							Arguments: json.RawMessage(`{}`),
						},
					}},
				},
			},
		},
		{
			name: "invalid tool arguments",
			history: []Message{
				TextMessage(RoleUser, "hello"),
				toolMessage("call-1", "collect", `[]`),
				toolResult("call-1", "collect"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(&scriptedModel{}, WithHistory(test.history...)); err == nil {
				t.Fatal("expected invalid history error")
			}
		})
	}
}

func TestNewRejectsAmbiguousTools(t *testing.T) {
	tool := &collectTool{}
	if _, err := New(&scriptedModel{}, WithTools(tool, tool)); err == nil {
		t.Fatal("expected duplicate tool error")
	}
}

func TestRunRejectsInvalidToolCallIdentity(t *testing.T) {
	model := &scriptedModel{outputs: []ModelResponse{
		response(toolMessage("", "collect", `{}`), StopReasonToolCalls),
	}}
	runner, err := New(model)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if _, err := runner.Run(context.Background(), "hello", nil); err == nil {
		t.Fatal("expected invalid tool call error")
	}
}

func TestToolRoundLimitAllowsFinalAnswer(t *testing.T) {
	model := &scriptedModel{outputs: []ModelResponse{
		response(toolMessage("call-1", "collect", `{"text":"first"}`), StopReasonToolCalls),
		response(TextMessage(RoleAssistant, "done"), StopReasonStop),
	}}
	tool := &collectTool{}
	runner, err := New(model, WithTools(tool), WithMaxToolRounds(1))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	result, err := runner.Run(context.Background(), "hello", nil)
	if err != nil {
		t.Fatalf("Run() error=%v", err)
	}
	if result.Message.Text() != "done" || result.Turns != 2 || tool.calls != 1 {
		t.Fatalf("result=%#v, tool calls=%d", result, tool.calls)
	}
}

func TestDefaultToolRoundLimitAllowsExtendedRun(t *testing.T) {
	const toolRounds = 16
	outputs := make([]ModelResponse, 0, toolRounds+1)
	for round := range toolRounds {
		outputs = append(outputs, response(
			toolMessage(
				fmt.Sprintf("call-%d", round),
				"collect",
				fmt.Sprintf(`{"text":"round-%d"}`, round),
			),
			StopReasonToolCalls,
		))
	}
	outputs = append(outputs, response(TextMessage(RoleAssistant, "done"), StopReasonStop))

	model := &scriptedModel{outputs: outputs}
	tool := &collectTool{}
	runner, err := New(model, WithTools(tool))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	result, err := runner.Run(context.Background(), "hello", nil)
	if err != nil {
		t.Fatalf("Run() error=%v", err)
	}
	if result.Message.Text() != "done" || result.Turns != toolRounds+1 {
		t.Fatalf("result=%#v", result)
	}
	if tool.calls != toolRounds {
		t.Fatalf("tool calls=%d, want %d", tool.calls, toolRounds)
	}
}

func TestToolRoundLimitRejectsNextToolWithoutPersistingOrExecutingIt(t *testing.T) {
	model := &scriptedModel{outputs: []ModelResponse{
		response(toolMessage("call-1", "collect", `{"text":"first"}`), StopReasonToolCalls),
		response(toolMessage("call-2", "collect", `{"text":"second"}`), StopReasonToolCalls),
	}}
	tool := &collectTool{}
	runner, err := New(model, WithTools(tool), WithMaxToolRounds(1))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	var events []EventType
	result, err := runner.Run(context.Background(), "hello", func(event Event) {
		events = append(events, event.Type)
	})
	if !errors.Is(err, ErrToolRoundLimit) {
		t.Fatalf("Run() error=%v, want ErrToolRoundLimit", err)
	}
	if !strings.Contains(err.Error(), "1 completed rounds") {
		t.Fatalf("Run() error=%q, want configured round count", err)
	}
	if tool.calls != 1 {
		t.Fatalf("tool calls=%d, want 1", tool.calls)
	}
	history := runner.History()
	if len(history) != 3 || history[2].Role != RoleTool || history[2].ToolCallID != "call-1" {
		t.Fatalf("history contains over-limit tool call: %#v", history)
	}
	if result.Turns != 1 {
		t.Fatalf("completed turns=%d, want 1", result.Turns)
	}
	wantTail := []EventType{EventMessageFailed, EventTurnFailed, EventAgentFailed}
	if len(events) < len(wantTail) {
		t.Fatalf("events=%v", events)
	}
	for index, want := range wantTail {
		got := events[len(events)-len(wantTail)+index]
		if got != want {
			t.Fatalf("terminal events=%v, want tail %v", events, wantTail)
		}
	}
}

func TestResetClearsHistory(t *testing.T) {
	model := &scriptedModel{outputs: []ModelResponse{
		response(TextMessage(RoleAssistant, "done"), StopReasonStop),
	}}
	runner, err := New(model)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if _, err := runner.Run(context.Background(), "hello", nil); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	runner.Reset()
	if history := runner.History(); len(history) != 0 {
		t.Fatalf("history after Reset()=%#v", history)
	}
}

func TestRunRejectsEmptyModelOutput(t *testing.T) {
	model := &scriptedModel{outputs: []ModelResponse{
		response(Message{Role: RoleAssistant}, StopReasonStop),
	}}
	runner, err := New(model)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if _, err := runner.Run(context.Background(), "hello", nil); !errors.Is(err, ErrEmptyModelOutput) {
		t.Fatalf("Run() error=%v, want ErrEmptyModelOutput", err)
	}
}

func TestRunHonorsCanceledContext(t *testing.T) {
	model := &scriptedModel{outputs: []ModelResponse{
		response(TextMessage(RoleAssistant, "unused"), StopReasonStop),
	}}
	runner, err := New(model)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.Run(ctx, "hello", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error=%v, want context.Canceled", err)
	}
	if len(model.requests) != 0 {
		t.Fatal("model was called after context cancellation")
	}
}
