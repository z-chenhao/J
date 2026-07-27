package jmcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/z-chenhao/J/J-agent/agent"
)

func TestConnectionProjectsAndCallsTools(t *testing.T) {
	connection, closeServer := testConnection(t, func(server *mcpsdk.Server) {
		server.AddTool(&mcpsdk.Tool{
			Name:        "echo",
			Description: " Echo text. ",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		}, func(_ context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			var input struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return nil, err
			}
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: input.Text}},
			}, nil
		})
	})
	defer closeServer()
	defer connection.Close()

	tools, err := connection.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	spec := tools[0].Spec()
	if spec.Name != "echo" || spec.Description != "Echo text." {
		t.Fatalf("unexpected tool spec: %+v", spec)
	}
	spec.InputSchema[0] = 'x'
	if tools[0].Spec().InputSchema[0] != '{' {
		t.Fatal("tool schema was not defensively copied")
	}
	output, err := tools[0].Call(context.Background(), json.RawMessage(`{"text":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if output != "hello" {
		t.Fatalf("got output %q, want hello", output)
	}

	again, err := connection.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	again[0] = nil
	if tools[0] == nil {
		t.Fatal("tool slice was not defensively copied")
	}
}

func TestConnectionPreservesMCPToolError(t *testing.T) {
	connection, closeServer := testConnection(t, func(server *mcpsdk.Server) {
		server.AddTool(&mcpsdk.Tool{
			Name:        "fail",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}, func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "try another value"}},
				IsError: true,
			}, nil
		})
	})
	defer closeServer()
	defer connection.Close()

	tools, err := connection.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	output, err := tools[0].Call(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected MCP tool error")
	}
	if output != "try another value" || !strings.Contains(err.Error(), output) {
		t.Fatalf("output %q and error %q did not preserve MCP failure", output, err)
	}
}

func TestConnectionRejectsUnsupportedContent(t *testing.T) {
	connection, closeServer := testConnection(t, func(server *mcpsdk.Server) {
		server.AddTool(&mcpsdk.Tool{
			Name:        "image",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}, func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.ImageContent{
					Data:     []byte("image"),
					MIMEType: "image/png",
				}},
			}, nil
		})
	})
	defer closeServer()
	defer connection.Close()

	tools, err := connection.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tools[0].Call(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported non-text content") {
		t.Fatalf("got error %v", err)
	}
}

func TestProjectToolRejectsInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name       string
		definition *mcpsdk.Tool
	}{
		{name: "nil", definition: nil},
		{
			name: "untrimmed name",
			definition: &mcpsdk.Tool{
				Name:        " echo ",
				InputSchema: map[string]any{"type": "object"},
			},
		},
		{
			name: "non-object schema",
			definition: &mcpsdk.Tool{
				Name:        "echo",
				InputSchema: []any{"not", "an", "object"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := projectTool(nil, test.definition); err == nil {
				t.Fatal("expected invalid tool definition error")
			}
		})
	}
}

func TestTextResultBoundsOutput(t *testing.T) {
	_, err := textResult("large", &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: strings.Repeat("x", maxToolOutputBytes+1)},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("got error %v", err)
	}
}

func TestMCPToolHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	connection, closeServer := testConnection(t, func(server *mcpsdk.Server) {
		server.AddTool(&mcpsdk.Tool{
			Name:        "wait",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}, func(ctx context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
	})
	defer closeServer()
	defer connection.Close()

	tools, err := connection.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := tools[0].Call(ctx, json.RawMessage(`{}`))
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got error %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MCP call did not stop after cancellation")
	}
}

func TestDialStdio(t *testing.T) {
	if os.Getenv("J_MCP_TEST_SERVER") == "1" {
		runTestServer()
		os.Exit(0)
	}

	connection, err := DialStdio(context.Background(), StdioConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestDialStdio$"},
		Env:     append(os.Environ(), "J_MCP_TEST_SERVER=1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := connection.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Spec().Name != "stdio_echo" {
		t.Fatalf("unexpected stdio tools: %+v", tools)
	}
	output, err := tools[0].Call(context.Background(), json.RawMessage(`{"text":"connected"}`))
	if err != nil {
		t.Fatal(err)
	}
	if output != "connected" {
		t.Fatalf("got output %q, want connected", output)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

func TestDialStdioValidatesConfiguration(t *testing.T) {
	if _, err := DialStdio(context.Background(), StdioConfig{}); err == nil {
		t.Fatal("expected missing command error")
	}
	if _, err := DialStdio(nil, StdioConfig{Command: "server"}); err == nil {
		t.Fatal("expected missing context error")
	}
	if _, err := Connect(context.Background(), nil); err == nil {
		t.Fatal("expected missing transport error")
	}
}

func testConnection(t *testing.T, addTools func(*mcpsdk.Server)) (*Connection, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "j-mcp-test",
		Version: "0.1.0",
	}, nil)
	addTools(server)
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	connection, err := Connect(ctx, clientTransport)
	if err != nil {
		_ = serverSession.Close()
		cancel()
		t.Fatal(err)
	}
	return connection, func() {
		_ = serverSession.Close()
		cancel()
	}
}

func TestProjectedToolRunsThroughAgent(t *testing.T) {
	connection, closeServer := testConnection(t, func(server *mcpsdk.Server) {
		server.AddTool(&mcpsdk.Tool{
			Name:        "echo",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		}, func(_ context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			var input struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return nil, err
			}
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: input.Text}},
			}, nil
		})
	})
	defer closeServer()
	defer connection.Close()

	tools, err := connection.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	model := &toolCallingModel{}
	runner, err := agent.New(model, agent.WithTools(tools...))
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), "use echo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.Text() != "MCP said: hello" {
		t.Fatalf("got final message %q", result.Message.Text())
	}
}

type toolCallingModel struct {
	turn int
}

func (model *toolCallingModel) Complete(
	_ context.Context,
	request agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	model.turn++
	switch model.turn {
	case 1:
		return agent.ModelResponse{
			Message: agent.Message{
				Role: agent.RoleAssistant,
				Content: []agent.Content{{
					Type: agent.ContentToolCall,
					ToolCall: &agent.ToolCall{
						ID:        "call-1",
						Name:      "echo",
						Arguments: json.RawMessage(`{"text":"hello"}`),
					},
				}},
			},
			Provider:   "test",
			Model:      "test",
			StopReason: agent.StopReasonToolCalls,
		}, nil
	case 2:
		last := request.Messages[len(request.Messages)-1]
		if last.Role != agent.RoleTool || last.Text() != "hello" {
			return agent.ModelResponse{}, errors.New("MCP tool result was not continued to the model")
		}
		return agent.ModelResponse{
			Message:    agent.TextMessage(agent.RoleAssistant, "MCP said: "+last.Text()),
			Provider:   "test",
			Model:      "test",
			StopReason: agent.StopReasonStop,
		}, nil
	default:
		return agent.ModelResponse{}, errors.New("unexpected model turn")
	}
}

func runTestServer() {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "j-mcp-stdio-test",
		Version: "0.1.0",
	}, nil)
	server.AddTool(&mcpsdk.Tool{
		Name:        "stdio_echo",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	}, func(_ context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var input struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
			return nil, err
		}
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: input.Text}},
		}, nil
	})
	if err := server.Run(context.Background(), &mcpsdk.StdioTransport{}); err != nil {
		os.Exit(2)
	}
}
