package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/z-chenhao/J/J-agent/agent"
)

type staticModel struct{}

func (staticModel) Complete(
	_ context.Context,
	request agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	if len(request.Messages) == 0 {
		return agent.ModelResponse{}, errors.New("messages missing")
	}
	return agent.ModelResponse{
		Message:    agent.TextMessage(agent.RoleAssistant, "eight"),
		Provider:   "test",
		Model:      "qwen",
		StopReason: agent.StopReasonStop,
	}, nil
}

func TestObservingModelCapturesExactFrame(t *testing.T) {
	observed := &observingModel{inner: staticModel{}}
	request := agent.ModelRequest{
		Messages: []agent.Message{agent.TextMessage(agent.RoleUser, "spider")},
		Tools: []agent.ToolSpec{{
			Name:        "bash",
			Description: "run command",
			InputSchema: []byte(`{"type":"object"}`),
		}},
	}
	response, err := observed.Complete(context.Background(), request, func(agent.ModelDelta) {})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Text() != "eight" || len(observed.frames) != 1 ||
		observed.frames[0].Request.Tools[0].Name != "bash" {
		t.Fatalf("response=%#v frames=%#v", response, observed.frames)
	}
}

func TestLoadAPIKeyPrefersEnvironmentAndDoesNotExposeIt(t *testing.T) {
	t.Setenv("TEST_OMLX_KEY", "environment-secret")
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"auth":{"api_key":"file-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := loadAPIKey(config{apiKeyEnv: "TEST_OMLX_KEY", omlxSettings: path})
	if err != nil {
		t.Fatal(err)
	}
	if key != "environment-secret" {
		t.Fatalf("key=%q", key)
	}
}

func TestProjectTranscriptIsExplicitlyOptInProjection(t *testing.T) {
	messages := []agent.Message{
		agent.TextMessage(agent.RoleUser, "question"),
		{Role: agent.RoleAssistant, Content: []agent.Content{
			{Type: agent.ContentReasoning, Text: "private reasoning"},
			{Type: agent.ContentText, Text: "answer"},
		}},
	}
	projected := projectTranscript(messages)
	if len(projected) != 2 || projected[1].Content != "private reasoning\nanswer" {
		t.Fatalf("projected=%#v", projected)
	}
}
