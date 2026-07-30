package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-agent/provider/openai"
	bashtool "github.com/z-chenhao/J/J-agent/tool/bash"
	"github.com/z-chenhao/J/J-space/internal/artifact"
	"github.com/z-chenhao/J/J-space/internal/replay"
)

const (
	defaultModel         = "Qwen3.6-35B-A3B-oQ4e-mtp"
	defaultModelRepo     = "Qwen/Qwen3.6-35B-A3B"
	defaultLensRepo      = "stanleytheli/qwen3.6-35B-A3B-jlens"
	defaultBaseURL       = "http://127.0.0.1:8000/v1"
	defaultQuantization  = "oQ4e/oQ5e mixed affine"
	defaultProbeFidelity = "exact J-agent messages and tool schemas; post-hoc full-prefix replay; " +
		"separate MLX process; MTP/cache kernel numerics may differ"
)

type config struct {
	label            string
	prompt           string
	stateDir         string
	model            string
	baseURL          string
	apiKeyEnv        string
	omlxSettings     string
	modelPath        string
	lensPath         string
	probePython      string
	probeScript      string
	tailPositions    int
	retainTranscript bool
	skipProbe        bool
}

type observingModel struct {
	inner  agent.Model
	frames []probeFrame
}

type probeFrame = replay.Frame
type probeOutput = replay.Output

