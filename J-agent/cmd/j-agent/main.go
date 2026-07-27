package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-agent/internal/runtime"
	"github.com/z-chenhao/J/J-agent/provider/openai"
	bashtool "github.com/z-chenhao/J/J-agent/tool/bash"
)

type config struct {
	provider        string
	model           string
	baseURL         string
	apiKeyEnv       string
	reasoningField  string
	reasoningEffort string
	rpc             bool
	prompt          []string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = os.Stdin.Close()
	}()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	config, err := parseConfig(args)
	if err != nil {
		return err
	}
	model, err := buildModel(config)
	if err != nil {
		return err
	}
	shell, err := bashtool.New(".")
	if err != nil {
		return err
	}
	runner, err := agent.New(model, agent.WithTools(shell))
	if err != nil {
		return err
	}
	if config.rpc {
		if len(config.prompt) > 0 {
			return errors.New("--rpc does not accept a prompt")
		}
		return runtime.RunRPC(ctx, runner, os.Stdin, os.Stdout)
	}
	return runtime.RunCLI(ctx, runner, os.Stdin, os.Stdout, os.Stderr, config.prompt...)
}

func parseConfig(args []string) (config, error) {
	flags := flag.NewFlagSet("j", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var config config
	flags.StringVar(&config.provider, "provider", env("J_AGENT_PROVIDER"), "model provider: openai")
	flags.StringVar(&config.model, "model", env("J_AGENT_MODEL"), "provider model name")
	flags.StringVar(&config.baseURL, "base-url", env("J_AGENT_BASE_URL"), "provider API base URL")
	flags.StringVar(
		&config.apiKeyEnv,
		"api-key-env",
		env("J_AGENT_API_KEY_ENV"),
		"environment variable containing the provider API key",
	)
	flags.StringVar(
		&config.reasoningField,
		"reasoning-field",
		env("J_AGENT_REASONING_FIELD"),
		"assistant reasoning history field: omit, reasoning_content, or reasoning",
	)
	flags.StringVar(
		&config.reasoningEffort,
		"reasoning-effort",
		env("J_AGENT_REASONING_EFFORT"),
		"OpenAI-compatible reasoning effort: default, none, low, medium, high, or max",
	)
	flags.BoolVar(&config.rpc, "rpc", false, "run the JSONL transport")
	if err := flags.Parse(args); err != nil {
		return config, err
	}
	config.provider = strings.ToLower(strings.TrimSpace(config.provider))
	config.model = strings.TrimSpace(config.model)
	config.baseURL = strings.TrimSpace(config.baseURL)
	config.apiKeyEnv = strings.TrimSpace(config.apiKeyEnv)
	config.reasoningField = strings.ToLower(strings.TrimSpace(config.reasoningField))
	config.reasoningEffort = strings.ToLower(strings.TrimSpace(config.reasoningEffort))
	if config.provider == "" {
		config.provider = "openai"
	}
	if config.apiKeyEnv == "" {
		config.apiKeyEnv = "OPENAI_API_KEY"
	}
	if config.reasoningField == "" {
		config.reasoningField = "omit"
	}
	if config.reasoningEffort == "" {
		config.reasoningEffort = "default"
	}
	config.prompt = flags.Args()
	if config.model == "" {
		return config, errors.New("--model or J_AGENT_MODEL is required")
	}
	if config.baseURL == "" {
		return config, errors.New("--base-url or J_AGENT_BASE_URL is required")
	}
	if config.provider != "openai" {
		return config, fmt.Errorf("unsupported provider %q", config.provider)
	}
	switch config.reasoningField {
	case "omit", "reasoning_content", "reasoning":
	default:
		return config, fmt.Errorf("unsupported reasoning field %q", config.reasoningField)
	}
	switch config.reasoningEffort {
	case "default", "none", "low", "medium", "high", "max":
	default:
		return config, fmt.Errorf("unsupported reasoning effort %q", config.reasoningEffort)
	}
	return config, nil
}

func buildModel(config config) (agent.Model, error) {
	if config.provider != "openai" {
		return nil, fmt.Errorf("unsupported provider %q", config.provider)
	}
	return openai.New(openai.Config{
		APIKey:          env(config.apiKeyEnv),
		Model:           config.model,
		BaseURL:         config.baseURL,
		ReasoningField:  parseReasoningField(config.reasoningField),
		ReasoningEffort: openai.ReasoningEffort(config.reasoningEffortValue()),
	})
}

func (config config) reasoningEffortValue() string {
	if config.reasoningEffort == "default" {
		return ""
	}
	return config.reasoningEffort
}

func parseReasoningField(value string) openai.ReasoningField {
	if value == "omit" {
		return openai.ReasoningFieldOmit
	}
	return openai.ReasoningField(value)
}

func env(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
