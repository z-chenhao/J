package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/z-chenhao/J/J-agent/agent"
)

func TestParseConfigJSON(t *testing.T) {
	isolateConfig(t)
	t.Setenv("J_TUI_MODEL", "")
	t.Setenv("J_TUI_BASE_URL", "")
	cfg, err := parseConfig([]string{
		"--mode", "json",
		"--provider", "openai",
		"--model", "qwen3.6:27b-q4_K_M",
		"--base-url", "http://127.0.0.1:11434/v1",
		"--reasoning-field", "reasoning",
		"hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.mode != "json" || cfg.provider != "openai" ||
		cfg.model != "qwen3.6:27b-q4_K_M" ||
		cfg.reasoningField != "reasoning" || len(cfg.prompts) != 1 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestParseConfigAcceptsDeepSeekThroughOpenAIProvider(t *testing.T) {
	isolateConfig(t)
	t.Setenv("J_TUI_MODEL", "")
	t.Setenv("J_TUI_BASE_URL", "")
	cfg, err := parseConfig([]string{
		"--provider", "openai",
		"--model", "deepseek-v4-flash",
		"--base-url", "https://api.deepseek.com",
		"--api-key-env", "DEEPSEEK_API_KEY",
		"--reasoning-field", "reasoning_content",
		"--reasoning-effort", "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.provider != "openai" || cfg.apiKeyEnv != "DEEPSEEK_API_KEY" ||
		cfg.reasoningField != "reasoning_content" || cfg.reasoningEffort != "high" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestParseConfigAcceptsOMLXThroughOpenAIProvider(t *testing.T) {
	isolateConfig(t)
	t.Setenv("J_TUI_MODEL", "")
	t.Setenv("J_TUI_BASE_URL", "")
	cfg, err := parseConfig([]string{
		"--provider", "openai",
		"--model", "Qwen3.6-35B-A3B-oQ4e-mtp",
		"--base-url", "http://127.0.0.1:8000/v1",
		"--api-key-env", "OMLX_API_KEY",
		"--reasoning-field", "reasoning_content",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.provider != "openai" || cfg.model != "Qwen3.6-35B-A3B-oQ4e-mtp" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestParseConfigRejectsFormerProviderNames(t *testing.T) {
	isolateConfig(t)
	t.Setenv("J_TUI_MODEL", "")
	t.Setenv("J_TUI_BASE_URL", "")
	_, err := parseConfig([]string{
		"--provider", "ollama",
		"--model", "qwen",
		"--base-url", "http://127.0.0.1:11434/v1",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseConfigRejectsMissingJSONPrompt(t *testing.T) {
	isolateConfig(t)
	t.Setenv("J_TUI_MODEL", "")
	t.Setenv("J_TUI_BASE_URL", "")
	_, err := parseConfig([]string{
		"--mode", "json",
		"--model", "qwen",
		"--base-url", "http://127.0.0.1:11434/v1",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseConfigLoadsProfileAndAppliesPrecedence(t *testing.T) {
	home := isolateConfig(t)
	path := filepath.Join(home, ".j", "config.json")
	writeConfig(t, path, `{
		"defaultProfile": "local",
		"profiles": {
			"local": {
				"provider": "openai",
				"model": "profile-model",
				"baseURL": "http://profile.example/v1",
				"apiKeyEnv": "PROFILE_API_KEY",
				"reasoningField": "reasoning_content"
			}
		}
	}`)
	t.Setenv("J_TUI_MODEL", "environment-model")
	cfg, err := parseConfig([]string{"--model", "flag-model"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.profile != "local" || cfg.model != "flag-model" ||
		cfg.baseURL != "http://profile.example/v1" ||
		cfg.apiKeyEnv != "PROFILE_API_KEY" {
		t.Fatalf("config=%#v", cfg)
	}
}

func TestParseConfigSelectsNamedProfile(t *testing.T) {
	home := isolateConfig(t)
	path := filepath.Join(home, ".j", "config.json")
	writeConfig(t, path, `{
		"defaultProfile": "local",
		"profiles": {
			"local": {
				"provider": "openai",
				"model": "local-model",
				"baseURL": "http://127.0.0.1:8000/v1"
			},
			"remote": {
				"provider": "openai",
				"model": "remote-model",
				"baseURL": "https://example.com/v1",
				"apiKeyEnv": "REMOTE_API_KEY"
			}
		}
	}`)
	cfg, err := parseConfig([]string{"--profile", "remote"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.profile != "remote" || cfg.model != "remote-model" ||
		cfg.apiKeyEnv != "REMOTE_API_KEY" {
		t.Fatalf("config=%#v", cfg)
	}
	local, err := parseConfig([]string{"--profile", "local"})
	if err != nil {
		t.Fatal(err)
	}
	if local.apiKeyEnv != "" {
		t.Fatalf("local API key environment=%q", local.apiKeyEnv)
	}
}

func TestParseConfigRequiresExplicitConfigToExist(t *testing.T) {
	isolateConfig(t)
	_, err := parseConfig([]string{
		"--config", filepath.Join(t.TempDir(), "missing.json"),
		"--model", "qwen",
		"--base-url", "http://127.0.0.1:8000/v1",
	})
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error=%v", err)
	}
}

func TestRunInitializesConfig(t *testing.T) {
	isolateConfig(t)
	path := filepath.Join(t.TempDir(), ".j", "config.json")
	var output bytes.Buffer
	if err := run(context.Background(), []string{
		"--init-config",
		"--config", path,
	}, &output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"defaultProfile": "omlx"`) ||
		!strings.Contains(output.String(), path) {
		t.Fatalf("config=%s output=%q", data, output.String())
	}
	if err := run(context.Background(), []string{
		"--init-config",
		"--config", path,
	}, &output); err == nil {
		t.Fatal("existing config was overwritten")
	}
}

func TestRunJSONEmitsCompleteToolLifecycle(t *testing.T) {
	runner, err := agent.New(
		&scriptedModel{},
		agent.WithTools(staticTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := runJSON(context.Background(), runner, []string{"use the tool"}, &output); err != nil {
		t.Fatal(err)
	}

	var got []agent.EventType
	sawToolDuration := false
	decoder := json.NewDecoder(&output)
	for decoder.More() {
		var event traceEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		got = append(got, event.Type)
		if event.Type == agent.EventToolCompleted && event.DurationMS != nil {
			sawToolDuration = true
		}
	}
	want := []agent.EventType{
		agent.EventAgentStarted,
		agent.EventTurnStarted,
		agent.EventMessageStarted,
		agent.EventMessageDelta,
		agent.EventMessageCompleted,
		agent.EventToolStarted,
		agent.EventToolCompleted,
		agent.EventTurnCompleted,
		agent.EventTurnStarted,
		agent.EventMessageStarted,
		agent.EventMessageDelta,
		agent.EventMessageCompleted,
		agent.EventTurnCompleted,
		agent.EventAgentCompleted,
	}
	if len(got) != len(want) {
		t.Fatalf("event count = %d, want %d\n%v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("event %d = %q, want %q\n%v", index, got[index], want[index], got)
		}
	}
	if !sawToolDuration {
		t.Fatal("tool.completed did not include durationMs")
	}
}

func TestRunJSONEmitsFailureLifecycle(t *testing.T) {
	runner, err := agent.New(failingModel{})
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err = runJSON(context.Background(), runner, []string{"fail"}, &output)
	if err == nil {
		t.Fatal("expected an error")
	}

	var got []agent.EventType
	decoder := json.NewDecoder(&output)
	for decoder.More() {
		var event traceEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		got = append(got, event.Type)
	}
	want := []agent.EventType{
		agent.EventAgentStarted,
		agent.EventTurnStarted,
		agent.EventMessageStarted,
		agent.EventMessageDelta,
		agent.EventMessageFailed,
		agent.EventTurnFailed,
		agent.EventAgentFailed,
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("event %d = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestRunJSONPreservesCacheableConversationPrefix(t *testing.T) {
	model := &recordingModel{}
	runner, err := agent.New(model)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := runJSON(
		context.Background(),
		runner,
		[]string{"first prompt", "second prompt"},
		&output,
	); err != nil {
		t.Fatal(err)
	}

	if len(model.requests) != 2 {
		t.Fatalf("requests = %d", len(model.requests))
	}
	first := model.requests[0].Messages
	second := model.requests[1].Messages
	if len(first) != 1 || len(second) != 3 {
		t.Fatalf("message lengths = %d, %d", len(first), len(second))
	}
	if !reflect.DeepEqual(first, second[:len(first)]) {
		t.Fatalf("first request is not a prefix of second:\n%#v\n%#v", first, second)
	}
	if second[1].Role != agent.RoleAssistant || second[1].Text() != "response 1" {
		t.Fatalf("assistant continuation = %#v", second[1])
	}
}

type scriptedModel struct {
	turn int
}

func (model *scriptedModel) Complete(
	_ context.Context,
	_ agent.ModelRequest,
	emit func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	model.turn++
	if model.turn == 1 {
		call := agent.ToolCall{
			ID:        "call-1",
			Name:      "probe",
			Arguments: json.RawMessage(`{"value":"ok"}`),
		}
		emit(agent.ModelDelta{
			Type:       agent.DeltaToolCall,
			Index:      0,
			Delta:      `{"value":"ok"}`,
			ToolCallID: call.ID,
			ToolName:   call.Name,
		})
		return agent.ModelResponse{
			Message: agent.Message{
				Role: agent.RoleAssistant,
				Content: []agent.Content{{
					Type:     agent.ContentToolCall,
					ToolCall: &call,
				}},
			},
			Provider:   "test",
			Model:      "scripted",
			StopReason: agent.StopReasonToolCalls,
		}, nil
	}
	emit(agent.ModelDelta{Type: agent.DeltaText, Index: 0, Delta: "done"})
	return agent.ModelResponse{
		Message:    agent.TextMessage(agent.RoleAssistant, "done"),
		Provider:   "test",
		Model:      "scripted",
		StopReason: agent.StopReasonStop,
	}, nil
}

type failingModel struct{}

func (failingModel) Complete(
	_ context.Context,
	_ agent.ModelRequest,
	emit func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	emit(agent.ModelDelta{Type: agent.DeltaText, Index: 0, Delta: "partial"})
	return agent.ModelResponse{}, errors.New("provider failed")
}

type staticTool struct{}

func (staticTool) Spec() agent.ToolSpec {
	return agent.ToolSpec{
		Name:        "probe",
		Description: "return a diagnostic value",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}}}`),
	}
}

func (staticTool) Call(context.Context, json.RawMessage) (string, error) {
	return "tool ok", nil
}

type recordingModel struct {
	requests []agent.ModelRequest
}

func (model *recordingModel) Complete(
	_ context.Context,
	request agent.ModelRequest,
	emit func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	model.requests = append(model.requests, request)
	text := "response " + fmt.Sprint(len(model.requests))
	emit(agent.ModelDelta{Type: agent.DeltaText, Delta: text})
	cached := int64(0)
	return agent.ModelResponse{
		Message:    agent.TextMessage(agent.RoleAssistant, text),
		Provider:   "test",
		Model:      "cache",
		StopReason: agent.StopReasonStop,
		Usage: &agent.Usage{
			InputTokens:       int64(len(request.Messages)),
			OutputTokens:      1,
			TotalTokens:       int64(len(request.Messages) + 1),
			CachedInputTokens: &cached,
		},
	}, nil
}

func isolateConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{
		"J_TUI_CONFIG",
		"J_TUI_PROFILE",
		"J_TUI_PROVIDER",
		"J_TUI_MODEL",
		"J_TUI_BASE_URL",
		"J_TUI_API_KEY_ENV",
		"J_TUI_REASONING_FIELD",
		"J_TUI_REASONING_EFFORT",
	} {
		t.Setenv(name, "")
	}
	return home
}

func writeConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