type omlxSettings struct {
	Auth struct {
		APIKey string `json:"api_key"`
	} `json:"auth"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	config, err := parseConfig(arguments)
	if err != nil {
		return err
	}
	apiKey, err := loadAPIKey(config)
	if err != nil {
		return err
	}
	inner, err := openai.New(openai.Config{
		APIKey:         apiKey,
		API:            openai.APICompletions,
		Model:          config.model,
		BaseURL:        config.baseURL,
		ReasoningField: openai.ReasoningFieldReasoningContent,
	})
	if err != nil {
		return err
	}
	observed := &observingModel{inner: inner}
	shell, err := bashtool.New(".")
	if err != nil {
		return err
	}
	runner, err := agent.New(observed, agent.WithTools(shell))
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	trace := artifact.Trace{
		SchemaVersion: artifact.SchemaVersion,
		ID:            newRunID(now),
		Label:         config.label,
		CreatedAt:     now,
		UpdatedAt:     now,
		Status:        "running",
		Agent: artifact.Agent{
			Commit: repositoryCommit(),
			Model:  config.model,
		},
		Measurement: artifact.Measurement{
			Kind:                "pending",
			Probe:               "mlx-jacobian-lens",
			ModelCheckpoint:     defaultModelRepo,
			RuntimeQuantization: defaultQuantization,
			LensRepository:      defaultLensRepo,
			ContextFidelity:     defaultProbeFidelity,
			Layers:              40,
			ResidualWidth:       2048,
			VocabularySize:      248320,
		},
		Events: []artifact.AgentEvent{},
	}
	runsDirectory := filepath.Join(config.stateDir, "runs")
	if err := artifact.WriteAtomic(runsDirectory, trace); err != nil {
		return err
	}

	started := time.Now()
	var sequence int64
	result, runErr := runner.Run(ctx, config.prompt, func(event agent.Event) {
		if event.Type == agent.EventMessageDelta {
			return
		}
		sequence++
		projected := artifact.AgentEvent{
			Sequence:    sequence,
			OffsetMS:    time.Since(started).Milliseconds(),
			Type:        string(event.Type),
			IsError:     event.IsError,
			OutputBytes: len(event.Output),
		}
		if event.Duration > 0 {
			duration := event.Duration.Milliseconds()
			projected.DurationMS = &duration
		}
		if event.ToolCall != nil {
			projected.Tool = event.ToolCall.Name
		}
		if event.Model != nil {
			projected.Model = event.Model.Model
			projected.StopReason = string(event.Model.StopReason)
			if event.Model.Usage != nil {
				content, _ := json.Marshal(event.Model.Usage)
				_ = json.Unmarshal(content, &projected.Usage)
			}
		}
		trace.Events = append(trace.Events, projected)
		_ = artifact.WriteAtomic(runsDirectory, trace)
	})

	if config.retainTranscript {
		trace.Transcript = projectTranscript(runner.History())
	}
	if runErr != nil {
		trace.Status = "failed"
		trace.Failure = safeFailure(runErr)
	} else {
		trace.Status = "probing"
		trace.Notes = append(trace.Notes, fmt.Sprintf(
			"agent completed in %s across %d model turn(s)",
			time.Since(started).Round(time.Millisecond),
			result.Turns,
		))
	}
	if err := artifact.WriteAtomic(runsDirectory, trace); err != nil {
		return err
	}

	if config.skipProbe || len(observed.frames) == 0 {
		trace.Measurement.Kind = "unavailable"
		if runErr == nil {
			trace.Status = "partial"
		}
		trace.Notes = append(trace.Notes, "J-lens probe was skipped")
		if err := artifact.WriteAtomic(runsDirectory, trace); err != nil {
			return err
		}
		if runErr != nil {
			return runErr
		}
		fmt.Println(filepath.Join(runsDirectory, trace.ID+".json"))
		return nil
	}

	measured, probeErr := runProbe(ctx, config, observed.frames)
	if probeErr != nil {
		trace.Measurement.Kind = "unavailable"
		trace.Status = "partial"
		trace.Notes = append(trace.Notes, "probe failed: "+safeFailure(probeErr))
	} else {
		trace.Measurement = measured.Measurement
		trace.Turns = measured.Turns
		trace.Notes = append(trace.Notes, measured.Notes...)
		if runErr == nil {
			trace.Status = "completed"
		} else {
			trace.Status = "partial"
		}
	}
	if err := artifact.WriteAtomic(runsDirectory, trace); err != nil {
		return err
	}
	fmt.Println(filepath.Join(runsDirectory, trace.ID+".json"))
	if runErr != nil {
		return runErr
	}
	if probeErr != nil {
		return probeErr
	}
	return nil
}

func (model *observingModel) Complete(
	ctx context.Context,
	request agent.ModelRequest,
	emit func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	response, err := model.inner.Complete(ctx, request, emit)
	if err == nil {
		model.frames = append(model.frames, probeFrame{Request: request, Response: response})
	}
	return response, err
}

func parseConfig(arguments []string) (config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config{}, err
	}
	moduleDirectory, err := findModuleDirectory()
	if err != nil {
		return config{}, err
	}
	flags := flag.NewFlagSet("jspace-record", flag.ContinueOnError)
	var value config
	flags.StringVar(&value.label, "label", "", "safe public run label")
	flags.StringVar(&value.prompt, "prompt", "", "local J-agent prompt")
	flags.StringVar(&value.stateDir, "state-dir",
		env("JSPACE_STATE_DIR", filepath.Join(home, ".j", "jspace")), "research state directory")
	flags.StringVar(&value.model, "model", env("JSPACE_MODEL", defaultModel), "oMLX model id")
	flags.StringVar(&value.baseURL, "base-url", env("JSPACE_BASE_URL", defaultBaseURL), "local model API")
	flags.StringVar(&value.apiKeyEnv, "api-key-env", env("JSPACE_API_KEY_ENV", "OMLX_API_KEY"), "model API key environment variable")
	flags.StringVar(&value.omlxSettings, "omlx-settings",
		env("JSPACE_OMLX_SETTINGS", filepath.Join(home, ".omlx", "settings.json")), "oMLX settings path")
	flags.StringVar(&value.modelPath, "model-path",
		env("JSPACE_MODEL_PATH", filepath.Join(home, ".omlx", "models", "Jundot", defaultModel)), "instrumented MLX checkpoint")
	flags.StringVar(&value.lensPath, "lens-path",
		env("JSPACE_LENS_PATH", filepath.Join(home, ".j", "jspace", "lenses", "qwen3.6-35B-A3B", "lens.pt")), "fitted Jacobian lens")
	flags.StringVar(&value.probePython, "probe-python",
		env("JSPACE_PROBE_PYTHON", "/opt/homebrew/opt/omlx/libexec/bin/python3.11"), "Python with oMLX and MLX")
	flags.StringVar(&value.probeScript, "probe-script",
		filepath.Join(moduleDirectory, "probe", "probe.py"), "MLX probe program")
	flags.IntVar(&value.tailPositions, "tail-positions", 18, "maximum sampled tail tokens per turn")
	flags.BoolVar(&value.retainTranscript, "retain-transcript", false, "persist sensitive transcript locally")
	flags.BoolVar(&value.skipProbe, "skip-probe", false, "record Agent events without loading model internals")
	if err := flags.Parse(arguments); err != nil {
		return config{}, err
	}
	if flags.NArg() > 0 {
		if value.prompt != "" {
			return config{}, errors.New("pass the prompt either with --prompt or as arguments")
		}
		value.prompt = strings.Join(flags.Args(), " ")
	}
	value.prompt = strings.TrimSpace(value.prompt)
	if value.prompt == "" {
		return config{}, errors.New("prompt is required")
	}
	if strings.TrimSpace(value.label) == "" {
		value.label = "Local J-agent run"
	}
	if value.tailPositions < 1 || value.tailPositions > 64 {
		return config{}, errors.New("tail-positions must be between 1 and 64")
	}
	return value, nil
}

func runProbe(ctx context.Context, config config, frames []probeFrame) (probeOutput, error) {
	measured, err := replay.Run(ctx, replay.Config{
		Python:        config.probePython,
		Script:        config.probeScript,
		ModelPath:     config.modelPath,
		LensPath:      config.lensPath,
		ModelID:       config.model,
		ModelRepo:     defaultModelRepo,
		LensRepo:      defaultLensRepo,
		TailPositions: config.tailPositions,
	}, frames)
	if err != nil {
		return probeOutput{}, err
	}
	if measured.Measurement.Kind != "posthoc_replay" {
		return probeOutput{}, fmt.Errorf("probe returned measurement kind %q", measured.Measurement.Kind)
	}
	return measured, nil
}

func loadAPIKey(config config) (string, error) {
	if key := strings.TrimSpace(os.Getenv(config.apiKeyEnv)); key != "" {
		return key, nil
	}
	content, err := os.ReadFile(config.omlxSettings)
	if err != nil {
		return "", fmt.Errorf("read oMLX settings: %w", err)
	}
	var settings omlxSettings
	if err := json.Unmarshal(content, &settings); err != nil {
		return "", fmt.Errorf("decode oMLX settings: %w", err)
	}
	key := strings.TrimSpace(settings.Auth.APIKey)
	if key == "" {
		return "", errors.New("oMLX API key is not configured")
	}
	return key, nil
}

func projectTranscript(messages []agent.Message) []artifact.Message {
	projected := make([]artifact.Message, 0, len(messages))
	for _, message := range messages {
		var parts []string
		for _, content := range message.Content {
			switch content.Type {
			case agent.ContentText, agent.ContentReasoning:
				parts = append(parts, content.Text)
			case agent.ContentToolCall:
				if content.ToolCall != nil {
					parts = append(parts, content.ToolCall.Name+" "+string(content.ToolCall.Arguments))
				}
			}
		}
		projected = append(projected, artifact.Message{
			Role:    string(message.Role),
			Content: strings.Join(parts, "\n"),
		})
	}
	return projected
}

func repositoryCommit() string {
	command := exec.Command("git", "rev-parse", "--short=12", "HEAD")
	output, err := command.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func findModuleDirectory() (string, error) {
	working, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for directory := working; ; directory = filepath.Dir(directory) {
		candidate := filepath.Join(directory, "J-space", "go.mod")
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Dir(candidate), nil
		}
		if filepath.Dir(directory) == directory {
			break
		}
	}
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Clean(filepath.Join(filepath.Dir(executable), ".."))
		if _, err := os.Stat(filepath.Join(candidate, "probe", "probe.py")); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("could not locate J-Space module directory")
}

func newRunID(now time.Time) string {
	random := make([]byte, 4)
	_, _ = rand.Read(random)
	return now.Format("20060102T150405Z") + "-" + hex.EncodeToString(random)
}

func safeFailure(err error) string {
	message := strings.TrimSpace(err.Error())
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
