// Package settings owns J-tui's private on-disk configuration.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const maxFileSize = 1 << 20

// File is J-tui's experimental configuration file.
type File struct {
	DefaultProfile string             `json:"defaultProfile"`
	Profiles       map[string]Profile `json:"profiles"`
	Extensions     *Extensions        `json:"extensions,omitempty"`
	Memory         *Memory            `json:"memory,omitempty"`
}

// Profile describes one concrete model connection.
type Profile struct {
	Provider        string `json:"provider"`
	API             string `json:"api,omitempty"`
	APIVersion      string `json:"apiVersion,omitempty"`
	Model           string `json:"model"`
	BaseURL         string `json:"baseURL"`
	APIKeyEnv       string `json:"apiKeyEnv,omitempty"`
	ReasoningField  string `json:"reasoningField,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

// Extensions contains J-tui's typed construction-time extension recipes.
type Extensions struct {
	MCP *MCP `json:"mcp,omitempty"`
}

// MCP describes the explicitly configured MCP servers.
type MCP struct {
	Servers map[string]MCPServer `json:"servers"`
}

// MCPServer describes either one stdio MCP process or one Streamable HTTP
// endpoint. Env and BearerTokenEnv contain environment-variable names, never
// values.
type MCPServer struct {
	Command        string   `json:"command,omitempty"`
	Args           []string `json:"args,omitempty"`
	Env            []string `json:"env,omitempty"`
	CWD            string   `json:"cwd,omitempty"`
	URL            string   `json:"url,omitempty"`
	BearerTokenEnv string   `json:"bearerTokenEnv,omitempty"`
	Tools          []string `json:"tools,omitempty"`
}

// Memory contains J-tui's two independently optional J-mem capabilities.
type Memory struct {
	Transcript *MemoryFile `json:"transcript,omitempty"`
	LongTerm   *MemoryFile `json:"longTerm,omitempty"`
}

// MemoryFile names one local state file. Relative paths are resolved from the
// directory containing the J-tui configuration file.
type MemoryFile struct {
	Path string `json:"path"`
}

// DefaultPath returns J-tui's user-scoped configuration path.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".j", "config.json"), nil
}

// Load reads and validates one configuration file.
func Load(path string) (File, error) {
	data, err := readFile(path)
	if err != nil {
		return File{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file File
	if err := decoder.Decode(&file); err != nil {
		return File{}, fmt.Errorf("decode J-tui config %q: %w", path, err)
	}
	if err := requireEOF(decoder); err != nil {
		return File{}, fmt.Errorf("decode J-tui config %q: %w", path, err)
	}
	if err := file.validate(); err != nil {
		return File{}, fmt.Errorf("validate J-tui config %q: %w", path, err)
	}
	return file, nil
}

// Resolve returns the requested profile, or the configured default when name
// is empty.
func (file File) Resolve(name string) (string, Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = file.DefaultProfile
	}
	profile, ok := file.Profiles[name]
	if !ok {
		return "", Profile{}, fmt.Errorf("profile %q is not defined", name)
	}
	return name, profile, nil
}

// WriteDefault creates a starter configuration without overwriting an existing
// file.
func WriteDefault(path string) error {
	file := File{
		DefaultProfile: "omlx",
		Profiles: map[string]Profile{
			"omlx": {
				Provider:       "openai",
				API:            "openai-completions",
				Model:          "Qwen3.6-35B-A3B-oQ4e-mtp",
				BaseURL:        "http://127.0.0.1:8000/v1",
				APIKeyEnv:      "OMLX_API_KEY",
				ReasoningField: "reasoning_content",
			},
			"deepseek": {
				Provider:       "openai",
				API:            "openai-completions",
				Model:          "deepseek-chat",
				BaseURL:        "https://api.deepseek.com",
				APIKeyEnv:      "DEEPSEEK_API_KEY",
				ReasoningField: "reasoning_content",
			},
			"ollama": {
				Provider:       "openai",
				API:            "openai-completions",
				Model:          "qwen3",
				BaseURL:        "http://127.0.0.1:11434/v1",
				ReasoningField: "reasoning",
			},
		},
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode starter J-tui config: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create J-tui config directory: %w", err)
	}
	handle, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("J-tui config %q already exists", path)
		}
		return fmt.Errorf("create J-tui config %q: %w", path, err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if _, err := handle.Write(data); err != nil {
		_ = handle.Close()
		return fmt.Errorf("write J-tui config %q: %w", path, err)
	}
	if err := handle.Close(); err != nil {
		return fmt.Errorf("close J-tui config %q: %w", path, err)
	}
	complete = true
	return nil
}

func readFile(path string) ([]byte, error) {
	handle, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open J-tui config %q: %w", path, err)
	}
	defer handle.Close()
	data, err := io.ReadAll(io.LimitReader(handle, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read J-tui config %q: %w", path, err)
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("J-tui config %q exceeds 1 MiB", path)
	}
	return data, nil
}

func requireEOF(decoder *json.Decoder) error {
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

func (file File) validate() error {
	if file.DefaultProfile == "" || file.DefaultProfile != strings.TrimSpace(file.DefaultProfile) {
		return errors.New("defaultProfile must be non-empty and trimmed")
	}
	if len(file.Profiles) == 0 {
		return errors.New("at least one profile is required")
	}
	for name, profile := range file.Profiles {
		if name == "" || name != strings.TrimSpace(name) {
			return fmt.Errorf("profile name %q must be non-empty and trimmed", name)
		}
		if strings.TrimSpace(profile.Provider) == "" {
			return fmt.Errorf("profile %q provider is required", name)
		}
		if strings.TrimSpace(profile.Model) == "" {
			return fmt.Errorf("profile %q model is required", name)
		}
		if strings.TrimSpace(profile.BaseURL) == "" {
			return fmt.Errorf("profile %q baseURL is required", name)
		}
	}
	if _, ok := file.Profiles[file.DefaultProfile]; !ok {
		return fmt.Errorf("default profile %q is not defined", file.DefaultProfile)
	}
	if err := file.Extensions.validate(); err != nil {
		return err
	}
	if err := file.Memory.validate(); err != nil {
		return err
	}
	return nil
}

func (extensions *Extensions) validate() error {
	if extensions == nil {
		return nil
	}
	if extensions.MCP == nil {
		return errors.New("extensions must configure mcp")
	}
	if len(extensions.MCP.Servers) == 0 {
		return errors.New("extensions.mcp must configure at least one server")
	}
	for name, server := range extensions.MCP.Servers {
		if name == "" || name != strings.TrimSpace(name) {
			return fmt.Errorf("MCP server name %q must be non-empty and trimmed", name)
		}
		if server.Command != strings.TrimSpace(server.Command) {
			return fmt.Errorf("MCP server %q command must be trimmed", name)
		}
		if server.URL != strings.TrimSpace(server.URL) {
			return fmt.Errorf("MCP server %q url must be trimmed", name)
		}
		hasCommand := server.Command != ""
		hasURL := server.URL != ""
		if hasCommand == hasURL {
			return fmt.Errorf(
				"MCP server %q must configure exactly one of command or url",
				name,
			)
		}
		if server.CWD != strings.TrimSpace(server.CWD) {
			return fmt.Errorf("MCP server %q cwd must be trimmed", name)
		}
		if hasURL {
			if len(server.Args) > 0 || len(server.Env) > 0 || server.CWD != "" {
				return fmt.Errorf(
					"MCP server %q url transport does not use args, env, or cwd",
					name,
				)
			}
			parsed, err := url.ParseRequestURI(server.URL)
			if err != nil || parsed.Host == "" ||
				(parsed.Scheme != "http" && parsed.Scheme != "https") ||
				parsed.User != nil {
				return fmt.Errorf(
					"MCP server %q url must be an HTTP(S) URL without user information",
					name,
				)
			}
		} else if server.BearerTokenEnv != "" {
			return fmt.Errorf(
				"MCP server %q bearerTokenEnv requires url transport",
				name,
			)
		}
		if server.BearerTokenEnv != "" {
			if err := validateEnvironmentName(server.BearerTokenEnv); err != nil {
				return fmt.Errorf("MCP server %q bearerTokenEnv: %w", name, err)
			}
		}
		seenEnvironment := make(map[string]struct{}, len(server.Env))
		for _, variable := range server.Env {
			if err := validateEnvironmentName(variable); err != nil {
				return fmt.Errorf("MCP server %q environment name %q: %w", name, variable, err)
			}
			if _, exists := seenEnvironment[variable]; exists {
				return fmt.Errorf(
					"MCP server %q repeats environment name %q",
					name,
					variable,
				)
			}
			seenEnvironment[variable] = struct{}{}
		}
		if server.Tools != nil && len(server.Tools) == 0 {
			return fmt.Errorf(
				"MCP server %q tools must be omitted or contain at least one tool name",
				name,
			)
		}
		seenTools := make(map[string]struct{}, len(server.Tools))
		for _, tool := range server.Tools {
			if tool == "" || tool != strings.TrimSpace(tool) {
				return fmt.Errorf(
					"MCP server %q tool name %q must be non-empty and trimmed",
					name,
					tool,
				)
			}
			if _, exists := seenTools[tool]; exists {
				return fmt.Errorf("MCP server %q repeats tool name %q", name, tool)
			}
			seenTools[tool] = struct{}{}
		}
	}
	return nil
}

func validateEnvironmentName(name string) error {
	if name == "" || name != strings.TrimSpace(name) || strings.Contains(name, "=") {
		return errors.New("must be non-empty, trimmed, and contain no '='")
	}
	return nil
}

func (memory *Memory) validate() error {
	if memory == nil {
		return nil
	}
	if memory.Transcript == nil && memory.LongTerm == nil {
		return errors.New("memory must configure transcript or longTerm")
	}
	for name, file := range map[string]*MemoryFile{
		"transcript": memory.Transcript,
		"longTerm":   memory.LongTerm,
	} {
		if file == nil {
			continue
		}
		if file.Path == "" || file.Path != strings.TrimSpace(file.Path) {
			return fmt.Errorf("memory.%s.path must be non-empty and trimmed", name)
		}
	}
	return nil
}
