// Package jmcp projects tools from one MCP server into ordinary J-agent Tools.
//
// J-mcp owns the MCP client connection and protocol conversion. It does not
// construct an Agent, read product configuration, render UI, or define a
// general extension interface.
package jmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/z-chenhao/J/J-agent/agent"
)

const (
	implementationVersion = "0.1.0"
	maxToolOutputBytes    = 1 << 20
)

// StdioConfig describes one explicit MCP server process.
//
// A nil Env inherits the current process environment, matching os/exec. A
// non-nil Env is passed exactly as supplied. Stderr is never mixed with MCP
// protocol stdout.
type StdioConfig struct {
	Command         string
	Args            []string
	Dir             string
	Env             []string
	Stderr          io.Writer
	ShutdownTimeout time.Duration
}

// Connection is one initialized MCP client session. Its first successful Tools
// call freezes the projected tool set for the lifetime of the connection.
type Connection struct {
	session *mcpsdk.ClientSession

	toolsMu    sync.Mutex
	tools      []agent.Tool
	toolsReady bool

	closeOnce sync.Once
	closeErr  error
}

// DialStdio starts and initializes one configured MCP server.
func DialStdio(ctx context.Context, config StdioConfig) (*Connection, error) {
	if ctx == nil {
		return nil, errors.New("MCP context is required")
	}
	command := strings.TrimSpace(config.Command)
	if command == "" {
		return nil, errors.New("MCP command is required")
	}
	cmd := exec.Command(command, append([]string(nil), config.Args...)...)
	cmd.Dir = config.Dir
	cmd.Stderr = config.Stderr
	if config.Env != nil {
		cmd.Env = append([]string(nil), config.Env...)
	}
	transport := &mcpsdk.CommandTransport{
		Command:           cmd,
		TerminateDuration: config.ShutdownTimeout,
	}
	connection, err := Connect(ctx, transport)
	if err != nil {
		return nil, fmt.Errorf("initialize MCP server %q: %w", command, err)
	}
	return connection, nil
}

// Connect initializes one MCP session over a caller-supplied official SDK
// transport. DialStdio is the default recipe; transport injection keeps J-mcp
// usable by applications with an already chosen MCP transport without
// introducing a J-specific Transport interface.
func Connect(ctx context.Context, transport mcpsdk.Transport) (*Connection, error) {
	if ctx == nil {
		return nil, errors.New("MCP context is required")
	}
	if transport == nil {
		return nil, errors.New("MCP transport is required")
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "j-mcp",
		Title:   "J MCP bridge",
		Version: implementationVersion,
	}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	return &Connection{session: session}, nil
}

// Tools lists, validates, and freezes all tools exposed by the connected MCP
// server. Returned slices are defensive copies.
func (connection *Connection) Tools(ctx context.Context) ([]agent.Tool, error) {
	if connection == nil || connection.session == nil {
		return nil, errors.New("MCP connection is required")
	}
	if ctx == nil {
		return nil, errors.New("MCP context is required")
	}

	connection.toolsMu.Lock()
	defer connection.toolsMu.Unlock()
	if connection.toolsReady {
		return append([]agent.Tool(nil), connection.tools...), nil
	}

	var (
		cursor string
		tools  []agent.Tool
		names  = make(map[string]struct{})
	)
	for {
		result, err := connection.session.ListTools(ctx, &mcpsdk.ListToolsParams{
			Cursor: cursor,
		})
		if err != nil {
			return nil, fmt.Errorf("list MCP tools: %w", err)
		}
		for _, definition := range result.Tools {
			projected, err := projectTool(connection.session, definition)
			if err != nil {
				return nil, err
			}
			name := projected.spec.Name
			if _, exists := names[name]; exists {
				return nil, fmt.Errorf("MCP server returned duplicate tool name %q", name)
			}
			names[name] = struct{}{}
			tools = append(tools, projected)
		}
		if result.NextCursor == "" {
			break
		}
		if result.NextCursor == cursor {
			return nil, errors.New("MCP tools pagination returned the same cursor")
		}
		cursor = result.NextCursor
	}

	connection.tools = tools
	connection.toolsReady = true
	return append([]agent.Tool(nil), tools...), nil
}

