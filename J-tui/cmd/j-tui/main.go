package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-agent/provider/openai"
	bashtool "github.com/z-chenhao/J/J-agent/tool/bash"
	"github.com/z-chenhao/J/J-tui/internal/tui"
)

type config struct {
	mode            string
	provider        string
	model           string
	baseURL         string
	apiKeyEnv       string
	reasoningField  string
	reasoningEffort string
	prompts         []string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}
	model, err := buildModel(cfg)
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

	switch cfg.mode {
	case "json":
		return runJSON(ctx, runner, cfg.prompts, out)
	case "tui":
		if len(cfg.prompts) > 1 {
			return errors.New("tui mode accepts at most one initial prompt")
		}
		initialPrompt := ""
		if len(cfg.prompts) == 1 {
			initialPrompt = cfg.prompts[0]
		}
		program := tea.NewProgram(
			tui.New(ctx, runner, cfg.provider, cfg.model, initialPrompt),
			tea.WithContext(ctx),
		)
		_, err := program.Run()
		return err
	default:
		return fmt.Errorf("unsupported mode %q", cfg.mode)
	}
}

func parseConfig(args []string) (config, error) {
	flags := flag.NewFlagSet("j-tui", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var cfg config
	flags.StringVar(&cfg.mode, "mode", "tui", "output mode: tui or json")
	flags.StringVar(&cfg.provider, "provider", env("J_TUI_PROVIDER"), "model provider: openai")
	flags.StringVar(&cfg.model, "model", env("J_TUI_MODEL"), "provider model name")
	flags.StringVar(&cfg.baseURL, "base-url", env("J_TUI_BASE_URL"), "provider API base URL")
	flags.StringVar(
		&cfg.apiKeyEnv,
		"api-key-env",
		env("J_TUI_API_KEY_ENV"),
		"environment variable containing the provider API key",
	)
	flags.StringVar(
		&cfg.reasoningField,
		"reasoning-field",
		env("J_TUI_REASONING_FIELD"),
		"assistant reasoning history field: omit, reasoning_content, or reasoning",
	)
	flags.StringVar(
		&cfg.reasoningEffort,
		"reasoning-effort",
		env("J_TUI_REASONING_EFFORT"),
		"OpenAI-compatible reasoning effort: default, none, low, medium, high, or max",
	)
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	cfg.mode = strings.ToLower(strings.TrimSpace(cfg.mode))
	cfg.provider = strings.ToLower(strings.TrimSpace(cfg.provider))
	cfg.model = strings.TrimSpace(cfg.model)
	cfg.baseURL = strings.TrimSpace(cfg.baseURL)
	cfg.apiKeyEnv = strings.TrimSpace(cfg.apiKeyEnv)
	cfg.reasoningField = strings.ToLower(strings.TrimSpace(cfg.reasoningField))
	cfg.reasoningEffort = strings.ToLower(strings.TrimSpace(cfg.reasoningEffort))
	if cfg.provider == "" {
		cfg.provider = "openai"
	}
	if cfg.apiKeyEnv == "" {
		cfg.apiKeyEnv = "OPENAI_API_KEY"
	}
	if cfg.reasoningField == "" {
		cfg.reasoningField = "omit"
	}
	if cfg.reasoningEffort == "" {
		cfg.reasoningEffort = "default"
	}
	cfg.prompts = flags.Args()

	if cfg.model == "" {
		return config{}, errors.New("--model or J_TUI_MODEL is required")
	}
	if cfg.baseURL == "" {
		return config{}, errors.New("--base-url or J_TUI_BASE_URL is required")
	}
	if cfg.mode != "tui" && cfg.mode != "json" {
		return config{}, fmt.Errorf("unsupported mode %q", cfg.mode)
	}
	if cfg.mode == "json" && len(cfg.prompts) == 0 {
		return config{}, errors.New("json mode requires at least one prompt")
	}
	if cfg.provider != "openai" {
		return config{}, fmt.Errorf("unsupported provider %q", cfg.provider)
	}
	switch cfg.reasoningField {
	case "omit", "reasoning_content", "reasoning":
	default:
		return config{}, fmt.Errorf("unsupported reasoning field %q", cfg.reasoningField)
	}
	switch cfg.reasoningEffort {
	case "default", "none", "low", "medium", "high", "max":
	default:
		return config{}, fmt.Errorf("unsupported reasoning effort %q", cfg.reasoningEffort)
	}
	return cfg, nil
}

func buildModel(cfg config) (agent.Model, error) {
	if cfg.provider != "openai" {
		return nil, fmt.Errorf("unsupported provider %q", cfg.provider)
	}
	return openai.New(openai.Config{
		APIKey:          env(cfg.apiKeyEnv),
		Model:           cfg.model,
		BaseURL:         cfg.baseURL,
		ReasoningField:  parseReasoningField(cfg.reasoningField),
		ReasoningEffort: openai.ReasoningEffort(cfg.reasoningEffortValue()),
	})
}

func (cfg config) reasoningEffortValue() string {
	if cfg.reasoningEffort == "default" {
		return ""
	}
	return cfg.reasoningEffort
}

func parseReasoningField(value string) openai.ReasoningField {
	if value == "omit" {
		return openai.ReasoningFieldOmit
	}
	return openai.ReasoningField(value)
}

func runJSON(ctx context.Context, runner *agent.Agent, prompts []string, out io.Writer) error {
	encoder := json.NewEncoder(out)
	for _, prompt := range prompts {
		var encodeErr error
		_, err := runner.Run(ctx, prompt, func(event agent.Event) {
			if encodeErr != nil {
				return
			}
			encodeErr = encoder.Encode(projectEvent(event))
		})
		if encodeErr != nil {
			return fmt.Errorf("write JSON event: %w", encodeErr)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type traceEvent struct {
	Type       agent.EventType        `json:"type"`
	Message    *agent.Message         `json:"message,omitempty"`
	Delta      *agent.ModelDelta      `json:"delta,omitempty"`
	Model      *traceModelObservation `json:"model,omitempty"`
	ToolCall   *agent.ToolCall        `json:"toolCall,omitempty"`
	Output     string                 `json:"output,omitempty"`
	DurationMS *int64                 `json:"durationMs,omitempty"`
	IsError    bool                   `json:"isError,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

type traceModelObservation struct {
	Provider     string           `json:"provider"`
	Model        string           `json:"model"`
	ResponseID   string           `json:"responseId,omitempty"`
	StopReason   agent.StopReason `json:"stopReason"`
	Usage        *agent.Usage     `json:"usage,omitempty"`
	DurationMS   int64            `json:"durationMs"`
	FirstDeltaMS *int64           `json:"firstDeltaMs,omitempty"`
}

func projectEvent(event agent.Event) traceEvent {
	projected := traceEvent{
		Type:     event.Type,
		Message:  event.Message,
		Delta:    event.Delta,
		ToolCall: event.ToolCall,
		Output:   event.Output,
		IsError:  event.IsError,
		Error:    event.Error,
	}
	if event.Duration > 0 {
		duration := event.Duration.Milliseconds()
		projected.DurationMS = &duration
	}
	if event.Model != nil {
		projected.Model = &traceModelObservation{
			Provider:     event.Model.Provider,
			Model:        event.Model.Model,
			ResponseID:   event.Model.ResponseID,
			StopReason:   event.Model.StopReason,
			Usage:        event.Model.Usage,
			DurationMS:   event.Model.Duration.Milliseconds(),
			FirstDeltaMS: durationMilliseconds(event.Model.FirstDelta),
		}
	}
	return projected
}

func durationMilliseconds(duration *time.Duration) *int64 {
	if duration == nil {
		return nil
	}
	value := duration.Milliseconds()
	return &value
}

func env(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
