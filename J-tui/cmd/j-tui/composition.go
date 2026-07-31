package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/z-chenhao/J/J-agent/agent"
	bashtool "github.com/z-chenhao/J/J-agent/tool/bash"
	jmcp "github.com/z-chenhao/J/J-mcp"
	"github.com/z-chenhao/J/J-mem/memory"
	"github.com/z-chenhao/J/J-mem/transcript"
	jpackages "github.com/z-chenhao/J/J-packages"
	jskills "github.com/z-chenhao/J/J-skills"
	jsubagents "github.com/z-chenhao/J/J-subagents"
	"github.com/z-chenhao/J/J-tui/internal/observe"
	"github.com/z-chenhao/J/J-tui/internal/settings"
)

const (
	mcpShutdownTimeout       = 5 * time.Second
	mcpTLSHandshakeTimeout   = 20 * time.Second
	transcriptSaveTimeout    = 5 * time.Second
	maxMCPStartupStderrBytes = 16 << 10
)

type conversationRunner interface {
	Run(context.Context, string, observe.Handler) (agent.RunResult, error)
}

type runtimeComposition struct {
	tools       []agent.Tool
	history     []agent.Message
	transcripts *transcript.Store
	connections []*jmcp.Connection
	packages    *jpackages.Session
	mcpTools    []mcpToolObservation
	observers   []observerSpec
	subagents   *subagentEventRelay
}

type mcpToolObservation struct {
	Server   string
	Name     string
	Selected bool
}

type subagentEventRelay struct {
	mu      sync.Mutex
	handler observe.Handler
}

func (relay *subagentEventRelay) begin(handler observe.Handler) {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	relay.handler = handler
}

func (relay *subagentEventRelay) end() {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	relay.handler = nil
}

func (relay *subagentEventRelay) emit(name string, event agent.Event) {
	relay.mu.Lock()
	handler := relay.handler
	relay.mu.Unlock()
	if handler != nil {
		handler(observe.Event{Subagent: name, Runtime: event})
	}
}

type mcpStartupStderr struct {
	mu        sync.Mutex
	content   []byte
	active    bool
	truncated bool
}

func newMCPStartupStderr() *mcpStartupStderr {
	return &mcpStartupStderr{active: true}
}

func (capture *mcpStartupStderr) Write(content []byte) (int, error) {
	written := len(content)
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if !capture.active || written == 0 {
		return written, nil
	}
	if written >= maxMCPStartupStderrBytes {
		capture.content = append(
			capture.content[:0],
			content[written-maxMCPStartupStderrBytes:]...,
		)
		capture.truncated = true
		return written, nil
	}
	overflow := len(capture.content) + written - maxMCPStartupStderrBytes
	if overflow > 0 {
		copy(capture.content, capture.content[overflow:])
		capture.content = capture.content[:len(capture.content)-overflow]
		capture.truncated = true
	}
	capture.content = append(capture.content, content...)
	return written, nil
}

func (capture *mcpStartupStderr) stop() string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.active = false
	details := strings.TrimSpace(ansi.Strip(string(capture.content)))
	capture.content = nil
	details = strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' || !unicode.IsControl(character) {
			return character
		}
		return '\uFFFD'
	}, details)
	if details != "" && capture.truncated {
		details = "[stderr truncated to last 16 KiB]\n" + details
	}
	return details
}

