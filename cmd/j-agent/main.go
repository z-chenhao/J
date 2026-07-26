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

	"github.com/z-chenhao/J-agent/adapter/deepseek"
	"github.com/z-chenhao/J-agent/adapter/ollama"
	"github.com/z-chenhao/J-agent/agent"
	"github.com/z-chenhao/J-agent/internal/runtime"
)

type config struct {
	provider        string
	model           string
	baseURL         string
	thinking        string
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
	runner, err := agent.New(model)
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
	flags.StringVar(&config.provider, "provider", env("J_AGENT_PROVIDER"), "model provider: deepseek or ollama")
	flags.StringVar(&config.model, "model", env("J_AGENT_MODEL"), "provider model name")
	flags.StringVar(&config.baseURL, "base-url", env("J_AGENT_BASE_URL"), "provider API base URL")
	flags.StringVar(&config.thinking, "thinking", env("J_AGENT_THINKING"), "thinking mode: default, enabled, or disabled")
	flags.StringVar(
		&config.reasoningEffort,
		"reasoning-effort",
		env("J_AGENT_REASONING_EFFORT"),
		"DeepSeek reasoning effort: default, high, or max",
	)
	flags.BoolVar(&config.rpc, "rpc", false, "run the JSONL transport")
	if err := flags.Parse(args); err != nil {
		return config, err
	}
	config.provider = strings.ToLower(strings.TrimSpace(config.provider))
	config.model = strings.TrimSpace(config.model)
	config.baseURL = strings.TrimSpace(config.baseURL)
	config.thinking = strings.ToLower(strings.TrimSpace(config.thinking))
	config.reasoningEffort = strings.ToLower(strings.TrimSpace(config.reasoningEffort))
	if config.thinking == "" {
		config.thinking = "default"
	}
	if config.reasoningEffort == "" {
		config.reasoningEffort = "default"
	}
	config.prompt = flags.Args()
	if config.provider == "" {
		return config, errors.New("--provider or J_AGENT_PROVIDER is required")
	}
	if config.model == "" {
		return config, errors.New("--model or J_AGENT_MODEL is required")
	}
	switch config.thinking {
	case "default", "enabled", "disabled":
	default:
		return config, fmt.Errorf("unsupported thinking mode %q", config.thinking)
	}
	switch config.reasoningEffort {
	case "default", "high", "max":
	default:
		return config, fmt.Errorf("unsupported reasoning effort %q", config.reasoningEffort)
	}
	if config.provider != "deepseek" && config.reasoningEffort != "default" {
		return config, errors.New("--reasoning-effort is supported only by deepseek")
	}
	return config, nil
}

func buildModel(config config) (agent.Model, error) {
	switch config.provider {
	case "deepseek":
		thinking := deepseek.ThinkingDefault
		switch config.thinking {
		case "enabled":
			thinking = deepseek.ThinkingEnabled
		case "disabled":
			thinking = deepseek.ThinkingDisabled
		}
		reasoningEffort := deepseek.ReasoningDefault
		switch config.reasoningEffort {
		case "high":
			reasoningEffort = deepseek.ReasoningHigh
		case "max":
			reasoningEffort = deepseek.ReasoningMax
		}
		return deepseek.New(deepseek.Config{
			APIKey:          env("DEEPSEEK_API_KEY"),
			Model:           config.model,
			BaseURL:         config.baseURL,
			Thinking:        thinking,
			ReasoningEffort: reasoningEffort,
		})
	case "ollama":
		thinking := ollama.ThinkingDefault
		switch config.thinking {
		case "enabled":
			thinking = ollama.ThinkingEnabled
		case "disabled":
			thinking = ollama.ThinkingDisabled
		}
		return ollama.New(ollama.Config{
			Model:    config.model,
			BaseURL:  config.baseURL,
			Thinking: thinking,
		})
	default:
		return nil, fmt.Errorf("unsupported provider %q", config.provider)
	}
}

func env(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