// Close closes the MCP session and its underlying server process. It is
// idempotent.
func (connection *Connection) Close() error {
	if connection == nil || connection.session == nil {
		return nil
	}
	connection.closeOnce.Do(func() {
		connection.closeErr = connection.session.Close()
	})
	return connection.closeErr
}

type projectedTool struct {
	session *mcpsdk.ClientSession
	spec    agent.ToolSpec
}

func projectTool(session *mcpsdk.ClientSession, definition *mcpsdk.Tool) (*projectedTool, error) {
	if definition == nil {
		return nil, errors.New("MCP server returned a nil tool")
	}
	name := strings.TrimSpace(definition.Name)
	if name == "" {
		return nil, errors.New("MCP server returned a tool without a name")
	}
	if name != definition.Name {
		return nil, fmt.Errorf("MCP tool name %q must be trimmed", definition.Name)
	}
	schema, err := json.Marshal(definition.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("encode MCP tool %q input schema: %w", name, err)
	}
	if !json.Valid(schema) {
		return nil, fmt.Errorf("MCP tool %q has an invalid input schema", name)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(schema, &object); err != nil || object == nil {
		return nil, fmt.Errorf("MCP tool %q input schema must be a JSON object", name)
	}
	return &projectedTool{
		session: session,
		spec: agent.ToolSpec{
			Name:        name,
			Description: strings.TrimSpace(definition.Description),
			InputSchema: append(json.RawMessage(nil), schema...),
		},
	}, nil
}

func (tool *projectedTool) Spec() agent.ToolSpec {
	spec := tool.spec
	spec.InputSchema = append(json.RawMessage(nil), tool.spec.InputSchema...)
	return spec
}

func (tool *projectedTool) Call(ctx context.Context, arguments json.RawMessage) (string, error) {
	if ctx == nil {
		return "", errors.New("MCP tool context is required")
	}
	var input map[string]any
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	if err := decoder.Decode(&input); err != nil {
		return "", fmt.Errorf("decode MCP tool %q arguments: %w", tool.spec.Name, err)
	}
	if input == nil {
		return "", fmt.Errorf("MCP tool %q arguments must be a JSON object", tool.spec.Name)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return "", fmt.Errorf("decode MCP tool %q arguments: %w", tool.spec.Name, err)
	}

	result, err := tool.session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      tool.spec.Name,
		Arguments: input,
	})
	if err != nil {
		return "", fmt.Errorf("call MCP tool %q: %w", tool.spec.Name, err)
	}
	output, err := textResult(tool.spec.Name, result)
	if err != nil {
		return "", err
	}
	if result.IsError {
		if output == "" {
			return "", fmt.Errorf("MCP tool %q returned an error", tool.spec.Name)
		}
		return output, fmt.Errorf("MCP tool %q returned an error: %s", tool.spec.Name, output)
	}
	return output, nil
}

func textResult(name string, result *mcpsdk.CallToolResult) (string, error) {
	if result == nil {
		return "", fmt.Errorf("MCP tool %q returned no result", name)
	}
	parts := make([]string, 0, len(result.Content))
	size := 0
	for _, content := range result.Content {
		text, ok := content.(*mcpsdk.TextContent)
		if !ok {
			return "", fmt.Errorf("MCP tool %q returned unsupported non-text content", name)
		}
		size += len(text.Text)
		if len(parts) > 0 {
			size++
		}
		if size > maxToolOutputBytes {
			return "", fmt.Errorf(
				"MCP tool %q text output exceeds %d bytes",
				name,
				maxToolOutputBytes,
			)
		}
		parts = append(parts, text.Text)
	}
	if len(parts) == 0 && result.StructuredContent != nil {
		return "", fmt.Errorf("MCP tool %q returned unsupported structured-only content", name)
	}
	return strings.Join(parts, "\n"), nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra json.RawMessage
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("unexpected data after the JSON object")
}