func composeRuntime(ctx context.Context, cfg config) (*runtimeComposition, error) {
	composition := &runtimeComposition{subagents: &subagentEventRelay{}}
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

	if cfg.extensions != nil && cfg.extensions.Observers != nil {
		observers, err := composeObservers(cfg.configPath, cfg.extensions.Observers)
		if err != nil {
			return nil, err
		}
		composition.observers = observers
	}

	var packageSkillRoots []string
	if !cfg.noPackages {
		packageSession, err := jpackages.Open(ctx, jpackages.HostConfig{
			RegistryPath: cfg.packagesRegistry,
		})
		if err != nil {
			return nil, fmt.Errorf("initialize J Packages: %w", err)
		}
		composition.packages = packageSession
		packageSkillRoots = packageSession.SkillRoots()
		for _, information := range packageSession.ToolInfo() {
			composition.mcpTools = append(composition.mcpTools, mcpToolObservation{
				Server:   information.Package + "/" + information.Server,
				Name:     information.Name,
				Selected: information.Selected,
			})
		}
		if err := composition.addTools(
			names,
			"J Packages",
			packageSession.Tools()...,
		); err != nil {
			return nil, err
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

	if !cfg.listTools && (cfg.skills != nil || len(packageSkillRoots) > 0) {
		_, catalog, err := loadConfiguredSkills(
			cfg.configPath,
			cfg.skills,
			packageSkillRoots,
		)
		if err != nil {
			return nil, err
		}
		skillTool, err := catalog.Tool()
		if err != nil {
			return nil, fmt.Errorf("initialize skills tool: %w", err)
		}
		if err := composition.addTools(names, "J-skills", skillTool); err != nil {
			return nil, err
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

	if !cfg.listTools && cfg.subagents != nil {
		definitions, err := buildSubagentDefinitions(
			cfg,
			composition.tools,
			composition.subagents,
		)
		if err != nil {
			return nil, err
		}
		var subagentTool agent.Tool
		if composition.transcripts != nil {
			subagentTool, err = jsubagents.NewSessionTool(
				jsubagents.SessionConfig{
					ParentID: cfg.session,
					Store:    composition.transcripts,
				},
				definitions...,
			)
		} else {
			subagentTool, err = jsubagents.NewTool(definitions...)
		}
		if err != nil {
			return nil, fmt.Errorf("initialize subagents: %w", err)
		}
		if err := composition.addTools(
			names,
			"J-subagents",
			subagentTool,
		); err != nil {
			return nil, err
		}
	}

	succeeded = true
	return composition, nil
}

func loadConfiguredSkills(
	configPath string,
	configured *settings.Skills,
	packageRoots []string,
) (*jskills.Catalog, *jskills.Catalog, error) {
	if configured == nil && len(packageRoots) == 0 {
		return nil, nil, errors.New("skills configuration is required")
	}
	paths := append([]string(nil), packageRoots...)
	if configured != nil {
		for _, configuredPath := range configured.Paths {
			resolved, err := settings.ResolveValue(configuredPath, os.LookupEnv)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"resolve skill path %q: %w",
					configuredPath,
					err,
				)
			}
			paths = append(paths, resolveStatePath(configPath, resolved))
		}
	}
	catalog, err := jskills.Load(paths...)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize skills: %w", err)
	}
	selected := catalog
	if configured != nil && configured.Include != nil {
		selected, err = catalog.Select(configured.Include...)
		if err != nil {
			return nil, nil, fmt.Errorf("select skills: %w", err)
		}
	}
	return catalog, selected, nil
}

func packageSkillRoots(cfg config) ([]string, error) {
	if cfg.noPackages {
		return nil, nil
	}
	roots, err := jpackages.SkillRoots(cfg.packagesRegistry)
	if err != nil {
		return nil, fmt.Errorf("load J Package skills: %w", err)
	}
	return roots, nil
}

func connectMCPServer(
	ctx context.Context,
	configPath string,
	server settings.MCPServer,
) (*jmcp.Connection, error) {
	if server.Command != "" {
		environment, err := processEnvironment(server.Env)
		if err != nil {
			return nil, err
		}
		stderr := newMCPStartupStderr()
		connection, err := jmcp.DialStdio(ctx, jmcp.StdioConfig{
			Command:         server.Command,
			Args:            append([]string(nil), server.Args...),
			Dir:             resolveOptionalPath(configPath, server.CWD),
			Env:             environment,
			Stderr:          stderr,
			ShutdownTimeout: mcpShutdownTimeout,
		})
		details := stderr.stop()
		if err != nil && details != "" {
			return nil, fmt.Errorf("%w\nMCP server stderr:\n%s", err, details)
		}
		return connection, err
	}

	var headers http.Header
	if len(server.Headers) > 0 {
		var err error
		headers, err = resolveHTTPHeaders(server.Headers)
		if err != nil {
			return nil, err
		}
	}
	return jmcp.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           newMCPHTTPClient(headers),
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	})
}

func newMCPHTTPClient(headers http.Header) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSHandshakeTimeout = mcpTLSHandshakeTimeout
	var roundTripper http.RoundTripper = transport
	client := &http.Client{Transport: roundTripper}
	if len(headers) > 0 {
		client.Transport = headerTransport{
			base:    transport,
			headers: headers,
		}
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return client
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

func buildSubagentDefinitions(
	cfg config,
	availableTools []agent.Tool,
	events *subagentEventRelay,
) ([]jsubagents.Definition, error) {
	names := make([]string, 0, len(cfg.subagents.Agents))
	for name := range cfg.subagents.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	definitions := make([]jsubagents.Definition, 0, len(names))
	for _, name := range names {
		configured := cfg.subagents.Agents[name]
		var (
			model agent.Model
			err   error
		)
		if configured.Profile == "" {
			model, err = buildModel(cfg)
		} else {
			profile, exists := cfg.profiles[configured.Profile]
			if !exists {
				return nil, fmt.Errorf(
					"subagent %q profile %q is not defined",
					name,
					configured.Profile,
				)
			}
			model, err = buildProfileModel(configured.Profile, profile)
		}
		if err != nil {
			return nil, fmt.Errorf("initialize subagent %q model: %w", name, err)
		}
		tools, err := selectSubagentTools(name, configured.Tools, availableTools)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, jsubagents.Definition{
			Name:         name,
			Description:  configured.Description,
			Model:        model,
			SystemPrompt: configured.SystemPrompt,
			Tools:        tools,
			EventHandler: func(event agent.Event) {
				events.emit(name, event)
			},
		})
	}
	return definitions, nil
}

