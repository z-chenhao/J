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
	"regexp"
	"strings"
)

const maxFileSize = 1 << 20

var observerNamePattern = regexp.MustCompile(
	`^[a-z0-9]+(?:[.-][a-z0-9]+)*/[a-z0-9]+(?:[.-][a-z0-9]+)*$`,
)

// File is J-tui's experimental configuration file.
type File struct {
	DefaultProfile string             `json:"defaultProfile"`
	Profiles       map[string]Profile `json:"profiles"`
	Extensions     *Extensions        `json:"extensions,omitempty"`
	Memory         *Memory            `json:"memory,omitempty"`
	Skills         *Skills            `json:"skills,omitempty"`
	Subagents      *Subagents         `json:"subagents,omitempty"`
}

// Profile describes one concrete model connection.
type Profile struct {
	Provider        string `json:"provider"`
	API             string `json:"api,omitempty"`
	APIVersion      string `json:"apiVersion,omitempty"`
	Model           string `json:"model"`
	BaseURL         string `json:"baseURL"`
	APIKey          string `json:"apiKey,omitempty"`
	ReasoningField  string `json:"reasoningField,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

// Extensions contains J-tui's typed construction-time extension recipes.
type Extensions struct {
	MCP       *MCP       `json:"mcp,omitempty"`
	Observers *Observers `json:"observers,omitempty"`
}

// Observers selects exact installed J Package observer contributions. Installing
// a package alone never grants it Agent events or model frames.
type Observers struct {
	Include []string `json:"include"`
}

// MCP describes the explicitly configured MCP servers.
type MCP struct {
	Servers map[string]MCPServer `json:"servers"`
}

// MCPServer describes either one stdio MCP process or one Streamable HTTP
// endpoint.
type MCPServer struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     []string          `json:"env,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Tools   []string          `json:"tools,omitempty"`
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

// Skills describes explicitly configured Agent Skills roots.
type Skills struct {
	Paths   []string `json:"paths"`
	Include []string `json:"include,omitempty"`
}

// Subagents contains J-tui's explicitly named foreground subagent recipes.
type Subagents struct {
	Agents map[string]Subagent `json:"agents"`
}

// Subagent selects one model profile, optional system prompt, and exact Tool
// set. An omitted Tools field inherits all non-subagent tools; an empty array
// selects none.
type Subagent struct {
	Description  string   `json:"description"`
	Profile      string   `json:"profile,omitempty"`
	SystemPrompt string   `json:"systemPrompt,omitempty"`
	Tools        []string `json:"tools,omitempty"`
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
				BaseURL:        "https://usej-model.tailb0426d.ts.net/v1",
				ReasoningField: "reasoning_content",
			},
			"deepseek": {
				Provider:       "openai",
				API:            "openai-completions",
				Model:          "deepseek-chat",
				BaseURL:        "https://api.deepseek.com",
				APIKey:         "${DEEPSEEK_API_KEY}",
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
		if err := ValidateValue(profile.APIKey); err != nil {
			return fmt.Errorf("profile %q apiKey: %w", name, err)
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
	if err := file.Skills.validate(); err != nil {
		return err
	}
	if err := file.Subagents.validate(file.Profiles); err != nil {
		return err
	}
	return nil
}

func (extensions *Extensions) validate() error {
	if extensions == nil {
		return nil
	}
	if extensions.MCP == nil && extensions.Observers == nil {
		return errors.New("extensions must configure mcp or observers")
	}
	if extensions.Observers != nil {
		if len(extensions.Observers.Include) == 0 {
			return errors.New("extensions.observers.include must be non-empty")
		}
		seen := make(map[string]struct{}, len(extensions.Observers.Include))
		for index, name := range extensions.Observers.Include {
			if name != strings.TrimSpace(name) || !observerNamePattern.MatchString(name) {
				return fmt.Errorf(
					"extensions.observers.include[%d] must be a trimmed package/observer name",
					index,
				)
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("observer %q is selected more than once", name)
			}
			seen[name] = struct{}{}
		}
	}
	if extensions.MCP == nil {
		return nil
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
		} else if server.Headers != nil {
			return fmt.Errorf(
				"MCP server %q headers require url transport",
				name,
			)
		}
		if server.Headers != nil {
			if len(server.Headers) == 0 {
				return fmt.Errorf(
					"MCP server %q headers must be omitted or contain at least one header",
					name,
				)
			}
			seenHeaders := make(map[string]struct{}, len(server.Headers))
			for header, value := range server.Headers {
				if !validHTTPHeaderName(header) {
					return fmt.Errorf("MCP server %q header name %q is invalid", name, header)
				}
				normalized := strings.ToLower(header)
				if _, exists := seenHeaders[normalized]; exists {
					return fmt.Errorf("MCP server %q repeats header name %q", name, header)
				}
				seenHeaders[normalized] = struct{}{}
				if protocolOwnedHTTPHeader(normalized) {
					return fmt.Errorf(
						"MCP server %q header %q is owned by the HTTP or MCP protocol",
						name,
						header,
					)
				}
				if err := ValidateValue(value); err != nil {
					return fmt.Errorf("MCP server %q header %q: %w", name, header, err)
				}
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

// ValidateValue checks the deliberately small configuration value syntax.
// Literal text is preserved and ${NAME} references are resolved only when the
// value is used.
func ValidateValue(value string) error {
	_, err := ResolveValue(value, func(string) (string, bool) {
		return "", true
	})
	return err
}

// ResolveValue substitutes ${NAME} references using lookup. It does not
// implement shell expansion, command execution, default values, or $NAME.
func ResolveValue(
	value string,
	lookup func(string) (string, bool),
) (string, error) {
	if lookup == nil {
		return "", errors.New("value lookup is required")
	}
	var resolved strings.Builder
	for cursor := 0; cursor < len(value); {
		offset := strings.Index(value[cursor:], "${")
		if offset < 0 {
			resolved.WriteString(value[cursor:])
			break
		}
		start := cursor + offset
		resolved.WriteString(value[cursor:start])
		endOffset := strings.IndexByte(value[start+2:], '}')
		if endOffset < 0 {
			return "", errors.New("contains an unterminated ${...} reference")
		}
		end := start + 2 + endOffset
		name := value[start+2 : end]
		if !validEnvironmentReference(name) {
			return "", errors.New(
				"environment reference names must match [A-Za-z_][A-Za-z0-9_]*",
			)
		}
		replacement, exists := lookup(name)
		if !exists {
			return "", fmt.Errorf("environment variable %s is not set", name)
		}
		resolved.WriteString(replacement)
		cursor = end + 1
	}
	return resolved.String(), nil
}

func validEnvironmentReference(name string) bool {
	if name == "" || !isASCIILetter(name[0]) && name[0] != '_' {
		return false
	}
	for index := 1; index < len(name); index++ {
		character := name[index]
		if !isASCIILetter(character) &&
			(character < '0' || character > '9') &&
			character != '_' {
			return false
		}
	}
	return true
}

func isASCIILetter(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z'
}

func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if isASCIILetter(character) ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func protocolOwnedHTTPHeader(normalized string) bool {
	switch normalized {
	case "host",
		"connection",
		"content-length",
		"transfer-encoding",
		"content-type",
		"accept",
		"last-event-id":
		return true
	default:
		return strings.HasPrefix(normalized, "mcp-")
	}
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

func (skills *Skills) validate() error {
	if skills == nil {
		return nil
	}
	if len(skills.Paths) == 0 {
		return errors.New("skills.paths must contain at least one path")
	}
	seen := make(map[string]struct{}, len(skills.Paths))
	for _, path := range skills.Paths {
		if path == "" || path != strings.TrimSpace(path) {
			return fmt.Errorf("skill path %q must be non-empty and trimmed", path)
		}
		if err := ValidateValue(path); err != nil {
			return fmt.Errorf("skill path %q: %w", path, err)
		}
		if _, exists := seen[path]; exists {
			return fmt.Errorf("skills repeats path %q", path)
		}
		seen[path] = struct{}{}
	}
	if skills.Include != nil && len(skills.Include) == 0 {
		return errors.New("skills.include must be omitted or contain at least one skill name")
	}
	selected := make(map[string]struct{}, len(skills.Include))
	for _, name := range skills.Include {
		if name == "" || name != strings.TrimSpace(name) {
			return fmt.Errorf("skill name %q must be non-empty and trimmed", name)
		}
		if _, exists := selected[name]; exists {
			return fmt.Errorf("skills repeats included name %q", name)
		}
		selected[name] = struct{}{}
	}
	return nil
}

func (subagents *Subagents) validate(profiles map[string]Profile) error {
	if subagents == nil {
		return nil
	}
	if len(subagents.Agents) == 0 {
		return errors.New("subagents.agents must configure at least one agent")
	}
	for name, configured := range subagents.Agents {
		if name == "" || name != strings.TrimSpace(name) {
			return fmt.Errorf("subagent name %q must be non-empty and trimmed", name)
		}
		if !validSubagentName(name) {
			return fmt.Errorf(
				"subagent name %q must start with an ASCII letter or digit and contain only letters, digits, '.', '_', and '-'",
				name,
			)
		}
		if configured.Description == "" ||
			configured.Description != strings.TrimSpace(configured.Description) {
			return fmt.Errorf(
				"subagent %q description must be non-empty and trimmed",
				name,
			)
		}
		if len([]rune(name)) > 64 {
			return fmt.Errorf("subagent name %q exceeds 64 characters", name)
		}
		if len([]rune(configured.Description)) > 1024 {
			return fmt.Errorf("subagent %q description exceeds 1024 characters", name)
		}
		if configured.Profile != strings.TrimSpace(configured.Profile) {
			return fmt.Errorf("subagent %q profile must be trimmed", name)
		}
		if configured.Profile != "" {
			if _, exists := profiles[configured.Profile]; !exists {
				return fmt.Errorf(
					"subagent %q profile %q is not defined",
					name,
					configured.Profile,
				)
			}
		}
		if configured.SystemPrompt != strings.TrimSpace(configured.SystemPrompt) {
			return fmt.Errorf("subagent %q systemPrompt must be trimmed", name)
		}
		seenTools := make(map[string]struct{}, len(configured.Tools))
		for _, tool := range configured.Tools {
			if tool == "" || tool != strings.TrimSpace(tool) {
				return fmt.Errorf(
					"subagent %q tool name %q must be non-empty and trimmed",
					name,
					tool,
				)
			}
			if _, exists := seenTools[tool]; exists {
				return fmt.Errorf("subagent %q repeats tool name %q", name, tool)
			}
			seenTools[tool] = struct{}{}
		}
	}
	return nil
}

func validSubagentName(name string) bool {
	if name == "" || len([]rune(name)) > 64 {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		alphanumeric := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9'
		if index == 0 && !alphanumeric {
			return false
		}
		if !alphanumeric && !strings.ContainsRune("._-", rune(character)) {
			return false
		}
	}
	return true
}
