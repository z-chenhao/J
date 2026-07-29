package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndResolve(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, path, `{
		"defaultProfile": "local",
		"profiles": {
			"local": {
				"provider": "openai",
				"api": "azure-openai-completions",
				"apiVersion": "2024-02-01",
				"model": "qwen",
				"baseURL": "http://127.0.0.1:8000/v1",
				"apiKey": "${LOCAL_API_KEY}",
				"reasoningField": "reasoning_content"
			}
		}
	}`)
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	name, profile, err := file.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if name != "local" || profile.API != "azure-openai-completions" ||
		profile.APIVersion != "2024-02-01" || profile.Model != "qwen" ||
		profile.APIKey != "${LOCAL_API_KEY}" ||
		profile.ReasoningField != "reasoning_content" {
		t.Fatalf("name=%q profile=%#v", name, profile)
	}
	if _, _, err := file.Resolve("missing"); err == nil {
		t.Fatal("missing profile was accepted")
	}
}

func TestLoadTypedMCPAndMemoryConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, path, `{
		"defaultProfile": "local",
		"profiles": {
			"local": {
				"provider": "openai",
				"model": "qwen",
				"baseURL": "http://127.0.0.1:8000/v1"
			}
		},
		"extensions": {
			"mcp": {
				"servers": {
					"filesystem": {
						"command": "mcp-server-filesystem",
						"args": ["/workspace"],
						"env": ["FILESYSTEM_TOKEN"],
						"cwd": "servers/filesystem",
						"tools": ["read_file", "list_directory"]
					}
				}
			}
		},
		"memory": {
			"transcript": {"path": "state/transcripts.db"},
			"longTerm": {"path": "state/memory.jsonl"}
		}
	}`)
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	server := file.Extensions.MCP.Servers["filesystem"]
	if server.Command != "mcp-server-filesystem" ||
		len(server.Args) != 1 || server.Env[0] != "FILESYSTEM_TOKEN" ||
		server.CWD != "servers/filesystem" ||
		strings.Join(server.Tools, ",") != "read_file,list_directory" {
		t.Fatalf("server=%#v", server)
	}
	if file.Memory.Transcript.Path != "state/transcripts.db" ||
		file.Memory.LongTerm.Path != "state/memory.jsonl" {
		t.Fatalf("memory=%#v", file.Memory)
	}
}

func TestLoadTypedSkillsAndSubagentsConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, path, `{
		"defaultProfile": "local",
		"profiles": {
			"local": {
				"provider": "openai",
				"model": "qwen",
				"baseURL": "http://127.0.0.1:8000/v1"
			},
			"research": {
				"provider": "openai",
				"model": "research-model",
				"baseURL": "http://127.0.0.1:8001/v1"
			}
		},
		"skills": {
			"paths": ["${HOME}/.agents/skills", "project-skills"],
			"include": ["research"]
		},
		"subagents": {
			"agents": {
				"research": {
					"description": "Research one bounded question.",
					"profile": "research",
					"systemPrompt": "Return concise evidence.",
					"tools": ["skill_read"]
				},
				"writer": {
					"description": "Write without tools.",
					"tools": []
				}
			}
		}
	}`)
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Skills.Paths) != 2 ||
		file.Skills.Paths[0] != "${HOME}/.agents/skills" ||
		len(file.Skills.Include) != 1 ||
		file.Skills.Include[0] != "research" {
		t.Fatalf("skills=%#v", file.Skills)
	}
	research := file.Subagents.Agents["research"]
	if research.Profile != "research" ||
		research.SystemPrompt != "Return concise evidence." ||
		len(research.Tools) != 1 || research.Tools[0] != "skill_read" {
		t.Fatalf("research=%#v", research)
	}
	writer := file.Subagents.Agents["writer"]
	if writer.Tools == nil || len(writer.Tools) != 0 {
		t.Fatalf("writer tools=%#v", writer.Tools)
	}
}

func TestLoadStreamableHTTPMCPConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, path, `{
		"defaultProfile":"local",
		"profiles":{
			"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}
		},
		"extensions":{"mcp":{"servers":{
			"search":{
				"url":"https://mcp.example.test/mcp/",
				"headers":{
					"Authorization":"Bearer ${SEARCH_MCP_TOKEN}",
					"X-Tenant":"tenant-a"
				},
				"tools":["search"]
			}
		}}}
	}`)
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	server := file.Extensions.MCP.Servers["search"]
	if server.URL != "https://mcp.example.test/mcp/" ||
		server.Headers["Authorization"] != "Bearer ${SEARCH_MCP_TOKEN}" ||
		server.Headers["X-Tenant"] != "tenant-a" ||
		len(server.Tools) != 1 {
		t.Fatalf("server=%#v", server)
	}
}