func selectSubagentTools(
	subagentName string,
	allowlist []string,
	availableTools []agent.Tool,
) ([]agent.Tool, error) {
	if allowlist == nil {
		return append([]agent.Tool(nil), availableTools...), nil
	}
	available := make(map[string]agent.Tool, len(availableTools))
	availableNames := make([]string, 0, len(availableTools))
	for _, tool := range availableTools {
		name := tool.Spec().Name
		available[name] = tool
		availableNames = append(availableNames, name)
	}
	selected := make([]agent.Tool, 0, len(allowlist))
	missing := make([]string, 0)
	for _, name := range allowlist {
		tool, exists := available[name]
		if !exists {
			missing = append(missing, name)
			continue
		}
		selected = append(selected, tool)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		sort.Strings(availableNames)
		return nil, fmt.Errorf(
			"subagent %q configured unknown tools %q; available tools: %q",
			subagentName,
			missing,
			availableNames,
		)
	}
	return selected, nil
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
	closeErr = errors.Join(closeErr, composition.packages.Close())
	composition.packages = nil
	return closeErr
}

type persistentRunner struct {
	runner    conversationRunner
	history   func() []agent.Message
	store     *transcript.Store
	sessionID string
}

func (runner *persistentRunner) Run(
	ctx context.Context,
	prompt string,
	handler observe.Handler,
) (agent.RunResult, error) {
	if ctx == nil {
		return agent.RunResult{}, errors.New("context is required")
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	var checkpointErr error
	result, runErr := runner.runner.Run(runContext, prompt, func(event observe.Event) {
		if checkpointErr == nil && event.Subagent == "" &&
			(event.Runtime.Type == agent.EventAgentStarted ||
				event.Runtime.Type == agent.EventTurnCompleted) {
			saveContext, stop := context.WithTimeout(
				context.WithoutCancel(ctx),
				transcriptSaveTimeout,
			)
			checkpointErr = runner.store.Save(
				saveContext,
				runner.sessionID,
				runner.history(),
			)
			stop()
			if checkpointErr != nil {
				cancel()
			}
		}
		if handler != nil {
			handler(event)
		}
	})
	if checkpointErr != nil {
		return result, fmt.Errorf(
			"persist session %q checkpoint: %w",
			runner.sessionID,
			checkpointErr,
		)
	}
	return result, runErr
}

type observedRunner struct {
	runner *agent.Agent
	relay  *subagentEventRelay
}

func newObservedRunner(
	runner *agent.Agent,
	relay *subagentEventRelay,
) *observedRunner {
	if relay == nil {
		relay = &subagentEventRelay{}
	}
	return &observedRunner{runner: runner, relay: relay}
}

func (runner *observedRunner) Run(
	ctx context.Context,
	prompt string,
	handler observe.Handler,
) (agent.RunResult, error) {
	runner.relay.begin(handler)
	defer runner.relay.end()
	return runner.runner.Run(ctx, prompt, func(event agent.Event) {
		if handler != nil {
			handler(observe.Event{Runtime: event})
		}
	})
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

func processEnvironment(requested []string) ([]string, error) {
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

func composeObservers(
	configPath string,
	configured map[string]settings.Observer,
) ([]observerSpec, error) {
	names := make([]string, 0, len(configured))
	for name := range configured {
		names = append(names, name)
	}
	sort.Strings(names)

	specs := make([]observerSpec, 0, len(names))
	for _, name := range names {
		observer := configured[name]
		environment, err := processEnvironment(observer.Env)
		if err != nil {
			return nil, fmt.Errorf("observer %q environment: %w", name, err)
		}
		command := observer.Command
		if !filepath.IsAbs(command) && strings.ContainsAny(command, `/\`) {
			command = resolveStatePath(configPath, command)
		}
		command, err = exec.LookPath(command)
		if err != nil {
			return nil, fmt.Errorf("observer %q command: %w", name, err)
		}
		directory := resolveOptionalPath(configPath, observer.CWD)
		if directory != "" {
			info, statErr := os.Stat(directory)
			if statErr != nil {
				return nil, fmt.Errorf("observer %q cwd: %w", name, statErr)
			}
			if !info.IsDir() {
				return nil, fmt.Errorf("observer %q cwd is not a directory", name)
			}
		}
		specs = append(specs, observerSpec{
			Name:        name,
			Command:     command,
			Args:        append([]string(nil), observer.Args...),
			Dir:         directory,
			Env:         environment,
			Permissions: append([]string(nil), observer.Permissions...),
		})
	}
	return specs, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
