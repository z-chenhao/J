package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
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
	mcpTools    []mcpToolObservation
}

type mcpToolObservation struct {
	Server   string
	Name     string
	Selected bool
}

func composeRuntime(ctx context.Context, cfg config) (*runtimeComposition, error) {
	composition := &runtimeComposition{}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = composition.Close()
		}
	}()

	names := make(map[string]string)
	if !cfg.listTools {
		shell, err := bashtool.New(".")
		if err != nil {
			return nil, fmt.Errorf("initialize bash tool: %w", err)
		}
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
	}

	if cfg.extensions != nil && cfg.extensions.MCP != nil {
		serverNames := sortedServerNames(cfg.extensions.MCP.Servers)
		for _, serverName := range serverNames {
			server := cfg.extensions.MCP.Servers[serverName]
			connection, err := connectMCPServer(ctx, cfg.configPath, server)
			if err != nil {
				return nil, fmt.Errorf("start MCP server %q: %w", serverName, err)
			}
			composition.connections = append(composition.connections, connection)
			tools, err := connection.Tools(ctx)
			if err != nil {
				return nil, fmt.Errorf("load MCP server %q tools: %w", serverName, err)
			}
			selected, observations, err := selectMCPTools(serverName, server.Tools, tools)
			if err != nil {
				return nil, err
			}
			composition.mcpTools = append(composition.mcpTools, observations...)
			if err := composition.addTools(
				names,
				"MCP server "+serverName,
				selected...,
			); err != nil {
				return nil, err
			}
		}
	}

	if !cfg.listTools && cfg.session != "" {
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
			if err := store.Save(ctx, cfg.session, nil); err != nil {
				return nil, fmt.Errorf("create session %q: %w", cfg.session, err)
			}
		default:
			return nil, fmt.Errorf("restore session %q: %w", cfg.session, err)
		}
	}

	succeeded = true
	return composition, nil
}

func connectMCPServer(
	ctx context.Context,
	configPath string,
	server settings.MCPServer,
) (*jmcp.Connection, error) {
	if server.Command != "" {
		environment, err := mcpEnvironment(server.Env)
		if err != nil {
			return nil, err
		}
		return jmcp.DialStdio(ctx, jmcp.StdioConfig{
			Command:         server.Command,
			Args:            append([]string(nil), server.Args...),
			Dir:             resolveOptionalPath(configPath, server.CWD),
			Env:             environment,
			ShutdownTimeout: mcpShutdownTimeout,
		})
	}

	var client *http.Client
	if len(server.Headers) > 0 {
		headers, err := resolveHTTPHeaders(server.Headers)
		if err != nil {
			return nil, err
		}
		client = &http.Client{
			Transport: headerTransport{
				base:    http.DefaultTransport,
				headers: headers,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return jmcp.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           client,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	})
}

func resolveHTTPHeaders(configured map[string]string) (http.Header, error) {
	headers := make(http.Header, len(configured))
	for name, value := range configured {
		resolved, err := settings.ResolveValue(value, os.LookupEnv)
		if err != nil {
			return nil, fmt.Errorf("resolve HTTP header %q: %w", name, err)
		}
		if !validHTTPHeaderValue(resolved) {
			return nil, fmt.Errorf("HTTP header %q contains an invalid value", name)
		}
		headers.Set(name, resolved)
	}
	return headers, nil
}

func validHTTPHeaderValue(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '\t' || character >= ' ' && character != '\u007f' {
			continue
		}
		return false
	}
	return true
}

type headerTransport struct {
	base    http.RoundTripper
	headers http.Header
}

func (transport headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	for name, values := range transport.headers {
		copy.Header[name] = append([]string(nil), values...)
	}
	return transport.base.RoundTrip(copy)
}

func selectMCPTools(
	serverName string,
	allowlist []string,
	tools []agent.Tool,
) ([]agent.Tool, []mcpToolObservation, error) {
	available := make(map[string]agent.Tool, len(tools))
	for _, tool := range tools {
		if tool == nil {
			return nil, nil, fmt.Errorf("MCP server %q returned a nil tool", serverName)
		}
		name := tool.Spec().Name
		available[name] = tool
	}

	selectedNames := make(map[string]struct{}, len(allowlist))
	if allowlist == nil {
		for name := range available {
			selectedNames[name] = struct{}{}
		}
	} else {
		missing := make([]string, 0)
		for _, name := range allowlist {
			if _, exists := available[name]; !exists {
				missing = append(missing, name)
				continue
			}
			selectedNames[name] = struct{}{}
		}
		if len(missing) > 0 {
			availableNames := make([]string, 0, len(available))
			for name := range available {
				availableNames = append(availableNames, name)
			}
			sort.Strings(missing)
			sort.Strings(availableNames)
			return nil, nil, fmt.Errorf(
				"MCP server %q configured unknown tools %q; available tools: %q",
				serverName,
				missing,
				availableNames,
			)
		}
	}

	selected := make([]agent.Tool, 0, len(selectedNames))
	observations := make([]mcpToolObservation, 0, len(tools))
	for _, tool := range tools {
		name := tool.Spec().Name
		_, enabled := selectedNames[name]
		observations = append(observations, mcpToolObservation{
			Server:   serverName,
			Name:     name,
			Selected: enabled,
		})
		if enabled {
			selected = append(selected, tool)
		}
	}
	return selected, observations, nil
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
