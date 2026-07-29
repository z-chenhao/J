package packages

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/z-chenhao/J/J-agent/agent"
	jmcp "github.com/z-chenhao/J/J-mcp"
)

const (
	mcpShutdownTimeout       = 5 * time.Second
	maxMCPStartupStderrBytes = 16 << 10
)

// HostConfig selects one installed-package registry and environment resolver.
type HostConfig struct {
	RegistryPath string
	LookupEnv    func(string) (string, bool)
}

// ToolInfo describes one advertised package tool and selection state.
type ToolInfo struct {
	Package  string
	Server   string
	Name     string
	Selected bool
}

// Session is one immutable construction-time package composition.
type Session struct {
	tools       []agent.Tool
	skillRoots  []string
	toolInfo    []ToolInfo
	connections []*jmcp.Connection
}

// Open starts every installed package MCP contribution and freezes its Tools.
// A missing registry returns an empty session.
func Open(ctx context.Context, config HostConfig) (*Session, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if strings.TrimSpace(config.RegistryPath) == "" {
		var err error
		config.RegistryPath, err = DefaultRegistryPath()
		if err != nil {
			return nil, err
		}
	}
	if config.LookupEnv == nil {
		config.LookupEnv = os.LookupEnv
	}
	installed, err := Installed(config.RegistryPath)
	if err != nil {
		return nil, err
	}
	session := &Session{}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = session.Close()
		}
	}()
	for _, pkg := range installed {
		for _, relative := range pkg.Manifest.Contributes.Skills {
			root, err := resolvedPathWithin(pkg.Root, relative)
			if err != nil {
				return nil, fmt.Errorf("package %q skill root: %w", pkg.Manifest.ID, err)
			}
			session.skillRoots = append(session.skillRoots, root)
		}
		contributions := append([]MCPContribution(nil), pkg.Manifest.Contributes.MCP...)
		sort.Slice(contributions, func(left, right int) bool {
			return contributions[left].ID < contributions[right].ID
		})
		for _, contribution := range contributions {
			connection, err := openContribution(ctx, config.LookupEnv, pkg, contribution)
			if err != nil {
				return nil, err
			}
			session.connections = append(session.connections, connection)
			tools, err := connection.Tools(ctx)
			if err != nil {
				return nil, fmt.Errorf(
					"package %q MCP %q list tools: %w",
					pkg.Manifest.ID,
					contribution.ID,
					err,
				)
			}
			selected, info, err := selectTools(pkg.Manifest.ID, contribution, tools)
			if err != nil {
				return nil, err
			}
			session.tools = append(session.tools, selected...)
			session.toolInfo = append(session.toolInfo, info...)
		}
	}
	succeeded = true
	return session, nil
}

// Tools returns a defensive copy of the package Tools.
func (session *Session) Tools() []agent.Tool {
	if session == nil {
		return nil
	}
	return append([]agent.Tool(nil), session.tools...)
}

// SkillRoots returns validated absolute Agent Skills roots.
func (session *Session) SkillRoots() []string {
	if session == nil {
		return nil
	}
	return append([]string(nil), session.skillRoots...)
}

// ToolInfo returns deterministic package Tool discovery facts.
func (session *Session) ToolInfo() []ToolInfo {
	if session == nil {
		return nil
	}
	return append([]ToolInfo(nil), session.toolInfo...)
}

// Close closes package MCP sessions in reverse construction order.
func (session *Session) Close() error {
	if session == nil {
		return nil
	}
	var closeErr error
	for index := len(session.connections) - 1; index >= 0; index-- {
		closeErr = errors.Join(closeErr, session.connections[index].Close())
	}
	session.connections = nil
	return closeErr
}

// SkillRoots reads only package manifests without starting executable code.
func SkillRoots(registryPath string) ([]string, error) {
	if strings.TrimSpace(registryPath) == "" {
		var err error
		registryPath, err = DefaultRegistryPath()
		if err != nil {
			return nil, err
		}
	}
	installed, err := Installed(registryPath)
	if err != nil {
		return nil, err
	}
	var roots []string
	for _, pkg := range installed {
		for _, relative := range pkg.Manifest.Contributes.Skills {
			root, err := resolvedPathWithin(pkg.Root, relative)
			if err != nil {
				return nil, fmt.Errorf("package %q skill root: %w", pkg.Manifest.ID, err)
			}
			roots = append(roots, root)
		}
	}
	return roots, nil
}

