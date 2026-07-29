// Package skills discovers standard Agent Skills and projects progressive
// resource loading through one J-agent Tool.
package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/J/J-agent/agent"
	"gopkg.in/yaml.v3"
)

const (
	maxSkillFileSize = 1 << 20
	skillFileName    = "SKILL.md"
)

var readSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {
			"type": "string",
			"description": "Exact configured skill name"
		},
		"resource": {
			"type": "string",
			"description": "Optional relative file path inside the skill directory; defaults to SKILL.md"
		}
	},
	"required": ["name"],
	"additionalProperties": false
}`)

// Skill is validated Agent Skill metadata plus its local directory.
type Skill struct {
	Name        string
	Description string
	Directory   string
}

// Catalog is an immutable snapshot of explicitly discovered skills.
type Catalog struct {
	skills []Skill
	byName map[string]Skill
}

// Load recursively discovers directories containing SKILL.md beneath the
// supplied roots. Roots are explicit; this package does not search user or
// project configuration locations on its own.
func Load(roots ...string) (*Catalog, error) {
	if len(roots) == 0 {
		return nil, errors.New("at least one skill root is required")
	}
	discovered := make(map[string]Skill)
	for _, configuredRoot := range roots {
		if configuredRoot == "" || configuredRoot != strings.TrimSpace(configuredRoot) {
			return nil, fmt.Errorf("skill root %q must be non-empty and trimmed", configuredRoot)
		}
		root, err := filepath.Abs(configuredRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve skill root %q: %w", configuredRoot, err)
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return nil, fmt.Errorf("resolve skill root %q: %w", configuredRoot, err)
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("inspect skill root %q: %w", configuredRoot, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("skill root %q is not a directory", configuredRoot)
		}
		if err := discoverRoot(root, discovered); err != nil {
			return nil, err
		}
	}
	if len(discovered) == 0 {
		return nil, errors.New("configured roots contain no SKILL.md files")
	}
	names := make([]string, 0, len(discovered))
	for name := range discovered {
		names = append(names, name)
	}
	sort.Strings(names)
	catalog := &Catalog{
		skills: make([]Skill, 0, len(names)),
		byName: make(map[string]Skill, len(names)),
	}
	for _, name := range names {
		skill := discovered[name]
		catalog.skills = append(catalog.skills, skill)
		catalog.byName[name] = skill
	}
	return catalog, nil
}

// Skills returns a copy of the catalog in deterministic name order.
func (catalog *Catalog) Skills() []Skill {
	if catalog == nil {
		return nil
	}
	return append([]Skill(nil), catalog.skills...)
}

// Select returns an immutable catalog containing the exact named skills.
// Selection is explicit and deterministic; unknown or repeated names fail
// rather than silently changing the model-visible capability set.
func (catalog *Catalog) Select(names ...string) (*Catalog, error) {
	if catalog == nil || len(catalog.skills) == 0 {
		return nil, errors.New("skill catalog is empty")
	}
	if len(names) == 0 {
		return nil, errors.New("at least one skill name is required")
	}
	selected := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" || name != strings.TrimSpace(name) {
			return nil, fmt.Errorf("skill name %q must be non-empty and trimmed", name)
		}
		if _, exists := selected[name]; exists {
			return nil, fmt.Errorf("skill name %q is repeated", name)
		}
		if _, exists := catalog.byName[name]; !exists {
			available := make([]string, 0, len(catalog.skills))
			for _, skill := range catalog.skills {
				available = append(available, skill.Name)
			}
			return nil, fmt.Errorf(
				"skill %q is not available; available skills: %q",
				name,
				available,
			)
		}
		selected[name] = struct{}{}
	}
	filtered := &Catalog{
		skills: make([]Skill, 0, len(selected)),
		byName: make(map[string]Skill, len(selected)),
	}
	for _, skill := range catalog.skills {
		if _, exists := selected[skill.Name]; !exists {
			continue
		}
		filtered.skills = append(filtered.skills, skill)
		filtered.byName[skill.Name] = skill
	}
	return filtered, nil
}

// Tool returns the ordinary J-agent Tool used to load skill instructions and
// referenced resources on demand.
func (catalog *Catalog) Tool() (agent.Tool, error) {
	if catalog == nil || len(catalog.skills) == 0 {
		return nil, errors.New("skill catalog is empty")
	}
	return &readTool{
		catalog: catalog,
		spec: agent.ToolSpec{
			Name:        "skill_read",
			Description: catalogDescription(catalog.skills),
			InputSchema: append(json.RawMessage(nil), readSchema...),
		},
	}, nil
}

func discoverRoot(root string, discovered map[string]Skill) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk skill root %q: %w", root, walkErr)
		}
		if !entry.IsDir() {
			return nil
		}
		skillPath := filepath.Join(path, skillFileName)
		info, err := os.Lstat(skillPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect skill file %q: %w", skillPath, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill file %q is not a regular file", skillPath)
		}
		skill, err := loadSkill(path, skillPath)
		if err != nil {
			return err
		}
		if previous, exists := discovered[skill.Name]; exists {
			return fmt.Errorf(
				"duplicate skill name %q in %q and %q",
				skill.Name,
				previous.Directory,
				skill.Directory,
			)
		}
		discovered[skill.Name] = skill
		return filepath.SkipDir
	})
}

func loadSkill(directory, path string) (Skill, error) {
	data, err := readBoundedResource(directory, skillFileName)
	if err != nil {
		return Skill{}, fmt.Errorf("read skill file %q: %w", path, err)
	}
	if !utf8.Valid(data) {
		return Skill{}, fmt.Errorf("read skill file %q: content must be UTF-8 text", path)
	}
	frontmatter, err := parseFrontmatter(data)
	if err != nil {
		return Skill{}, fmt.Errorf("parse skill %q: %w", path, err)
	}
	if err := validateSkillMetadata(frontmatter.Name, frontmatter.Description); err != nil {
		return Skill{}, fmt.Errorf("validate skill %q: %w", path, err)
	}
	if filepath.Base(directory) != frontmatter.Name {
		return Skill{}, fmt.Errorf(
			"validate skill %q: name %q must match parent directory %q",
			path,
			frontmatter.Name,
			filepath.Base(directory),
		)
	}
	return Skill{
		Name:        frontmatter.Name,
		Description: frontmatter.Description,
		Directory:   filepath.Clean(directory),
	}, nil
}

type metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func parseFrontmatter(data []byte) (metadata, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return metadata{}, errors.New("SKILL.md must start with YAML frontmatter")
	}
	remaining := data[len("---\n"):]
	end := bytes.Index(remaining, []byte("\n---"))
	if end < 0 {
		return metadata{}, errors.New("SKILL.md frontmatter is not terminated")
	}
	after := remaining[end+len("\n---"):]
	if len(after) > 0 && after[0] != '\n' && after[0] != '\r' {
		return metadata{}, errors.New("SKILL.md frontmatter terminator must occupy one line")
	}
	var parsed metadata
	if err := yaml.Unmarshal(remaining[:end], &parsed); err != nil {
		return metadata{}, err
	}
	return parsed, nil
}

func validateSkillMetadata(name, description string) error {
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return errors.New("name must contain 1 to 64 characters")
	}
	if name[0] == '-' || name[len(name)-1] == '-' || strings.Contains(name, "--") {
		return errors.New("name must not start or end with '-' or contain '--'")
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '-' {
			continue
		}
		return errors.New("name may contain only lowercase ASCII letters, digits, and hyphens")
	}
	if description == "" || description != strings.TrimSpace(description) ||
		utf8.RuneCountInString(description) > 1024 {
		return errors.New("description must contain 1 to 1024 trimmed characters")
	}
	return nil
}

func readBoundedResource(directory, resource string) ([]byte, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	handle, err := root.Open(resource)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("resource is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(handle, maxSkillFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSkillFileSize {
		return nil, errors.New("resource exceeds 1 MiB")
	}
	return data, nil
}

func catalogDescription(skills []Skill) string {
	var description strings.Builder
	description.WriteString(
		"Read one configured Agent Skill or a referenced resource on demand. " +
			"Read SKILL.md before following a matching skill. Available skills:\n",
	)
	for _, skill := range skills {
		fmt.Fprintf(&description, "- %s: %s\n", skill.Name, skill.Description)
	}
	return strings.TrimSpace(description.String())
}

type readTool struct {
	catalog *Catalog
	spec    agent.ToolSpec
}

func (tool *readTool) Spec() agent.ToolSpec {
	spec := tool.spec
	spec.InputSchema = append(json.RawMessage(nil), spec.InputSchema...)
	return spec
}

func (tool *readTool) Call(ctx context.Context, arguments json.RawMessage) (string, error) {
	if ctx == nil {
		return "", errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var input struct {
		Name     string `json:"name"`
		Resource string `json:"resource"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return "", fmt.Errorf("decode skill_read arguments: %w", err)
	}
	if input.Name == "" || input.Name != strings.TrimSpace(input.Name) {
		return "", errors.New("skill name must be non-empty and trimmed")
	}
	skill, exists := tool.catalog.byName[input.Name]
	if !exists {
		return "", fmt.Errorf("skill %q is not configured", input.Name)
	}
	resource := input.Resource
	if resource == "" {
		resource = skillFileName
	}
	if resource != strings.TrimSpace(resource) || filepath.IsAbs(resource) {
		return "", errors.New("skill resource must be a trimmed relative path")
	}
	resource = filepath.Clean(resource)
	if resource == "." {
		return "", errors.New("skill resource must name a file")
	}
	data, err := readBoundedResource(skill.Directory, resource)
	if err != nil {
		return "", fmt.Errorf(
			"read skill %q resource %q: %w",
			skill.Name,
			resource,
			err,
		)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf(
			"skill %q resource %q must be UTF-8 text",
			skill.Name,
			resource,
		)
	}
	content := strings.ReplaceAll(string(data), "{baseDir}", skill.Directory)
	return fmt.Sprintf(
		"Skill: %s\nDirectory: %s\nResource: %s\n\n%s",
		skill.Name,
		skill.Directory,
		resource,
		content,
	), nil
}

func decodeArguments(data json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
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
