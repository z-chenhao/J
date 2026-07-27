package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/z-chenhao/J/J-agent/agent"
	bashtool "github.com/z-chenhao/J/J-agent/tool/bash"
	jmcp "github.com/z-chenhao/J/J-mcp"
	"github.com/z-chenhao/J/J-mem/memory"
	"github.com/z-chenhao/J/J-mem/transcript"
	"github.com/z-chenhao/J/J-tui/internal/settings"
)

const mcpShutdownTimeout = 5 * time.Second

type conversationRunner interface {
	Run(context.Context, string, agent.EventHandler) (agent.RunResult, error)
}

type runtimeComposition struct {
	tools       []agent.Tool
	history     []agent.Message
	transcripts *transcript.Store
	connections []*jmcp.Connection
}

func composeRuntime(ctx context.Context, cfg config) (*runtimeComposition, error) {
	composition := &runtimeComposition{}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = composition.Close()
		}
	}()

	shell, err := bashtool.New(".")
	if err != nil {
		return nil, fmt.Errorf("initialize bash tool: %w", err)
	}
	names := make(map[string]string)
	if err := composition.addTools(names, "J-agent", shell); err != nil {
		return nil, err
	}

	if cfg.memory != nil && cfg.memory.LongTerm != nil {
		path := resolveStatePath(cfg.configPath, cfg.memory.LongTerm.Path)
		log, err := memory.Open(path)
		if err != nil {
			return nil, fmt.Errorf("initialize long-term memory: %w", err)
		}
		if err := composition.addTools(names, "J-mem", log.Tools()...); err != nil {
			return nil, err
		}
	}

	if cfg.extensions != nil && cfg.extensions.MCP != nil {
		serverNames := sortedServerNames(cfg.extensions.MCP.Servers)
		for _, serverName := range serverNames {
			server := cfg.extensions.MCP.Servers[serverName]
			environment, err := mcpEnvironment(server.Env)
			if err != nil {
				return nil, fmt.Errorf("configure MCP server %q: %w", serverName, err)
			}
			connection, err := jmcp.DialStdio(ctx, jmcp.StdioConfig{
				Command:         server.Command,
				Args:            append([]string(nil), server.Args...),
				Dir:             resolveOptionalPath(cfg.configPath, server.CWD),
				Env:             environment,
				ShutdownTimeout: mcpShutdownTimeout,
			})
			if err != nil {
				return nil, fmt.Errorf("start MCP server %q: %w", serverName, err)
			}
			composition.connections = append(composition.connections, connection)
			tools, err := connection.Tools(ctx)
			if err != nil {
				return nil, fmt.Errorf("load MCP server %q tools: %w", serverName, err)
			}
			if err := composition.addTools(names, "MCP server "+serverName, tools...); err != nil {
				return nil, err
			}
		}
	}

	if cfg.session != "" {
		if cfg.memory == nil || cfg.memory.Transcript == nil {
			return nil, errors.New("transcript session requires memory.transcript")
		}
		path := resolveStatePath(cfg.configPath, cfg.memory.Transcript.Path)
		store, err := transcript.Open(path)
		if err != nil {
			return nil, fmt.Errorf("initialize transcript memory: %w", err)
		}
		composition.transcripts = store
		history, err := store.Load(ctx, cfg.session)
		switch {
		case err == nil:
			composition.history = history
		case errors.Is(err, transcript.ErrNotFound):
		default:
			return nil, fmt.Errorf("restore session %q: %w", cfg.session, err)
		}
	}

	succeeded = true
	return composition, nil
}

func (composition *runtimeComposition) addTools(
	names map[string]string,
	owner string,
	tools ...agent.Tool,
) error {
	for _, tool := range tools {
		if tool == nil {
			return fmt.Errorf("%s returned a nil tool", owner)
		}
		name := strings.TrimSpace(tool.Spec().Name)
		if name == "" {
			return fmt.Errorf("%s returned a tool without a name", owner)
		}
		if previous, exists := names[name]; exists {
			return fmt.Errorf(
				"tool name %q from %s conflicts with %s",
				name,
				owner,
				previous,
			)
		}
		names[name] = owner
		composition.tools = append(composition.tools, tool)
	}
	return nil
}

func (composition *runtimeComposition) Close() error {
	if composition == nil {
		return nil
	}
	var closeErr error
	if composition.transcripts != nil {
		closeErr = errors.Join(closeErr, composition.transcripts.Close())
		composition.transcripts = nil
	}
	for index := len(composition.connections) - 1; index >= 0; index-- {
		closeErr = errors.Join(closeErr, composition.connections[index].Close())
	}
	composition.connections = nil
	return closeErr
}

type persistentRunner struct {
	runner    *agent.Agent
	store     *transcript.Store
	sessionID string
}

func (runner *persistentRunner) Run(
	ctx context.Context,
	prompt string,
	handler agent.EventHandler,
) (agent.RunResult, error) {
	result, err := runner.runner.Run(ctx, prompt, handler)
	if err != nil {
		return result, err
	}
	if err := runner.store.Save(ctx, runner.sessionID, runner.runner.History()); err != nil {
		return result, fmt.Errorf("persist session %q: %w", runner.sessionID, err)
	}
	return result, nil
}

func sortedServerNames(servers map[string]settings.MCPServer) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func resolveStatePath(configPath, statePath string) string {
	if filepath.IsAbs(statePath) {
		return filepath.Clean(statePath)
	}
	return filepath.Join(filepath.Dir(configPath), statePath)
}

func resolveOptionalPath(configPath, optionalPath string) string {
	if optionalPath == "" {
		return ""
	}
	return resolveStatePath(configPath, optionalPath)
}

func mcpEnvironment(requested []string) ([]string, error) {
	const (
		pathVariable = "PATH"
		homeVariable = "HOME"
	)
	baseline := []string{
		pathVariable,
		homeVariable,
		"TMPDIR",
		"LANG",
		"LC_ALL",
	}
	seen := make(map[string]struct{}, len(baseline)+len(requested))
	environment := make([]string, 0, len(baseline)+len(requested))
	for _, name := range append(baseline, requested...) {
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		value, exists := os.LookupEnv(name)
		if !exists {
			if name == pathVariable || name == homeVariable {
				return nil, fmt.Errorf("required environment variable %s is not set", name)
			}
			if contains(requested, name) {
				return nil, fmt.Errorf("forwarded environment variable %s is not set", name)
			}
			continue
		}
		environment = append(environment, name+"="+value)
	}
	return environment, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
