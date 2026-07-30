package packages

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/z-chenhao/J/J-agent/agent"
)

func TestHostLoadsPackageToolAndSkillRoot(t *testing.T) {
	if os.Getenv("J_PACKAGES_TEST_SERVER") == "1" {
		runPackageTestServer()
		os.Exit(0)
	}

	root := t.TempDir()
	serverName := "j-packages-test-server"
	copyExecutable(t, os.Args[0], filepath.Join(root, serverName))
	if err := os.Mkdir(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestManifest(t, root, `{
		"schemaVersion":"j.package.v0.1",
		"id":"dev.usej.host",
		"version":"1.0.0",
		"contributes":{
			"mcp":[{
				"id":"echo",
				"command":"`+serverName+`",
				"args":["-test.run=^TestHostLoadsPackageToolAndSkillRoot$"],
				"env":["J_PACKAGES_TEST_SERVER"],
				"tools":["package_echo"]
			}],
			"skills":["skills"]
		}
	}`)
	registryPath := filepath.Join(t.TempDir(), "packages.json")
	digest, err := manifestDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteRegistry(registryPath, Registry{Packages: []Entry{{
		ID:             "dev.usej.host",
		Version:        "1.0.0",
		Source:         "local:" + root,
		Root:           root,
		ManifestSHA256: digest,
	}}}); err != nil {
		t.Fatal(err)
	}
	lookup := map[string]string{
		"PATH":                   root + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME":                   t.TempDir(),
		"J_PACKAGES_TEST_SERVER": "1",
	}
	session, err := Open(context.Background(), HostConfig{
		RegistryPath: registryPath,
		LookupEnv: func(name string) (string, bool) {
			value, exists := lookup[name]
			return value, exists
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools := session.Tools()
	if len(tools) != 1 || tools[0].Spec().Name != "package_echo" {
		t.Fatalf("tools=%+v", tools)
	}
	output, err := tools[0].Call(
		context.Background(),
		json.RawMessage(`{"text":"PACKAGE_OK"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if output != "PACKAGE_OK" {
		t.Fatalf("output=%q", output)
	}
	runner, err := agent.New(&packageCallingModel{}, agent.WithTools(tools...))
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), "call the installed package", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.Text() != "package result: PACKAGE_AGENT_OK" {
		t.Fatalf("agent result=%q", result.Message.Text())
	}
	roots := session.SkillRoots()
	expectedRoot, err := filepath.EvalSymlinks(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0] != expectedRoot {
		t.Fatalf("roots=%q", roots)
	}
	info := session.ToolInfo()
	if len(info) != 1 || !info[0].Selected || info[0].Package != "dev.usej.host" {
		t.Fatalf("tool info=%+v", info)
	}
}

type packageCallingModel struct {
	turn int
}

func (model *packageCallingModel) Complete(
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
						ID:        "package-call",
						Name:      "package_echo",
						Arguments: json.RawMessage(`{"text":"PACKAGE_AGENT_OK"}`),
					},
				}},
			},
			Provider:   "test",
			Model:      "test",
			StopReason: agent.StopReasonToolCalls,
		}, nil
	}
	last := request.Messages[len(request.Messages)-1]
	if last.Role != agent.RoleTool || last.Text() != "PACKAGE_AGENT_OK" {
		return agent.ModelResponse{}, errors.New("package result did not continue to the model")
	}
	return agent.ModelResponse{
		Message:    agent.TextMessage(agent.RoleAssistant, "package result: "+last.Text()),
		Provider:   "test",
		Model:      "test",
		StopReason: agent.StopReasonStop,
	}, nil
}

func TestHostDoesNotStartAnythingWithoutRegistry(t *testing.T) {
	session, err := Open(context.Background(), HostConfig{
		RegistryPath: filepath.Join(t.TempDir(), "missing.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if len(session.Tools()) != 0 || len(session.SkillRoots()) != 0 {
		t.Fatalf("session=%+v", session)
	}
}

func TestProcessEnvironmentForwardsOnlyBaselineAndRequested(t *testing.T) {
	values := map[string]string{
		"PATH":   "/bin",
		"HOME":   "/home/test",
		"SECRET": "allowed",
		"HIDDEN": "not-forwarded",
	}
	environment, err := processEnvironment([]string{"SECRET"}, func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	if !strings.Contains(joined, "SECRET=allowed") || strings.Contains(joined, "HIDDEN") {
		t.Fatalf("environment=%q", environment)
	}
	if _, err := processEnvironment([]string{"MISSING"}, func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	}); err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("error=%v", err)
	}
}

func runPackageTestServer() {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "j-package-host-test",
		Version: "0.1.0",
	}, nil)
	server.AddTool(&mcpsdk.Tool{
		Name:        "package_echo",
		Description: "Echo one package test value.",
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

func copyExecutable(t *testing.T, source, target string) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}
