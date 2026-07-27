package main

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestParseConfigRequiresModelAndBaseURL(t *testing.T) {
	t.Setenv("J_AGENT_PROVIDER", "")
	t.Setenv("J_AGENT_API", "")
	t.Setenv("J_AGENT_API_VERSION", "")
	t.Setenv("J_AGENT_MODEL", "")
	t.Setenv("J_AGENT_BASE_URL", "")
	if _, err := parseConfig(nil); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("error=%v", err)
	}
	if _, err := parseConfig([]string{"--model", "qwen3"}); err == nil ||
		!strings.Contains(err.Error(), "base-url") {
		t.Fatalf("error=%v", err)
	}
}

func TestParseConfigReturnsHelpWithoutValidation(t *testing.T) {
	if _, err := parseConfig([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parseConfig() error=%v, want flag.ErrHelp", err)
	}
}

func TestParseConfigUsesOpenAIProviderOptions(t *testing.T) {
	t.Setenv("J_AGENT_PROVIDER", "")
	t.Setenv("J_AGENT_API", "")
	t.Setenv("J_AGENT_API_VERSION", "")
	t.Setenv("J_AGENT_MODEL", "")
	t.Setenv("J_AGENT_BASE_URL", "")
	config, err := parseConfig([]string{
		"--provider", "openai",
		"--model", "qwen3",
		"--base-url", "http://127.0.0.1:8000/v1",
		"--api-key-env", "OMLX_API_KEY",
		"--reasoning-field", "reasoning_content",
		"--reasoning-effort", "high",
		"hello",
	})
	if err != nil {
		t.Fatalf("parseConfig() error: %v", err)
	}
	if config.provider != "openai" || config.api != "openai-completions" ||
		config.model != "qwen3" ||
		config.apiKeyEnv != "OMLX_API_KEY" ||
		config.reasoningField != "reasoning_content" ||
		config.reasoningEffort != "high" ||
		strings.Join(config.prompt, " ") != "hello" {
		t.Fatalf("config=%#v", config)
	}
}

func TestParseConfigUsesAzureOpenAICompletionsAPI(t *testing.T) {
	t.Setenv("J_AGENT_PROVIDER", "")
	t.Setenv("J_AGENT_API", "")
	t.Setenv("J_AGENT_API_VERSION", "")
	t.Setenv("J_AGENT_MODEL", "")
	t.Setenv("J_AGENT_BASE_URL", "")
	config, err := parseConfig([]string{
		"--api", "azure-openai-completions",
		"--api-version", "2024-02-01",
		"--model", "gpt-5.5-2026-04-24",
		"--base-url", "https://example.invalid/modelhub",
		"--api-key-env", "GPT_5_5_API_KEY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.api != "azure-openai-completions" ||
		config.apiVersion != "2024-02-01" ||
		config.apiKeyEnv != "GPT_5_5_API_KEY" {
		t.Fatalf("config=%#v", config)
	}
}

func TestBuildOpenAIProviderUsesOptionalEnvironmentCredential(t *testing.T) {
	t.Setenv("OMLX_API_KEY", "")
	if _, err := buildModel(config{
		provider:        "openai",
		model:           "Qwen3.6-35B-A3B-oQ4e-mtp",
		baseURL:         "http://127.0.0.1:8000/v1",
		apiKeyEnv:       "OMLX_API_KEY",
		reasoningField:  "reasoning_content",
		reasoningEffort: "default",
	}); err != nil {
		t.Fatalf("buildModel() error=%v", err)
	}
}

func TestParseConfigRejectsFormerProviderNames(t *testing.T) {
	t.Setenv("J_AGENT_PROVIDER", "")
	t.Setenv("J_AGENT_API", "")
	t.Setenv("J_AGENT_API_VERSION", "")
	t.Setenv("J_AGENT_MODEL", "")
	t.Setenv("J_AGENT_BASE_URL", "")
	_, err := parseConfig([]string{
		"--provider", "ollama",
		"--model", "qwen3",
		"--base-url", "http://127.0.0.1:11434/v1",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("error=%v", err)
	}
}
