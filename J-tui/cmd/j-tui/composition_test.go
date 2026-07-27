package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-tui/internal/settings"
)

func TestComposeRuntimeLoadsMCPAndLongTermMemoryTools(t *testing.T) {
	t.Setenv("J_TUI_MCP_TEST_SERVER", "1")
	t.Setenv("J_TUI_MCP_TOOL_NAME", "mcp_probe")
	t.Setenv("FORWARDED_VALUE", "from-parent")
	configPath := filepath.Join(t.TempDir(), ".j", "config.json")
	composition, err := composeRuntime(context.Background(), config{
		configPath: configPath,
		extensions: &settings.Extensions{MCP: &settings.MCP{
			Servers: map[string]settings.MCPServer{
				"probe": {
					Command: os.Args[0],
					Args:    []string{"-test.run=^TestMCPStdioHelper$"},
					Env: []string{
						"J_TUI_MCP_TEST_SERVER",
						"J_TUI_MCP_TOOL_NAME",
						"FORWARDED_VALUE",
					},
				},
			},
		}},
		memory: &settings.Memory{
			LongTerm: &settings.MemoryFile{Path: "state/memory.jsonl"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer composition.Close()

	names := make([]string, 0, len(composition.tools))
	var probe agent.Tool
	for _, tool := range composition.tools {
		names = append(names, tool.Spec().Name)
		if tool.Spec().Name == "mcp_probe" {
			probe = tool
		}
	}
	want := []string{
		"bash",
		"memory_retrieve",
		"memory_store",
		"memory_modify",
		"memory_forget",
		"mcp_probe",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tool names=%v, want %v", names, want)
	}
	if probe == nil {
		t.Fatal("MCP probe tool was not composed")
	}
	output, err := probe.Call(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if output != "from-parent" {
		t.Fatalf("MCP output=%q", output)
	}
	runner, err := agent.New(
		&mcpCallingModel{},
		agent.WithTools(composition.tools...),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), "use the MCP probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.Text() != "MCP said: from-parent" {
		t.Fatalf("agent result=%q", result.Message.Text())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(configPath), "state", "memory.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestComposeRuntimeRejectsToolNameCollision(t *testing.T) {
	t.Setenv("J_TUI_MCP_TEST_SERVER", "1")
	t.Setenv("J_TUI_MCP_TOOL_NAME", "bash")
	_, err := composeRuntime(context.Background(), config{
		configPath: filepath.Join(t.TempDir(), "config.json"),
		extensions: &settings.Extensions{MCP: &settings.MCP{
			Servers: map[string]settings.MCPServer{
				"collision": {
					Command: os.Args[0],
					Args:    []string{"-test.run=^TestMCPStdioHelper$"},
					Env: []string{
						"J_TUI_MCP_TEST_SERVER",
						"J_TUI_MCP_TOOL_NAME",
					},
				},
			},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), `tool name "bash"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestComposeRuntimeSelectsConfiguredMCPToolsBeforeCollisionChecks(t *testing.T) {
	t.Setenv("J_TUI_MCP_TEST_SERVER", "1")
	t.Setenv("J_TUI_MCP_TOOL_NAME", "mcp_probe")
	t.Setenv("J_TUI_MCP_EXTRA_TOOL_NAME", "bash")
	composition, err := composeRuntime(context.Background(), config{
		configPath: filepath.Join(t.TempDir(), "config.json"),
		extensions: &settings.Extensions{MCP: &settings.MCP{
			Servers: map[string]settings.MCPServer{
				"selected": {
					Command: os.Args[0],
					Args:    []string{"-test.run=^TestMCPStdioHelper$"},
					Env: []string{
						"J_TUI_MCP_TEST_SERVER",
						"J_TUI_MCP_TOOL_NAME",
						"J_TUI_MCP_EXTRA_TOOL_NAME",
					},
					Tools: []string{"mcp_probe"},
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer composition.Close()

	names := make([]string, 0, len(composition.tools))
	for _, tool := range composition.tools {
		names = append(names, tool.Spec().Name)
	}
	if strings.Join(names, ",") != "bash,mcp_probe" {
		t.Fatalf("selected tools=%v", names)
	}
	selections := make(map[string]bool, len(composition.mcpTools))
	for _, observation := range composition.mcpTools {
		selections[observation.Name] = observation.Selected
	}
	if len(selections) != 2 || !selections["mcp_probe"] || selections["bash"] {
		t.Fatalf("observations=%#v", composition.mcpTools)
	}
}

func TestComposeRuntimeRejectsUnknownConfiguredMCPTool(t *testing.T) {
	t.Setenv("J_TUI_MCP_TEST_SERVER", "1")
	t.Setenv("J_TUI_MCP_TOOL_NAME", "mcp_probe")
	_, err := composeRuntime(context.Background(), config{
		configPath: filepath.Join(t.TempDir(), "config.json"),
		extensions: &settings.Extensions{MCP: &settings.MCP{
			Servers: map[string]settings.MCPServer{
				"probe": {
					Command: os.Args[0],
					Args:    []string{"-test.run=^TestMCPStdioHelper$"},
					Env: []string{
						"J_TUI_MCP_TEST_SERVER",
						"J_TUI_MCP_TOOL_NAME",
					},
					Tools: []string{"missing"},
				},
			},
		}},
	})
	if err == nil ||
		!strings.Contains(err.Error(), `"missing"`) ||
		!strings.Contains(err.Error(), `"mcp_probe"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestComposeRuntimeLoadsStreamableHTTPMCPWithBearerToken(t *testing.T) {
	t.Setenv("REMOTE_MCP_TOKEN", "test-token")
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "j-tui-http-mcp-test",
		Version: "0.1.0",
	}, nil)
	server.AddTool(&mcpsdk.Tool{
		Name:        "remote_probe",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "remote-result"}},
		}, nil
	})
	mcpHandler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return server },
		&mcpsdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
	httpServer := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.Header.Get("Authorization") != "Bearer test-token" {
				http.Error(writer, "unauthorized", http.StatusUnauthorized)
				return
			}
			mcpHandler.ServeHTTP(writer, request)
		},
	))
	defer httpServer.Close()

	composition, err := composeRuntime(context.Background(), config{
		configPath: filepath.Join(t.TempDir(), "config.json"),
		extensions: &settings.Extensions{MCP: &settings.MCP{
			Servers: map[string]settings.MCPServer{
				"remote": {
					URL:            httpServer.URL,
					BearerTokenEnv: "REMOTE_MCP_TOKEN",
					Tools:          []string{"remote_probe"},
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer composition.Close()

	var probe agent.Tool
	for _, tool := range composition.tools {
		if tool.Spec().Name == "remote_probe" {
			probe = tool
		}
	}
	if probe == nil {
		t.Fatal("Streamable HTTP MCP tool was not composed")
	}
	output, err := probe.Call(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if output != "remote-result" {
		t.Fatalf("output=%q", output)
	}
}

func TestComposeRuntimeRequiresConfiguredHTTPBearerToken(t *testing.T) {
	if err := os.Unsetenv("MISSING_REMOTE_MCP_TOKEN"); err != nil {
		t.Fatal(err)
	}
	_, err := connectMCPServer(context.Background(), "", settings.MCPServer{
		URL:            "https://mcp.example.test/mcp",
		BearerTokenEnv: "MISSING_REMOTE_MCP_TOKEN",
	})
	if err == nil || !strings.Contains(err.Error(), "MISSING_REMOTE_MCP_TOKEN") {
		t.Fatalf("error=%v", err)
	}
}

func TestPersistentRunnerSavesAndRestoresTranscript(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".j", "config.json")
	cfg := config{
		configPath: configPath,
		session:    "project-j",
		memory: &settings.Memory{
			Transcript: &settings.MemoryFile{Path: "state/transcripts.db"},
		},
	}
	composition, err := composeRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	model := &recordingModel{}
	agentRunner, err := agent.New(
		model,
		agent.WithTools(composition.tools...),
	)
	if err != nil {
		t.Fatal(err)
	}
	runner := &persistentRunner{
		runner:    agentRunner,
		store:     composition.transcripts,
		sessionID: cfg.session,
	}
	if _, err := runner.Run(context.Background(), "remember this", nil); err != nil {
		t.Fatal(err)
	}
	if err := composition.Close(); err != nil {
		t.Fatal(err)
	}

	restored, err := composeRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if len(restored.history) != 2 ||
		restored.history[0].Text() != "remember this" ||
		restored.history[1].Text() != "response 1" {
		t.Fatalf("restored history=%#v", restored.history)
	}
	if _, err := agent.New(
		&recordingModel{},
		agent.WithTools(restored.tools...),
		agent.WithHistory(restored.history...),
	); err != nil {
		t.Fatalf("restored Agent failed: %v", err)
	}
}

func TestComposeRuntimeCreatesEmptyTranscriptSessionImmediately(t *testing.T) {
	cfg := config{
		configPath: filepath.Join(t.TempDir(), ".j", "config.json"),
		memory: &settings.Memory{
			Transcript: &settings.MemoryFile{Path: "state/transcripts.db"},
		},
	}
	if err := ensureSession(&cfg); err != nil {
		t.Fatal(err)
	}
	composition, err := composeRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer composition.Close()

	history, err := composition.transcripts.Load(context.Background(), cfg.session)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("new session history=%#v", history)
	}
}

func TestMCPEnvironmentRequiresForwardedValues(t *testing.T) {
	t.Setenv("MISSING_MCP_VALUE", "")
	if err := os.Unsetenv("MISSING_MCP_VALUE"); err != nil {
		t.Fatal(err)
	}
	if _, err := mcpEnvironment([]string{"MISSING_MCP_VALUE"}); err == nil {
		t.Fatal("missing forwarded environment variable was accepted")
	}
}

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("J_TUI_MCP_TEST_SERVER") != "1" {
		return
	}
	name := os.Getenv("J_TUI_MCP_TOOL_NAME")
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "j-tui-mcp-test",
		Version: "0.1.0",
	}, nil)
	addMCPTestTool(server, name)
	if extra := os.Getenv("J_TUI_MCP_EXTRA_TOOL_NAME"); extra != "" {
		addMCPTestTool(server, extra)
	}
	if err := server.Run(context.Background(), &mcpsdk.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func addMCPTestTool(server *mcpsdk.Server, name string) {
	server.AddTool(&mcpsdk.Tool{
		Name:        name,
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: os.Getenv("FORWARDED_VALUE")},
			},
		}, nil
	})
}

type mcpCallingModel struct {
	turn int
}

func (model *mcpCallingModel) Complete(
	_ context.Context,
	request agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	model.turn++
	if model.turn == 1 {
		return agent.ModelResponse{
			Message: agent.Message{
				Role: agent.RoleAssistant,
				Content: []agent.Content{{
					Type: agent.ContentToolCall,
					ToolCall: &agent.ToolCall{
						ID:        "call-mcp",
						Name:      "mcp_probe",
						Arguments: json.RawMessage(`{}`),
					},
				}},
			},
			Provider:   "test",
			Model:      "test",
			StopReason: agent.StopReasonToolCalls,
		}, nil
	}
	last := request.Messages[len(request.Messages)-1]
	if last.Role != agent.RoleTool || last.Text() != "from-parent" {
		return agent.ModelResponse{}, errors.New("MCP result did not continue to the model")
	}
	return agent.ModelResponse{
		Message:    agent.TextMessage(agent.RoleAssistant, "MCP said: "+last.Text()),
		Provider:   "test",
		Model:      "test",
		StopReason: agent.StopReasonStop,
	}, nil
}
