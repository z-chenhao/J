package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/z-chenhao/J/J-agent/agent"
)

type echoTool struct{}

func (echoTool) Spec() agent.ToolSpec {
	return agent.ToolSpec{
		Name:        "community_echo",
		Description: "Echo text from a community-owned Tool.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"text":{"type":"string"}},
			"required":["text"],
			"additionalProperties":false
		}`),
	}
}

func (echoTool) Call(_ context.Context, arguments json.RawMessage) (string, error) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return "", err
	}
	return input.Text, nil
}

type exampleModel struct{}

func (exampleModel) Complete(
	_ context.Context,
	request agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	last := request.Messages[len(request.Messages)-1]
	if last.Role == agent.RoleTool {
		return agent.ModelResponse{
			Message:    agent.TextMessage(agent.RoleAssistant, "community host: "+last.Text()),
			Provider:   "example",
			Model:      "scripted",
			StopReason: agent.StopReasonStop,
		}, nil
	}
	return agent.ModelResponse{
		Message: agent.Message{
			Role: agent.RoleAssistant,
			Content: []agent.Content{{
				Type: agent.ContentToolCall,
				ToolCall: &agent.ToolCall{
					ID:        "community-call",
					Name:      "community_echo",
					Arguments: json.RawMessage(`{"text":"assembled outside J"}`),
				},
			}},
		},
		Provider:   "example",
		Model:      "scripted",
		StopReason: agent.StopReasonToolCalls,
	}, nil
}

type recordingModel struct {
	inner agent.Model

	mu    sync.Mutex
	calls int
}

func (model *recordingModel) Complete(
	ctx context.Context,
	request agent.ModelRequest,
	emit func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	model.mu.Lock()
	model.calls++
	model.mu.Unlock()
	return model.inner.Complete(ctx, request, emit)
}

func (model *recordingModel) Calls() int {
	model.mu.Lock()
	defer model.mu.Unlock()
	return model.calls
}

func assemble(model agent.Model, history []agent.Message) (*agent.Agent, error) {
	options := []agent.Option{agent.WithTools(echoTool{})}
	if len(history) > 0 {
		options = append(options, agent.WithHistory(history...))
	} else {
		options = append(options, agent.WithSystemPrompt(
			"You are assembled by an application outside the J repository.",
		))
	}
	return agent.New(model, options...)
}

func run(ctx context.Context) error {
	model := &recordingModel{inner: exampleModel{}}
	runner, err := assemble(model, nil)
	if err != nil {
		return err
	}
	result, err := runner.Run(ctx, "demonstrate composition", func(event agent.Event) {
		if event.Type != agent.EventMessageDelta {
			_, _ = fmt.Fprintln(os.Stderr, event.Type)
		}
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, result.Message.Text())
	return err
}

func main() {
	if err := run(context.Background()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
