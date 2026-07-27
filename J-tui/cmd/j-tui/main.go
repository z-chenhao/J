package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-agent/provider/openai"
	"github.com/z-chenhao/J/J-tui/internal/settings"
	"github.com/z-chenhao/J/J-tui/internal/tui"
)

type config struct {
	configPath      string
	profile         string
	initConfig      bool
	listTools       bool
	mode            string
	provider        string
	api             string
	apiVersion      string
	model           string
	baseURL         string
	apiKeyEnv       string
	reasoningField  string
	reasoningEffort string
	session         string
	noSession       bool
	extensions      *settings.Extensions
	memory          *settings.Memory
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

func run(ctx context.Context, args []string, out io.Writer) (runErr error) {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}
	if cfg.initConfig {
		if err := settings.WriteDefault(cfg.configPath); err != nil {
			return err
		}
		_, err := fmt.Fprintf(
			out,
			"Created %s\nEdit the profiles if needed, then run j-tui.\n",
			cfg.configPath,
		)
		return err
	}
	if cfg.listTools {
		composition, err := composeRuntime(ctx, cfg)
		if err != nil {
			return err
		}
		defer func() {
			runErr = errors.Join(runErr, composition.Close())
		}()
		return writeMCPToolList(out, composition.mcpTools)
	}
	if err := ensureSession(&cfg); err != nil {
		return err
	}
	model, err := buildModel(cfg)
	if err != nil {
		return err
	}
	composition, err := composeRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, composition.Close())
	}()
	options := []agent.Option{agent.WithTools(composition.tools...)}
	if len(composition.history) > 0 {
		options = append(options, agent.WithHistory(composition.history...))
	}
	agentRunner, err := agent.New(model, options...)
	if err != nil {
		return err
	}
	var runner conversationRunner = agentRunner
	if composition.transcripts != nil {
		runner = &persistentRunner{
			runner:    agentRunner,
			store:     composition.transcripts,
			sessionID: cfg.session,
		}
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
			tui.New(ctx, runner, cfg.provider, cfg.model, initialPrompt, cfg.session),
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
	var values config
	flags.StringVar(&values.configPath, "config", "", "configuration file path")
	flags.StringVar(&values.profile, "profile", "", "named model profile")
	flags.BoolVar(&values.initConfig, "init-config", false, "create a starter configuration and exit")
	flags.BoolVar(
		&values.listTools,
		"list-tools",
		false,
		"list configured MCP tools and selection state, then exit",
	)
	flags.StringVar(&values.mode, "mode", "tui", "output mode: tui or json")
	flags.StringVar(&values.provider, "provider", "", "model provider: openai")
	flags.StringVar(
		&values.api,
		"api",
		"",
		"provider API: openai-completions or azure-openai-completions",
	)
	flags.StringVar(&values.apiVersion, "api-version", "", "provider API version")
	flags.StringVar(&values.model, "model", "", "provider model name")
	flags.StringVar(&values.baseURL, "base-url", "", "provider API base URL")
	flags.StringVar(
		&values.apiKeyEnv,
		"api-key-env",
		"",
		"environment variable containing the provider API key",
	)
	flags.StringVar(
		&values.reasoningField,
		"reasoning-field",
		"",
		"assistant reasoning history field: omit, reasoning_content, or reasoning",
	)
	flags.StringVar(
		&values.reasoningEffort,
		"reasoning-effort",
		"",
		"OpenAI-compatible reasoning effort: default, none, low, medium, high, or max",
	)
	flags.StringVar(
		&values.session,
		"session",
		"",
		"transcript session to restore and persist",
	)
	flags.BoolVar(
		&values.noSession,
		"no-session",
		false,
		"disable transcript persistence for this invocation",
	)
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	visited := make(map[string]bool)
	flags.Visit(func(value *flag.Flag) {
		visited[value.Name] = true
	})

	defaultPath, err := settings.DefaultPath()
	if err != nil {
		return config{}, err
	}
	configPath := env("J_TUI_CONFIG")
	if configPath == "" {
		configPath = defaultPath
	}
	if visited["config"] {
		configPath = strings.TrimSpace(values.configPath)
		if configPath == "" {
			return config{}, errors.New("--config requires a non-empty path")
		}
	}
	if values.initConfig {
		if len(flags.Args()) > 0 {
			return config{}, errors.New("--init-config does not accept a prompt")
		}
		if visited["session"] {
			return config{}, errors.New("--init-config does not accept --session")
		}
		if visited["no-session"] {
			return config{}, errors.New("--init-config does not accept --no-session")
		}
		if values.listTools {
			return config{}, errors.New("--init-config does not accept --list-tools")
		}
		return config{
			configPath: configPath,
			initConfig: true,
			mode:       "tui",
		}, nil
	}

	cfg := config{
		configPath:      configPath,
		listTools:       values.listTools,
		noSession:       values.noSession,
		mode:            "tui",
		provider:        "openai",
		api:             string(openai.APICompletions),
		reasoningField:  "omit",
		reasoningEffort: "default",
	}
	profileName := env("J_TUI_PROFILE")
	if visited["profile"] {
		profileName = strings.TrimSpace(values.profile)
	}
	file, fileLoaded, err := loadSettings(configPath)
	if err != nil {
		explicitConfig := visited["config"] || env("J_TUI_CONFIG") != ""
		if !errors.Is(err, os.ErrNotExist) || explicitConfig || profileName != "" {
			return config{}, err
		}
	}
	apiKeySpecified := false
	if fileLoaded {
		resolvedName, profile, err := file.Resolve(profileName)
		if err != nil {
			return config{}, fmt.Errorf("resolve J-tui config %q: %w", configPath, err)
		}
		cfg.profile = resolvedName
		cfg.provider = profile.Provider
		cfg.api = profile.API
		cfg.apiVersion = profile.APIVersion
		cfg.model = profile.Model
		cfg.baseURL = profile.BaseURL
		cfg.apiKeyEnv = profile.APIKeyEnv
		cfg.reasoningField = profile.ReasoningField
		cfg.reasoningEffort = profile.ReasoningEffort
		cfg.extensions = file.Extensions
		cfg.memory = file.Memory
		apiKeySpecified = true
	}
	applyEnvironment(&cfg, &apiKeySpecified)
	applyFlags(&cfg, values, visited, &apiKeySpecified)
	cfg.prompts = flags.Args()

	cfg.mode = strings.ToLower(strings.TrimSpace(cfg.mode))
	cfg.provider = strings.ToLower(strings.TrimSpace(cfg.provider))
	cfg.api = strings.ToLower(strings.TrimSpace(cfg.api))
	cfg.apiVersion = strings.TrimSpace(cfg.apiVersion)
	cfg.model = strings.TrimSpace(cfg.model)
	cfg.baseURL = strings.TrimSpace(cfg.baseURL)
	cfg.apiKeyEnv = strings.TrimSpace(cfg.apiKeyEnv)
	cfg.reasoningField = strings.ToLower(strings.TrimSpace(cfg.reasoningField))
	cfg.reasoningEffort = strings.ToLower(strings.TrimSpace(cfg.reasoningEffort))
	cfg.session = strings.TrimSpace(cfg.session)
	if cfg.listTools {
		if len(cfg.prompts) > 0 {
			return config{}, errors.New("--list-tools does not accept a prompt")
		}
		if visited["mode"] {
			return config{}, errors.New("--list-tools does not accept --mode")
		}
		if cfg.session != "" {
			return config{}, errors.New("--list-tools does not accept --session")
		}
		if visited["no-session"] {
			return config{}, errors.New("--list-tools does not accept --no-session")
		}
		if cfg.extensions == nil || cfg.extensions.MCP == nil {
			return config{}, errors.New("--list-tools requires extensions.mcp in the configuration")
		}
		return cfg, nil
	}
	if cfg.api == "" {
		cfg.api = string(openai.APICompletions)
	}
	if !apiKeySpecified {
		if cfg.api == string(openai.APIAzureCompletions) {
			cfg.apiKeyEnv = "AZURE_OPENAI_API_KEY"
		} else {
			cfg.apiKeyEnv = "OPENAI_API_KEY"
		}
	}
	if cfg.reasoningField == "" {
		cfg.reasoningField = "omit"
	}
	if cfg.reasoningEffort == "" {
		cfg.reasoningEffort = "default"
	}
	if cfg.model == "" {
		return config{}, errors.New(
			"model is required; select a configured profile, set --model/J_TUI_MODEL, or run --init-config",
		)
	}
	if cfg.baseURL == "" {
		return config{}, errors.New(
			"base URL is required; select a configured profile, set --base-url/J_TUI_BASE_URL, or run --init-config",
		)
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
	switch openai.API(cfg.api) {
	case openai.APICompletions:
		if cfg.apiVersion != "" {
			return config{}, errors.New("openai-completions does not use --api-version")
		}
	case openai.APIAzureCompletions:
		if cfg.apiVersion == "" {
			return config{}, errors.New(
				"Azure OpenAI API version is required; set apiVersion, --api-version, or J_TUI_API_VERSION",
			)
		}
	default:
		return config{}, fmt.Errorf("unsupported provider API %q", cfg.api)
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
	if cfg.session != "" &&
		(cfg.memory == nil || cfg.memory.Transcript == nil) {
		return config{}, errors.New(
			"--session requires memory.transcript in the selected configuration",
		)
	}
	if cfg.noSession && cfg.session != "" {
		return config{}, errors.New("--no-session cannot be combined with --session or J_TUI_SESSION")
	}
	return cfg, nil
}

func ensureSession(cfg *config) error {
	if cfg == nil || cfg.noSession || cfg.session != "" ||
		cfg.memory == nil || cfg.memory.Transcript == nil {
		return nil
	}
	var entropy [6]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return fmt.Errorf("create transcript session ID: %w", err)
	}
	cfg.session = time.Now().UTC().Format("20060102T150405.000000000Z") +
		"-" + hex.EncodeToString(entropy[:])
	return nil
}

func writeMCPToolList(out io.Writer, observations []mcpToolObservation) error {
	sort.Slice(observations, func(left, right int) bool {
		if observations[left].Server == observations[right].Server {
			return observations[left].Name < observations[right].Name
		}
		return observations[left].Server < observations[right].Server
	})
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "SERVER\tTOOL\tSELECTED"); err != nil {
		return fmt.Errorf("write MCP tool list: %w", err)
	}
	for _, observation := range observations {
		selected := "no"
		if observation.Selected {
			selected = "yes"
		}
		if _, err := fmt.Fprintf(
			writer,
			"%s\t%s\t%s\n",
			sanitizeTableCell(observation.Server),
			sanitizeTableCell(observation.Name),
			selected,
		); err != nil {
			return fmt.Errorf("write MCP tool list: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("write MCP tool list: %w", err)
	}
	return nil
}

func sanitizeTableCell(value string) string {
	return strings.Map(func(character rune) rune {
		if character < ' ' || character == '\u007f' {
			return '\uFFFD'
		}
		return character
	}, value)
}

func loadSettings(path string) (settings.File, bool, error) {
	file, err := settings.Load(path)
	if err != nil {
		return settings.File{}, false, err
	}
	return file, true, nil
}

func applyEnvironment(cfg *config, apiKeySpecified *bool) {
	for name, target := range map[string]*string{
		"J_TUI_PROVIDER":         &cfg.provider,
		"J_TUI_API":              &cfg.api,
		"J_TUI_API_VERSION":      &cfg.apiVersion,
		"J_TUI_MODEL":            &cfg.model,
		"J_TUI_BASE_URL":         &cfg.baseURL,
		"J_TUI_REASONING_FIELD":  &cfg.reasoningField,
		"J_TUI_REASONING_EFFORT": &cfg.reasoningEffort,
		"J_TUI_SESSION":          &cfg.session,
	} {
		if value := env(name); value != "" {
			*target = value
		}
	}
	if value := env("J_TUI_API_KEY_ENV"); value != "" {
		cfg.apiKeyEnv = value
		*apiKeySpecified = true
	}
}

func applyFlags(
	cfg *config,
	values config,
	visited map[string]bool,
	apiKeySpecified *bool,
) {
	for name, pair := range map[string]struct {
		target *string
		value  string
	}{
		"mode":             {&cfg.mode, values.mode},
		"provider":         {&cfg.provider, values.provider},
		"api":              {&cfg.api, values.api},
		"api-version":      {&cfg.apiVersion, values.apiVersion},
		"model":            {&cfg.model, values.model},
		"base-url":         {&cfg.baseURL, values.baseURL},
		"reasoning-field":  {&cfg.reasoningField, values.reasoningField},
		"reasoning-effort": {&cfg.reasoningEffort, values.reasoningEffort},
		"session":          {&cfg.session, values.session},
	} {
		if visited[name] {
			*pair.target = pair.value
		}
	}
	if visited["api-key-env"] {
		cfg.apiKeyEnv = values.apiKeyEnv
		*apiKeySpecified = true
	}
}

func buildModel(cfg config) (agent.Model, error) {
	if cfg.provider != "openai" {
		return nil, fmt.Errorf("unsupported provider %q", cfg.provider)
	}
	return openai.New(openai.Config{
		APIKey:          env(cfg.apiKeyEnv),
		API:             openai.API(cfg.api),
		APIVersion:      cfg.apiVersion,
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

func runJSON(ctx context.Context, runner conversationRunner, prompts []string, out io.Writer) error {
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
