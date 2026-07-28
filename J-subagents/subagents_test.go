package subagents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/z-chenhao/J/J-agent/agent"
)

func TestToolRunsFreshIsolatedSubagent(t *testing.T) {
	model := &recordingModel{}
	subagentTool, err := NewTool(Definition{
		Name:         "research",
		Description:  "Research one bounded question.",
		Model:        model,
		SystemPrompt: "You are a researcher.",
		Tools:        []agent.Tool{staticTool{name: "lookup"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := subagentTool.Spec()
	if spec.Name != "subagent_run" ||
		!strings.Contains(spec.Description, "research: Research one bounded question.") ||
		!strings.Contains(string(spec.InputSchema), `"enum":["research"]`) {
		t.Fatalf("spec=%#v", spec)
	}

	for _, task := range []string{"first", "second"} {
		output, err := subagentTool.Call(
			context.Background(),
			json.RawMessage(`{"agent":"research","task":"`+task+`"}`),
		)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Agent   string       `json:"agent"`
			Content string       `json:"content"`
			Turns   int          `json:"turns"`
			Usage   *agent.Usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(output), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Agent != "research" || decoded.Content != "done: "+task ||
			decoded.Turns != 1 || decoded.Usage == nil ||
			decoded.Usage.TotalTokens != 3 {
			t.Fatalf("result=%#v", decoded)
		}
	}

	requests := model.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}
	for index, request := range requests {
		if len(request.Messages) != 2 ||
			request.Messages[0].Role != agent.RoleSystem ||
			request.Messages[0].Text() != "You are a researcher." ||
			request.Messages[1].Role != agent.RoleUser ||
			len(request.Tools) != 1 || request.Tools[0].Name != "lookup" {
			t.Fatalf("request[%d]=%#v", index, request)
		}
	}
}

func TestToolRejectsInvalidCalls(t *testing.T) {
	subagentTool, err := NewTool(Definition{
		Name:        "plain",
		Description: "Answer plainly.",
		Model:       &recordingModel{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"unknown agent": `{"agent":"missing","task":"work"}`,
		"empty task":    `{"agent":"plain","task":" "}`,
		"unknown field": `{"agent":"plain","task":"work","extra":true}`,
		"trailing data": `{"agent":"plain","task":"work"} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := subagentTool.Call(
				context.Background(),
				json.RawMessage(input),
			); err == nil {
				t.Fatal("invalid call was accepted")
			}
		})
	}
}

func TestToolPropagatesCancellation(t *testing.T) {
	model := blockingModel{}
	subagentTool, err := NewTool(Definition{
		Name:        "slow",
		Description: "Wait for cancellation.",
		Model:       model,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = subagentTool.Call(
		ctx,
		json.RawMessage(`{"agent":"slow","task":"wait"}`),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestToolCancellationInterruptsSerializationWait(t *testing.T) {
	model := &serialModel{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	subagentTool, err := NewTool(Definition{
		Name:        "serial",
		Description: "Serialize model calls.",
		Model:       model,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, callErr := subagentTool.Call(
			context.Background(),
			json.RawMessage(`{"agent":"serial","task":"first"}`),
		)
		firstDone <- callErr
	}()
	<-model.started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = subagentTool.Call(
		ctx,
		json.RawMessage(`{"agent":"serial","task":"second"}`),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	close(model.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestNewToolValidatesDefinitions(t *testing.T) {
	valid := Definition{
		Name:        "same",
		Description: "Valid.",
		Model:       &recordingModel{},
	}
	tests := map[string][]Definition{
		"empty":             nil,
		"blank name":        {{Name: " ", Description: "Valid.", Model: &recordingModel{}}},
		"unsafe name":       {{Name: "bad name", Description: "Valid.", Model: &recordingModel{}}},
		"punctuation start": {{Name: ".bad", Description: "Valid.", Model: &recordingModel{}}},
		"blank description": {{Name: "x", Description: " ", Model: &recordingModel{}}},
		"nil model":         {{Name: "x", Description: "Valid."}},
		"duplicate":         {valid, valid},
		"invalid tool": {{
			Name:        "x",
			Description: "Valid.",
			Model:       &recordingModel{},
			Tools:       []agent.Tool{staticTool{name: ""}},
		}},
	}
	for name, definitions := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NewTool(definitions...); err == nil {
				t.Fatal("invalid definitions were accepted")
			}
		})
	}
}

type recordingModel struct {
	mu       sync.Mutex
	requests []agent.ModelRequest
}

func (model *recordingModel) Complete(
	_ context.Context,
	request agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	model.mu.Lock()
	model.requests = append(model.requests, request)
	model.mu.Unlock()
	task := request.Messages[len(request.Messages)-1].Text()
	return agent.ModelResponse{
		Message:    agent.TextMessage(agent.RoleAssistant, "done: "+task),
		Provider:   "test",
		Model:      "test",
		StopReason: agent.StopReasonStop,
		Usage: &agent.Usage{
			InputTokens:  2,
			OutputTokens: 1,
			TotalTokens:  3,
		},
	}, nil
}

func (model *recordingModel) Requests() []agent.ModelRequest {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]agent.ModelRequest(nil), model.requests...)
}

type blockingModel struct{}

func (blockingModel) Complete(
	ctx context.Context,
	_ agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	<-ctx.Done()
	return agent.ModelResponse{}, ctx.Err()
}

type serialModel struct {
	started chan struct{}
	release chan struct{}
}

func (model *serialModel) Complete(
	ctx context.Context,
	request agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	close(model.started)
	select {
	case <-ctx.Done():
		return agent.ModelResponse{}, ctx.Err()
	case <-model.release:
	}
	return agent.ModelResponse{
		Message:    agent.TextMessage(agent.RoleAssistant, request.Messages[len(request.Messages)-1].Text()),
		Provider:   "test",
		Model:      "test",
		StopReason: agent.StopReasonStop,
	}, nil
}

type staticTool struct {
	name string
}

func (tool staticTool) Spec() agent.ToolSpec {
	return agent.ToolSpec{
		Name:        tool.name,
		Description: "Static test tool.",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (staticTool) Call(context.Context, json.RawMessage) (string, error) {
	return "ok", nil
}