func openContribution(
	ctx context.Context,
	lookup func(string) (string, bool),
	pkg Package,
	contribution MCPContribution,
) (*jmcp.Connection, error) {
	command := contribution.Command
	if hasPathSeparator(command) {
		var err error
		command, err = resolvedPathWithin(pkg.Root, command)
		if err != nil {
			return nil, fmt.Errorf(
				"package %q MCP %q command: %w",
				pkg.Manifest.ID,
				contribution.ID,
				err,
			)
		}
	}
	directory := pkg.Root
	if contribution.CWD != "" {
		var err error
		directory, err = resolvedPathWithin(pkg.Root, contribution.CWD)
		if err != nil {
			return nil, fmt.Errorf(
				"package %q MCP %q cwd: %w",
				pkg.Manifest.ID,
				contribution.ID,
				err,
			)
		}
	}
	environment, err := processEnvironment(contribution.Env, lookup)
	if err != nil {
		return nil, fmt.Errorf(
			"package %q MCP %q environment: %w",
			pkg.Manifest.ID,
			contribution.ID,
			err,
		)
	}
	if !hasPathSeparator(command) {
		command, err = lookPath(command, environment)
		if err != nil {
			return nil, fmt.Errorf(
				"package %q MCP %q command: %w",
				pkg.Manifest.ID,
				contribution.ID,
				err,
			)
		}
	}
	stderr := newStartupStderr()
	connection, err := jmcp.DialStdio(ctx, jmcp.StdioConfig{
		Command:         command,
		Args:            append([]string(nil), contribution.Args...),
		Dir:             directory,
		Env:             environment,
		Stderr:          stderr,
		ShutdownTimeout: mcpShutdownTimeout,
	})
	details := stderr.stop()
	if err != nil {
		if details != "" {
			err = fmt.Errorf("%w\nMCP server stderr:\n%s", err, details)
		}
		return nil, fmt.Errorf(
			"package %q MCP %q: %w",
			pkg.Manifest.ID,
			contribution.ID,
			err,
		)
	}
	return connection, nil
}

func lookPath(command string, environment []string) (string, error) {
	var pathValue string
	for _, value := range environment {
		if strings.HasPrefix(value, "PATH=") {
			pathValue = strings.TrimPrefix(value, "PATH=")
			break
		}
	}
	for _, directory := range filepath.SplitList(pathValue) {
		if directory == "" {
			directory = "."
		}
		candidate := filepath.Join(directory, command)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return "", err
		}
		return absolute, nil
	}
	return "", fmt.Errorf("executable %q was not found in forwarded PATH", command)
}

func selectTools(
	packageID string,
	contribution MCPContribution,
	tools []agent.Tool,
) ([]agent.Tool, []ToolInfo, error) {
	available := make(map[string]agent.Tool, len(tools))
	for _, tool := range tools {
		if tool == nil {
			return nil, nil, fmt.Errorf(
				"package %q MCP %q returned a nil tool",
				packageID,
				contribution.ID,
			)
		}
		available[tool.Spec().Name] = tool
	}
	selectedNames := make(map[string]struct{}, len(contribution.Tools))
	if contribution.Tools == nil {
		for name := range available {
			selectedNames[name] = struct{}{}
		}
	} else {
		var missing []string
		for _, name := range contribution.Tools {
			if _, exists := available[name]; !exists {
				missing = append(missing, name)
			} else {
				selectedNames[name] = struct{}{}
			}
		}
		if len(missing) > 0 {
			availableNames := make([]string, 0, len(available))
			for name := range available {
				availableNames = append(availableNames, name)
			}
			sort.Strings(missing)
			sort.Strings(availableNames)
			return nil, nil, fmt.Errorf(
				"package %q MCP %q configured unknown tools %q; available tools: %q",
				packageID,
				contribution.ID,
				missing,
				availableNames,
			)
		}
	}
	selected := make([]agent.Tool, 0, len(selectedNames))
	info := make([]ToolInfo, 0, len(tools))
	for _, tool := range tools {
		name := tool.Spec().Name
		_, enabled := selectedNames[name]
		info = append(info, ToolInfo{
			Package:  packageID,
			Server:   contribution.ID,
			Name:     name,
			Selected: enabled,
		})
		if enabled {
			selected = append(selected, tool)
		}
	}
	return selected, info, nil
}

func processEnvironment(
	requested []string,
	lookup func(string) (string, bool),
) ([]string, error) {
	const (
		pathVariable = "PATH"
		homeVariable = "HOME"
	)
	baseline := []string{pathVariable, homeVariable, "TMPDIR", "LANG", "LC_ALL"}
	requestedSet := make(map[string]struct{}, len(requested))
	for _, name := range requested {
		requestedSet[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(baseline)+len(requested))
	environment := make([]string, 0, len(baseline)+len(requested))
	for _, name := range append(baseline, requested...) {
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		value, exists := lookup(name)
		if !exists {
			if name == pathVariable || name == homeVariable {
				return nil, fmt.Errorf("required environment variable %s is not set", name)
			}
			if _, required := requestedSet[name]; required {
				return nil, fmt.Errorf("forwarded environment variable %s is not set", name)
			}
			continue
		}
		environment = append(environment, name+"="+value)
	}
	return environment, nil
}

type startupStderr struct {
	mu        sync.Mutex
	content   []byte
	active    bool
	truncated bool
}

func newStartupStderr() *startupStderr {
	return &startupStderr{active: true}
}

func (capture *startupStderr) Write(content []byte) (int, error) {
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

func (capture *startupStderr) stop() string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.active = false
	details := strings.TrimSpace(string(capture.content))
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

var _ io.Writer = (*startupStderr)(nil)
