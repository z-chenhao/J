package main

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestParseConfigRequiresExplicitProviderAndModel(t *testing.T) {
	t.Setenv("J_AGENT_PROVIDER", "")
	t.Setenv("J_AGENT_MODEL", "")
	if _, err := parseConfig(nil); err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("error=%v", err)
	}
	if _, err := parseConfig([]string{"--provider", "ollama"}); err == nil ||
		!strings.Contains(err.Error(), "model") {
		t.Fatalf("error=%v", err)
	}
}

func TestParseConfigReturnsHelpWithoutValidation(t *testing.T) {
	if _, err := parseConfig([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parseConfig() error=%v, want flag.ErrHelp", err)
	}
}

func TestParseConfigUsesTypedThinkingMode(t *testing.T) {
	t.Setenv("J_AGENT_PROVIDER", "")
	t.Setenv("J_AGENT_MODEL", "")
	config, err := parseConfig([]string{
		"--provider", "ollama",
		"--model", "qwen3",
		"--thinking", "enabled",
		"hello",
	})
	if err != nil {
		t.Fatalf("parseConfig() error: %v", err)
	}
	if config.provider != "ollama" || config.model != "qwen3" ||
		config.thinking != "enabled" || config.reasoningEffort != "default" ||
		strings.Join(config.prompt, " ") != "hello" {
		t.Fatalf("config=%#v", config)
	}
}

func TestBuildDeepSeekModelRequiresEnvironmentCredential(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	_, err := buildModel(config{
		provider:        "deepseek",
		model:           "deepseek-v4-pro",
		thinking:        "default",
		reasoningEffort: "default",
	})
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("error=%v", err)
	}
}

func TestParseConfigRejectsDeepSeekOnlyEffortForOllama(t *testing.T) {
	t.Setenv("J_AGENT_PROVIDER", "")
	t.Setenv("J_AGENT_MODEL", "")
	_, err := parseConfig([]string{
		"--provider", "ollama",
		"--model", "qwen3",
		"--reasoning-effort", "max",
	})
	if err == nil || !strings.Contains(err.Error(), "only by deepseek") {
		t.Fatalf("error=%v", err)
	}
}
