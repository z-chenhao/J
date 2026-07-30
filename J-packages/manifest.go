// Package packages loads explicit J Package manifests and projects their
// standard contributions for product hosts. It does not modify J-agent or
// define a universal extension interface.
package packages

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	ManifestFilename = "j-package.json"
	ManifestSchema   = "j.package.v0.1"
	maxJSONFileSize  = 1 << 20
)

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Manifest is the complete experimental J Package contract.
type Manifest struct {
	SchemaVersion string        `json:"schemaVersion"`
	ID            string        `json:"id"`
	Version       string        `json:"version"`
	Description   string        `json:"description,omitempty"`
	Contributes   Contributions `json:"contributes"`
}

// Contributions contains the cross-host capability types proven by package
// consumers.
type Contributions struct {
	MCP    []MCPContribution `json:"mcp,omitempty"`
	Skills []string          `json:"skills,omitempty"`
}

// MCPContribution starts one package-owned stdio MCP server. Command is either
// a bare executable resolved through PATH or a relative executable confined to
// the package root. Args are passed directly without shell expansion.
type MCPContribution struct {
	ID      string   `json:"id"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	CWD     string   `json:"cwd,omitempty"`
	Tools   []string `json:"tools,omitempty"`
}

// Package is one validated manifest rooted at a concrete local directory.
type Package struct {
	Root     string
	Manifest Manifest
}

// Load validates the package rooted at root.
func Load(root string) (Package, error) {
	if strings.TrimSpace(root) == "" {
		return Package{}, errors.New("package root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Package{}, fmt.Errorf("resolve package root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return Package{}, fmt.Errorf("resolve package root %q: %w", absolute, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Package{}, fmt.Errorf("stat package root %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return Package{}, fmt.Errorf("package root %q is not a directory", resolved)
	}

	var manifest Manifest
	path := filepath.Join(resolved, ManifestFilename)
	if err := decodeStrictFile(path, &manifest); err != nil {
		return Package{}, err
	}
	if err := validateManifest(resolved, &manifest); err != nil {
		return Package{}, fmt.Errorf("validate %q: %w", path, err)
	}
	return Package{Root: resolved, Manifest: manifest}, nil
}

func validateManifest(root string, manifest *Manifest) error {
	manifest.SchemaVersion = strings.TrimSpace(manifest.SchemaVersion)
	manifest.ID = strings.TrimSpace(manifest.ID)
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.Description = strings.TrimSpace(manifest.Description)
	if manifest.SchemaVersion != ManifestSchema {
		return fmt.Errorf(
			"schemaVersion must be %q, got %q",
			ManifestSchema,
			manifest.SchemaVersion,
		)
	}
	if !idPattern.MatchString(manifest.ID) {
		return fmt.Errorf("id %q must use lowercase dot-separated identifiers", manifest.ID)
	}
	if !versionPattern.MatchString(manifest.Version) {
		return fmt.Errorf("version %q must be semantic version x.y.z", manifest.Version)
	}
	if len(manifest.Contributes.MCP) == 0 &&
		len(manifest.Contributes.Skills) == 0 {
		return errors.New("contributes must contain mcp or skills")
	}

	contributionIDs := make(map[string]struct{}, len(manifest.Contributes.MCP))
	for index := range manifest.Contributes.MCP {
		contribution := &manifest.Contributes.MCP[index]
		contribution.ID = strings.TrimSpace(contribution.ID)
		contribution.Command = strings.TrimSpace(contribution.Command)
		contribution.CWD = strings.TrimSpace(contribution.CWD)
		if !idPattern.MatchString(contribution.ID) {
			return fmt.Errorf(
				"contributes.mcp[%d].id %q must use lowercase dot-separated identifiers",
				index,
				contribution.ID,
			)
		}
		if _, exists := contributionIDs[contribution.ID]; exists {
			return fmt.Errorf("duplicate MCP contribution id %q", contribution.ID)
		}
		contributionIDs[contribution.ID] = struct{}{}
		if contribution.Command == "" {
			return fmt.Errorf("MCP contribution %q command is required", contribution.ID)
		}
		if filepath.IsAbs(contribution.Command) {
			return fmt.Errorf(
				"MCP contribution %q command must be a PATH name or package-relative path",
				contribution.ID,
			)
		}
		if contribution.CWD != "" {
			info, err := resolveWithin(root, contribution.CWD)
			if err != nil {
				return fmt.Errorf("MCP contribution %q cwd: %w", contribution.ID, err)
			}
			if !info.IsDir() {
				return fmt.Errorf("MCP contribution %q cwd is not a directory", contribution.ID)
			}
		}
		if hasPathSeparator(contribution.Command) {
			info, err := resolveWithin(root, contribution.Command)
			if err != nil {
				return fmt.Errorf("MCP contribution %q command: %w", contribution.ID, err)
			}
			if info.IsDir() {
				return fmt.Errorf("MCP contribution %q command is a directory", contribution.ID)
			}
		}
		if err := validateNames("environment", contribution.Env, envNamePattern); err != nil {
			return fmt.Errorf("MCP contribution %q: %w", contribution.ID, err)
		}
		if contribution.Tools != nil {
			if len(contribution.Tools) == 0 {
				return fmt.Errorf(
					"MCP contribution %q tools must be omitted or non-empty",
					contribution.ID,
				)
			}
			if err := validateNames("tool", contribution.Tools, nil); err != nil {
				return fmt.Errorf("MCP contribution %q: %w", contribution.ID, err)
			}
		}
	}

	skillPaths := make(map[string]struct{}, len(manifest.Contributes.Skills))
	for index, configured := range manifest.Contributes.Skills {
		configured = strings.TrimSpace(configured)
		if configured == "" {
			return fmt.Errorf("contributes.skills[%d] is empty", index)
		}
		if filepath.IsAbs(configured) {
			return fmt.Errorf("contributes.skills[%d] must be package-relative", index)
		}
		info, err := resolveWithin(root, configured)
		if err != nil {
			return fmt.Errorf("contributes.skills[%d]: %w", index, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("contributes.skills[%d] is not a directory", index)
		}
		clean := filepath.Clean(configured)
		if _, exists := skillPaths[clean]; exists {
			return fmt.Errorf("duplicate skill path %q", clean)
		}
		skillPaths[clean] = struct{}{}
		manifest.Contributes.Skills[index] = clean
	}
	return nil
}

func validateNames(kind string, values []string, pattern *regexp.Regexp) error {
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || trimmed != value {
			return fmt.Errorf("%s name at index %d must be non-empty and trimmed", kind, index)
		}
		if pattern != nil && !pattern.MatchString(trimmed) {
			return fmt.Errorf("invalid %s name %q", kind, trimmed)
		}
		if _, exists := seen[trimmed]; exists {
			return fmt.Errorf("duplicate %s name %q", kind, trimmed)
		}
		seen[trimmed] = struct{}{}
	}
	return nil
}

func resolveWithin(root, relative string) (os.FileInfo, error) {
	clean := filepath.Clean(relative)
	if clean == "." && relative != "." {
		return nil, errors.New("path is empty")
	}
	if filepath.IsAbs(clean) {
		return nil, errors.New("path must be relative")
	}
	candidate := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return nil, err
	}
	inside, err := filepath.Rel(root, resolved)
	if err != nil {
		return nil, err
	}
	if inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return nil, errors.New("path escapes package root")
	}
	return os.Stat(resolved)
}

func resolvedPathWithin(root, relative string) (string, error) {
	if _, err := resolveWithin(root, relative); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.Clean(relative)))
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func hasPathSeparator(value string) bool {
	return strings.ContainsRune(value, filepath.Separator) ||
		filepath.Separator != '/' && strings.ContainsRune(value, '/')
}

func decodeStrictFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxJSONFileSize+1))
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	if len(data) > maxJSONFileSize {
		return fmt.Errorf("%q exceeds %d bytes", path, maxJSONFileSize)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %q: %w", path, err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return fmt.Errorf("decode %q: %w", path, err)
	}
	return nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