func TestLoadRejectsUnknownAndTrailingData(t *testing.T) {
	for name, contents := range map[string]string{
		"unknown":  `{"defaultProfile":"x","profiles":{},"extra":true}`,
		"trailing": `{"defaultProfile":"x","profiles":{}} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			writeTestFile(t, path, contents)
			if _, err := Load(path); err == nil {
				t.Fatal("invalid config was accepted")
			}
		})
	}
}

func TestLoadValidatesProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, path, `{
		"defaultProfile": "missing",
		"profiles": {
			"local": {"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}
		}
	}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "default profile") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadRejectsInvalidMCPAndMemoryConfiguration(t *testing.T) {
	tests := map[string]string{
		"former profile API key field": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1","apiKeyEnv":"TOKEN"}}
		}`,
		"former MCP bearer field": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{"url":"https://mcp.example/mcp","bearerTokenEnv":"TOKEN"}}}}
		}`,
		"empty extensions": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{}
		}`,
		"empty servers": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{}}}
		}`,
		"secret value": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{"command":"server","env":["TOKEN=secret"]}}}}
		}`,
		"empty tool allowlist": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{"command":"server","tools":[]}}}}
		}`,
		"blank tool name": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{"command":"server","tools":[" "]}}}}
		}`,
		"duplicate tool name": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{"command":"server","tools":["read_file","read_file"]}}}}
		}`,
		"missing MCP transport": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{}}}}
		}`,
		"multiple MCP transports": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{"command":"server","url":"https://mcp.example/mcp"}}}}
		}`,
		"stdio headers": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{"command":"server","headers":{"Authorization":"secret"}}}}}
		}`,
		"empty HTTP headers": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{"url":"https://mcp.example/mcp","headers":{}}}}}
		}`,
		"protocol-owned HTTP header": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{"url":"https://mcp.example/mcp","headers":{"Mcp-Session-Id":"bad"}}}}}
		}`,
		"malformed HTTP header value": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{"url":"https://mcp.example/mcp","headers":{"Authorization":"Bearer ${TOKEN"}}}}}
		}`,
		"HTTP process settings": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{"url":"https://mcp.example/mcp","args":["bad"]}}}}
		}`,
		"invalid HTTP URL": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"extensions":{"mcp":{"servers":{"x":{"url":"file:///tmp/mcp"}}}}
		}`,
		"empty memory": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"memory":{}
		}`,
		"empty memory path": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"memory":{"longTerm":{"path":""}}
		}`,
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			writeTestFile(t, path, contents)
			if _, err := Load(path); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestLoadRejectsInvalidSkillsAndSubagentsConfiguration(t *testing.T) {
	tests := map[string]string{
		"empty skill paths": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"skills":{"paths":[]}
		}`,
		"malformed skill environment reference": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"skills":{"paths":["${BAD"]}
		}`,
		"duplicate skill path": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"skills":{"paths":["skills","skills"]}
		}`,
		"empty skill include": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"skills":{"paths":["skills"],"include":[]}
		}`,
		"duplicate skill include": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"skills":{"paths":["skills"],"include":["one","one"]}
		}`,
		"empty subagents": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"subagents":{"agents":{}}
		}`,
		"blank subagent description": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"subagents":{"agents":{"research":{"description":" "}}}
		}`,
		"invalid subagent name": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"subagents":{"agents":{"bad name":{"description":"Research."}}}
		}`,
		"unknown subagent profile": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"subagents":{"agents":{"research":{"description":"Research.","profile":"missing"}}}
		}`,
		"duplicate subagent tool": `{
			"defaultProfile":"local",
			"profiles":{"local":{"provider":"openai","model":"qwen","baseURL":"http://localhost/v1"}},
			"subagents":{"agents":{"research":{"description":"Research.","tools":["bash","bash"]}}}
		}`,
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			writeTestFile(t, path, contents)
			if _, err := Load(path); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestResolveValue(t *testing.T) {
	lookup := func(name string) (string, bool) {
		values := map[string]string{
			"TOKEN":  "secret",
			"TENANT": "team-a",
		}
		value, exists := values[name]
		return value, exists
	}
	for name, test := range map[string]struct {
		value string
		want  string
	}{
		"literal":              {"literal-key", "literal-key"},
		"one reference":        {"${TOKEN}", "secret"},
		"mixed references":     {"Bearer ${TOKEN}:${TENANT}", "Bearer secret:team-a"},
		"dollar without brace": {"$TOKEN", "$TOKEN"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ResolveValue(test.value, lookup)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("ResolveValue(%q)=%q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestResolveValueRejectsInvalidReferences(t *testing.T) {
	for name, value := range map[string]string{
		"missing":      "${MISSING}",
		"unterminated": "Bearer ${TOKEN",
		"empty":        "${}",
		"shell syntax": "${TOKEN:-fallback}",
		"command":      "${!command}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ResolveValue(value, func(string) (string, bool) {
				return "", false
			}); err == nil {
				t.Fatal("invalid reference was accepted")
			}
		})
	}
}

func TestWriteDefaultCreatesPrivateConfigWithoutOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".j", "config.json")
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%#o", info.Mode().Perm())
	}
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	omlx := file.Profiles["omlx"]
	if file.DefaultProfile != "omlx" || len(file.Profiles) != 3 ||
		omlx.API != "openai-completions" ||
		omlx.BaseURL != "https://usej-model.tailb0426d.ts.net/v1" ||
		omlx.APIKey != "" {
		t.Fatalf("file=%#v", file)
	}
	if err := WriteDefault(path); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite error=%v", err)
	}
}

func TestDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(home, ".j", "config.json") {
		t.Fatalf("path=%q", path)
	}
}

func TestLoadAcceptsAuthenticatedJSpaceCapture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, path, `{
		"defaultProfile":"local",
		"profiles":{
			"local":{
				"provider":"openai",
				"model":"qwen",
				"baseURL":"https://model.example/v1"
			}
		},
		"jspace":{
			"url":"https://model.example/jspace/api/captures",
			"captureToken":"${JSPACE_CAPTURE_TOKEN}"
		}
	}`)
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.JSpace == nil ||
		file.JSpace.URL != "https://model.example/jspace/api/captures" ||
		file.JSpace.CaptureToken != "${JSPACE_CAPTURE_TOKEN}" {
		t.Fatalf("jspace=%#v", file.JSpace)
	}
}

func TestLoadRejectsInsecureJSpaceCapture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, path, `{
		"defaultProfile":"local",
		"profiles":{
			"local":{
				"provider":"openai",
				"model":"qwen",
				"baseURL":"https://model.example/v1"
			}
		},
		"jspace":{
			"url":"http://model.example/jspace/api/captures",
			"captureToken":"secret"
		}
	}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("error=%v", err)
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
