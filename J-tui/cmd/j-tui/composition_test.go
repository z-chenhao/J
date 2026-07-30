package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/z-chenhao/J/J-agent/agent"
	jpackages "github.com/z-chenhao/J/J-packages"
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

func TestComposeRuntimeLoadsInstalledPackageToolsAndSkills(t *testing.T) {
	registryPath := installTestPackage(t, "package_probe")
	t.Setenv("FORWARDED_VALUE", "from-package")

	listing, err := composeRuntime(context.Background(), config{
		configPath:       filepath.Join(t.TempDir(), ".j", "config.json"),
		listTools:        true,
		packagesRegistry: registryPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer listing.Close()
	if len(listing.tools) != 1 || listing.tools[0].Spec().Name != "package_probe" {
		t.Fatalf("tools=%v", toolNames(listing.tools))
	}
	if len(listing.mcpTools) != 1 || listing.mcpTools[0] != (mcpToolObservation{
		Server:   "dev.usej.test-package/probe",
		Name:     "package_probe",
		Selected: true,
	}) {
		t.Fatalf("observations=%+v", listing.mcpTools)
	}
	output, err := listing.tools[0].Call(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if output != "from-package" {
		t.Fatalf("package tool output=%q", output)
	}

	composed, err := composeRuntime(context.Background(), config{
		configPath:       filepath.Join(t.TempDir(), ".j", "config.json"),
		packagesRegistry: registryPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer composed.Close()
	if got := strings.Join(toolNames(composed.tools), ","); got != "bash,package_probe,skill_read" {
		t.Fatalf("tools=%s", got)
	}
	skillOutput, err := findTool(t, composed.tools, "skill_read").Call(
		context.Background(),
		json.RawMessage(`{"name":"package-probe"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(skillOutput, "Package Probe") {
		t.Fatalf("skill output=%q", skillOutput)
	}
}

func TestComposeRuntimeCanDisableInstalledPackages(t *testing.T) {
	registryPath := installTestPackage(t, "package_probe")
	composition, err := composeRuntime(context.Background(), config{
		configPath:       filepath.Join(t.TempDir(), ".j", "config.json"),
		listTools:        true,
		noPackages:       true,
		packagesRegistry: registryPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer composition.Close()
	if len(composition.tools) != 0 || len(composition.mcpTools) != 0 {
		t.Fatalf("tools=%v observations=%+v", toolNames(composition.tools), composition.mcpTools)
	}
}

func TestComposeRuntimeSelectsOnlyConfiguredPackageObserver(t *testing.T) {
	root := t.TempDir()
	observerPath := filepath.Join(root, "observer")
	if err := os.WriteFile(observerPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"schemaVersion":"j.package.v0.2",
		"id":"dev.usej.observer-test",
		"version":"1.0.0",
		"contributes":{"observers":[{
			"id":"trace",
			"command":"./observer",
			"env":["OBSERVER_SECRET"],
			"permissions":["agent.events","model.frames"]
		}]}
	}`
	if err := os.WriteFile(
		filepath.Join(root, jpackages.ManifestFilename),
		[]byte(manifest),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(t.TempDir(), "packages.json")
	manager, err := jpackages.NewManager(registryPath, filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OBSERVER_SECRET", "allowed")
	composition, err := composeRuntime(context.Background(), config{
		configPath:       filepath.Join(t.TempDir(), ".j", "config.json"),
		packagesRegistry: registryPath,
		extensions: &settings.Extensions{Observers: &settings.Observers{
			Include: []string{"dev.usej.observer-test/trace"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer composition.Close()
	if len(composition.observers) != 1 ||
		composition.observers[0].Name != "dev.usej.observer-test/trace" ||
		!strings.Contains(strings.Join(composition.observers[0].Env, "\n"), "OBSERVER_SECRET=allowed") {
		t.Fatalf("observers=%+v", composition.observers)
	}
}

func TestComposeRuntimeRejectsPackageToolCollision(t *testing.T) {
	registryPath := installTestPackage(t, "bash")
	_, err := composeRuntime(context.Background(), config{
		configPath:       filepath.Join(t.TempDir(), ".j", "config.json"),
		packagesRegistry: registryPath,
	})
	if err == nil || !strings.Contains(err.Error(), `tool name "bash"`) ||
		!strings.Contains(err.Error(), "J Packages") {
		t.Fatalf("error=%v", err)
	}
}

func TestComposeRuntimeLoadsSkillsAndRunsConfiguredSubagent(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".j", "config.json")
	skillDirectory := filepath.Join(filepath.Dir(configPath), "skills", "research")
	if err := os.MkdirAll(filepath.Join(skillDirectory, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(skillDirectory, "SKILL.md"),
		[]byte("---\nname: research\ndescription: Research with evidence.\n---\n\n# Research\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(skillDirectory, "references", "guide.md"),
		[]byte("evidence guide"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("path=%q", request.URL.Path)
		}
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.Messages) != 2 ||
			payload.Messages[0].Role != "system" ||
			payload.Messages[0].Content != "Return concise evidence." ||
			payload.Messages[1].Content != "find evidence" {
			t.Errorf("messages=%#v", payload.Messages)
		}
		if len(payload.Tools) != 1 ||
			payload.Tools[0].Function.Name != "skill_read" {
			t.Errorf("tools=%#v", payload.Tools)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(
			"data: " +
				`{"id":"child-1","model":"child-model","choices":[{"delta":{"content":"child answer"},"finish_reason":"stop"}]}` +
				"\n\n" +
				"data: " +
				`{"id":"child-1","model":"child-model","choices":[],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}` +
				"\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer server.Close()

	composition, err := composeRuntime(context.Background(), config{
		configPath:      configPath,
		provider:        "openai",
		api:             "openai-completions",
		model:           "child-model",
		baseURL:         server.URL + "/v1",
		reasoningField:  "omit",
		reasoningEffort: "default",
		skills: &settings.Skills{
			Paths: []string{"skills"},
		},
		subagents: &settings.Subagents{
			Agents: map[string]settings.Subagent{
				"research": {
					Description:  "Research one bounded question.",
					SystemPrompt: "Return concise evidence.",
					Tools:        []string{"skill_read"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer composition.Close()

	tools := make(map[string]agent.Tool, len(composition.tools))
	for _, tool := range composition.tools {
		tools[tool.Spec().Name] = tool
	}
	if len(tools) != 3 || tools["bash"] == nil ||
		tools["skill_read"] == nil || tools["subagent_run"] == nil {
		t.Fatalf("tools=%v", tools)
	}
	skillOutput, err := tools["skill_read"].Call(
		context.Background(),
		json.RawMessage(`{"name":"research","resource":"references/guide.md"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(skillOutput, "evidence guide") {
		t.Fatalf("skill output=%q", skillOutput)
	}
	subagentOutput, err := tools["subagent_run"].Call(
		context.Background(),
		json.RawMessage(`{"agent":"research","task":"find evidence"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Content string       `json:"content"`
		Usage   *agent.Usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(subagentOutput), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Content != "child answer" || decoded.Usage == nil ||
		decoded.Usage.TotalTokens != 10 || requests.Load() != 1 {
		t.Fatalf("subagent output=%q requests=%d", subagentOutput, requests.Load())
	}
}

func TestComposeRuntimeResumesPersistedSubagentSession(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestNumber := requests.Add(1)
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		wantContents := []string{"Keep child context.", "first"}
		if requestNumber == 2 {
			wantContents = append(wantContents, "answer 1", "second")
		}
		if len(payload.Messages) != len(wantContents) {
			t.Errorf("request %d messages=%#v", requestNumber, payload.Messages)
		} else {
			for index, content := range wantContents {
				if payload.Messages[index].Content != content {
					t.Errorf(
						"request %d message %d=%#v",
						requestNumber,
						index,
						payload.Messages[index],
					)
				}
			}
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(
			writer,
			"data: {\"id\":\"child-%d\",\"model\":\"child-model\",\"choices\":[{\"delta\":{\"content\":\"answer %d\"},\"finish_reason\":\"stop\"}]}\n\n"+
				"data: [DONE]\n\n",
			requestNumber,
			requestNumber,
		)
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), ".j", "config.json")
	cfg := config{
		configPath:      configPath,
		session:         "parent-session",
		provider:        "openai",
		api:             "openai-completions",
		model:           "child-model",
		baseURL:         server.URL + "/v1",
		reasoningField:  "omit",
		reasoningEffort: "default",
		memory: &settings.Memory{
			Transcript: &settings.MemoryFile{Path: "state/transcripts.db"},
		},
		subagents: &settings.Subagents{
			Agents: map[string]settings.Subagent{
				"research": {
					Description:  "Research one bounded question.",
					SystemPrompt: "Keep child context.",
					Tools:        []string{},
				},
			},
		},
	}

	firstComposition, err := composeRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	firstTool := findTool(t, firstComposition.tools, "subagent_run")
	firstOutput, err := firstTool.Call(
		context.Background(),
		json.RawMessage(`{"agent":"research","task":"first"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var first struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal([]byte(firstOutput), &first); err != nil {
		t.Fatal(err)
	}
	if first.Session == "" {
		t.Fatalf("first output=%s", firstOutput)
	}
	if err := firstComposition.Close(); err != nil {
		t.Fatal(err)
	}

	secondComposition, err := composeRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer secondComposition.Close()
	secondTool := findTool(t, secondComposition.tools, "subagent_run")
	secondOutput, err := secondTool.Call(
		context.Background(),
		json.RawMessage(
			`{"agent":"research","task":"second","session":"`+
				first.Session+`"}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	var second struct {
		Session string `json:"session"`
		Resumed bool   `json:"resumed"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(secondOutput), &second); err != nil {
		t.Fatal(err)
	}
	if second.Session != first.Session || !second.Resumed ||
		second.Content != "answer 2" || requests.Load() != 2 {
		t.Fatalf("second output=%s requests=%d", secondOutput, requests.Load())
	}
}

func TestLoadConfiguredSkillsAppliesExactSelection(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".j", "config.json")
	root := filepath.Join(filepath.Dir(configPath), "skills")
	for _, name := range []string{"alpha", "beta"} {
		directory := filepath.Join(root, name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(directory, "SKILL.md"),
			[]byte("---\nname: "+name+"\ndescription: "+name+" skill.\n---\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	all, selected, err := loadConfiguredSkills(configPath, &settings.Skills{
		Paths:   []string{"skills"},
		Include: []string{"beta"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Skills()) != 2 || len(selected.Skills()) != 1 ||
		selected.Skills()[0].Name != "beta" {
		t.Fatalf("all=%#v selected=%#v", all.Skills(), selected.Skills())
	}
}

func findTool(t *testing.T, tools []agent.Tool, name string) agent.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Spec().Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func toolNames(tools []agent.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Spec().Name)
	}
	return names
}

func installTestPackage(t *testing.T, toolName string) string {
	t.Helper()
	root := t.TempDir()
	serverPath := filepath.Join(root, "server")
	executable, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serverPath, executable, 0o755); err != nil {
		t.Fatal(err)
	}
	skillRoot := filepath.Join(root, "skills", "package-probe")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(skillRoot, "SKILL.md"),
		[]byte("---\nname: package-probe\ndescription: Probe one installed package.\n---\n\n# Package Probe\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"schemaVersion":"j.package.v0.1",
		"id":"dev.usej.test-package",
		"version":"1.0.0",
		"contributes":{
			"mcp":[{
				"id":"probe",
				"command":"./server",
				"args":["-test.run=^TestMCPStdioHelper$"],
				"env":[
					"J_TUI_MCP_TEST_SERVER",
					"J_TUI_MCP_TOOL_NAME",
					"FORWARDED_VALUE"
				],
				"tools":["` + toolName + `"]
			}],
			"skills":["skills"]
		}
	}`
	if err := os.WriteFile(
		filepath.Join(root, jpackages.ManifestFilename),
		[]byte(manifest),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(t.TempDir(), "packages.json")
	manager, err := jpackages.NewManager(registryPath, filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("J_TUI_MCP_TEST_SERVER", "1")
	t.Setenv("J_TUI_MCP_TOOL_NAME", toolName)
	t.Setenv("FORWARDED_VALUE", "from-package")
	if _, err := manager.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	return registryPath
}

func TestComposeRuntimeRejectsUnknownSubagentTool(t *testing.T) {
	_, err := composeRuntime(context.Background(), config{
		configPath:      filepath.Join(t.TempDir(), "config.json"),
		provider:        "openai",
		api:             "openai-completions",
		model:           "test",
		baseURL:         "http://127.0.0.1:1/v1",
		reasoningField:  "omit",
		reasoningEffort: "default",
		subagents: &settings.Subagents{
			Agents: map[string]settings.Subagent{
				"research": {
					Description: "Research.",
					Tools:       []string{"missing"},
				},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), `"missing"`) ||
		!strings.Contains(err.Error(), `"bash"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestSelectSubagentToolsDistinguishesOmittedAndEmpty(t *testing.T) {
	available := []agent.Tool{
		namedTool{name: "one"},
		namedTool{name: "two"},
	}
	inherited, err := selectSubagentTools("test", nil, available)
	if err != nil {
		t.Fatal(err)
	}
	if len(inherited) != 2 {
		t.Fatalf("inherited=%d", len(inherited))
	}
	selected, err := selectSubagentTools("test", []string{}, available)
	if err != nil {
		t.Fatal(err)
	}
	if selected == nil || len(selected) != 0 {
		t.Fatalf("selected=%#v", selected)
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

func TestComposeRuntimeReportsMCPInitializationStderr(t *testing.T) {
	t.Setenv("J_TUI_MCP_FAILURE_SERVER", "1")
	_, err := composeRuntime(context.Background(), config{
		configPath: filepath.Join(t.TempDir(), "config.json"),
		extensions: &settings.Extensions{MCP: &settings.MCP{
			Servers: map[string]settings.MCPServer{
				"failure": {
					Command: os.Args[0],
					Args:    []string{"-test.run=^TestMCPStdioFailureHelper$"},
					Env:     []string{"J_TUI_MCP_FAILURE_SERVER"},
				},
			},
		}},
	})
	if err == nil ||
		!strings.Contains(err.Error(), `start MCP server "failure"`) ||
		!strings.Contains(err.Error(), "calling \"initialize\": EOF") ||
		!strings.Contains(err.Error(), "configured filesystem root is not accessible") ||
		strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("error=%v", err)
	}
}

func TestMCPStartupStderrIsBoundedAndStopsAfterInitialization(t *testing.T) {
	capture := newMCPStartupStderr()
	content := strings.Repeat("x", maxMCPStartupStderrBytes) + "tail"
	if written, err := capture.Write([]byte(content)); err != nil || written != len(content) {
		t.Fatalf("write=%d error=%v", written, err)
	}
	details := capture.stop()
	if !strings.HasPrefix(details, "[stderr truncated to last 16 KiB]\n") ||
		!strings.HasSuffix(details, "tail") ||
		len(details) > maxMCPStartupStderrBytes+64 {
		t.Fatalf("details length=%d prefix=%q", len(details), details[:min(len(details), 64)])
	}
	if _, err := capture.Write([]byte("runtime noise")); err != nil {
		t.Fatal(err)
	}
	if details := capture.stop(); details != "" {
		t.Fatalf("post-initialization stderr=%q", details)
	}
}

func TestComposeRuntimeLoadsStreamableHTTPMCPWithConfiguredHeaders(t *testing.T) {
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
			if request.Header.Get("X-J-Tenant") != "team-a" {
				http.Error(writer, "missing tenant", http.StatusBadRequest)
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
					URL: httpServer.URL,
					Headers: map[string]string{
						"Authorization": "Bearer ${REMOTE_MCP_TOKEN}",
						"X-J-Tenant":    "team-a",
					},
					Tools: []string{"remote_probe"},
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

func TestMCPHTTPClientExtendsTLSHandshakeBudgetWithoutMutatingDefault(t *testing.T) {
	defaultTransport := http.DefaultTransport.(*http.Transport)
	defaultTimeout := defaultTransport.TLSHandshakeTimeout

	client := newMCPHTTPClient(nil)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport=%T", client.Transport)
	}
	if transport == defaultTransport {
		t.Fatal("MCP HTTP client reused the mutable default transport")
	}
	if transport.TLSHandshakeTimeout != mcpTLSHandshakeTimeout {
		t.Fatalf("TLS handshake timeout=%s", transport.TLSHandshakeTimeout)
	}
	if defaultTransport.TLSHandshakeTimeout != defaultTimeout {
		t.Fatalf("default TLS handshake timeout=%s", defaultTransport.TLSHandshakeTimeout)
	}
	if client.CheckRedirect != nil {
		t.Fatal("headerless MCP HTTP client changed default redirect behavior")
	}
}

func TestComposeRuntimeRequiresConfiguredHTTPHeaderReference(t *testing.T) {
	if err := os.Unsetenv("MISSING_REMOTE_MCP_TOKEN"); err != nil {
		t.Fatal(err)
	}
	_, err := connectMCPServer(context.Background(), "", settings.MCPServer{
		URL: "https://mcp.example.test/mcp",
		Headers: map[string]string{
			"Authorization": "Bearer ${MISSING_REMOTE_MCP_TOKEN}",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "MISSING_REMOTE_MCP_TOKEN") {
		t.Fatalf("error=%v", err)
	}
}

func TestConfiguredHTTPHeadersAreNotForwardedAcrossRedirects(t *testing.T) {
	var targetRequests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {
			targetRequests.Add(1)
		},
	))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
		},
	))
	defer redirect.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := connectMCPServer(ctx, "", settings.MCPServer{
		URL: redirect.URL,
		Headers: map[string]string{
			"Authorization": "Bearer literal-secret",
		},
	})
	if err == nil {
		t.Fatal("redirecting MCP endpoint was accepted")
	}
	if targetRequests.Load() != 0 {
		t.Fatal("configured HTTP headers followed a redirect")
	}
}

func TestResolveHTTPHeadersRejectsControlCharacters(t *testing.T) {
	_, err := resolveHTTPHeaders(map[string]string{
		"Authorization": "Bearer good\r\nX-Injected: bad",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid value") {
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
		runner:    newObservedRunner(agentRunner, composition.subagents),
		history:   agentRunner.History,
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

func TestPersistentRunnerSavesAcceptedUserCheckpointOnFailedRun(t *testing.T) {
	cfg := config{
		configPath: filepath.Join(t.TempDir(), ".j", "config.json"),
		session:    "stable-checkpoint",
		memory: &settings.Memory{
			Transcript: &settings.MemoryFile{Path: "state/transcripts.db"},
		},
	}
	composition, err := composeRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer composition.Close()
	agentRunner, err := agent.New(failingModel{})
	if err != nil {
		t.Fatal(err)
	}
	runner := &persistentRunner{
		runner:    newObservedRunner(agentRunner, composition.subagents),
		history:   agentRunner.History,
		store:     composition.transcripts,
		sessionID: cfg.session,
	}
	if _, err := runner.Run(context.Background(), "checkpoint this user turn", nil); err == nil {
		t.Fatal("failed run unexpectedly succeeded")
	}
	history, err := composition.transcripts.Load(context.Background(), cfg.session)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 ||
		history[0].Role != agent.RoleUser ||
		history[0].Text() != "checkpoint this user turn" {
		t.Fatalf("accepted user checkpoint=%#v", history)
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

func TestMCPStdioFailureHelper(t *testing.T) {
	if os.Getenv("J_TUI_MCP_FAILURE_SERVER") != "1" {
		return
	}
	_, _ = os.Stderr.WriteString("\x1b[31mconfigured filesystem root is not accessible\x1b[0m\n")
	os.Exit(2)
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

type namedTool struct {
	name string
}

func (tool namedTool) Spec() agent.ToolSpec {
	return agent.ToolSpec{
		Name:        tool.name,
		Description: "Named test tool.",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (namedTool) Call(context.Context, json.RawMessage) (string, error) {
	return "ok", nil
}
