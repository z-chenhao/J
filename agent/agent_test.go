package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type scriptedModel struct {
	outputs  []Message
	requests []ModelRequest
}

func (m *scriptedModel) Complete(_ context.Context, request ModelRequest) (Message, error) {
	m.requests = append(m.requests, request)
	if len(m.outputs) == 0 {
		return Message{}, errors.New("no scripted output")
	}
	output := m.outputs[0]
	m.outputs = m.outputs[1:]
	return output, nil
}

type collectTool struct {
	arguments map[string]string
}

func (t *collectTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "collect",
		Description: "returns a text value",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
	}
}

func (t *collectTool) Call(_ context.Context, arguments json.RawMessage) (string, error) {
	if err := json.Unmarshal(arguments, &t.arguments); err != nil {
		return "", err
	}
	return t.arguments["text"], nil
}

func TestRunProvidesToolSpecsAndExecutesTool(t *testing.T) {
	model := &scriptedModel{outputs: []Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "collect",
				Arguments: json.RawMessage(`{"text":"hello"}`),
			}},
		},
		{Role: RoleAssistant, Content: "done"},
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
	if result.Content != "done" {
		t.Fatalf("result=%q, want done", result.Content)
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
	if history[3].Role != RoleTool || history[3].ToolCallID != "call-1" {
		t.Fatalf("unexpected tool result: %#v", history[3])
	}
}

func TestRunEmitsBalancedLifecycle(t *testing.T) {
	model := &scriptedModel{outputs: []Message{{Role: RoleAssistant, Content: "done"}}}
	runner, err := New(model)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	var events []EventType
	_, err = runner.Run(context.Background(), "hello", func(event Event) {
		events = append(events, event.Type)
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	want := []EventType{
		EventAgentStarted,
		EventTurnStarted,
		EventMessageCreated,
		EventTurnCompleted,
		EventAgentCompleted,
	}
	if len(events) != len(want) {
		t.Fatalf("events=%v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events[%d]=%q, want %q", i, events[i], want[i])
		}
	}
}

func TestHistoryIsDeepCopy(t *testing.T) {
	model := &scriptedModel{outputs: []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{{
			ID:        "call-1",
			Name:      "missing",
			Arguments: json.RawMessage(`{"value":"original"}`),
		}},
	}, {Role: RoleAssistant, Content: "done"}}}
	runner, err := New(model)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if _, err := runner.Run(context.Background(), "hello", nil); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	first := runner.History()
	first[1].ToolCalls[0].Arguments[0] = 'x'
	second := runner.History()
	if string(second[1].ToolCalls[0].Arguments) != `{"value":"original"}` {
		t.Fatalf("history aliases caller memory: %s", second[1].ToolCalls[0].Arguments)
	}
}

func TestNewRejectsAmbiguousTools(t *testing.T) {
	tool := &collectTool{}
	if _, err := New(&scriptedModel{}, WithTools(tool, tool)); err == nil {
		t.Fatal("expected duplicate tool error")
	}
}

func TestRunRejectsInvalidToolCallIdentity(t *testing.T) {
	model := &scriptedModel{outputs: []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{{
			Name:      "collect",
			Arguments: json.RawMessage(`{}`),
		}},
	}}}
	runner, err := New(model)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if _, err := runner.Run(context.Background(), "hello", nil); err == nil {
		t.Fatal("expected invalid tool call error")
	}
}

func TestToolRoundLimitIsExplicit(t *testing.T) {
	model := &scriptedModel{outputs: []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{{
			ID:        "call-1",
			Name:      "missing",
			Arguments: json.RawMessage(`{}`),
		}},
	}}}
	runner, err := New(model, WithMaxToolRounds(1))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if _, err := runner.Run(context.Background(), "hello", nil); !errors.Is(err, ErrToolRoundLimit) {
		t.Fatalf("Run() error=%v, want ErrToolRoundLimit", err)
	}
}

func TestResetClearsHistory(t *testing.T) {
	tool := &collectTool{}
	model := &scriptedModel{outputs: []Message{{Role: RoleAssistant, Content: "done"}}}
	runner, err := New(model, WithTools(tool))
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
	model := &scriptedModel{outputs: []Message{{Role: RoleAssistant}}}
	runner, err := New(model)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if _, err := runner.Run(context.Background(), "hello", nil); !errors.Is(err, ErrEmptyModelOutput) {
		t.Fatalf("Run() error=%v, want ErrEmptyModelOutput", err)
	}
}

func TestRunHonorsCanceledContext(t *testing.T) {
	model := &scriptedModel{outputs: []Message{{Role: RoleAssistant, Content: "unused"}}}
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
