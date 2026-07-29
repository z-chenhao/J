package packages

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const RegistrySchema = "j.packages.v0.1"

// Registry is the user-owned installed package set.
type Registry struct {
	SchemaVersion string  `json:"schemaVersion"`
	Packages      []Entry `json:"packages"`
}

// Entry pins one installed package to a concrete local root.
type Entry struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Source         string `json:"source"`
	Root           string `json:"root"`
	Resolved       string `json:"resolved,omitempty"`
	ManifestSHA256 string `json:"manifestSha256"`
}

// DefaultRegistryPath returns ~/.j/packages.json.
func DefaultRegistryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".j", "packages.json"), nil
}

// DefaultCacheRoot returns ~/.j/packages.
func DefaultCacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".j", "packages"), nil
}

// LoadRegistry reads and validates a registry. A missing file is an empty
// registry, allowing hosts to remain unchanged until a package is installed.
func LoadRegistry(path string) (Registry, error) {
	if strings.TrimSpace(path) == "" {
		return Registry{}, errors.New("registry path is required")
	}
	var registry Registry
	if err := decodeStrictFile(path, &registry); err != nil {
		if errors.Is(unwrapPathError(err), os.ErrNotExist) {
			return Registry{SchemaVersion: RegistrySchema, Packages: []Entry{}}, nil
		}
		return Registry{}, err
	}
	if registry.SchemaVersion != RegistrySchema {
		return Registry{}, fmt.Errorf(
			"registry schemaVersion must be %q, got %q",
			RegistrySchema,
			registry.SchemaVersion,
		)
	}
	seen := make(map[string]struct{}, len(registry.Packages))
	for index := range registry.Packages {
		entry := &registry.Packages[index]
		entry.ID = strings.TrimSpace(entry.ID)
		entry.Version = strings.TrimSpace(entry.Version)
		entry.Source = strings.TrimSpace(entry.Source)
		entry.Root = strings.TrimSpace(entry.Root)
		entry.Resolved = strings.TrimSpace(entry.Resolved)
		entry.ManifestSHA256 = strings.TrimSpace(entry.ManifestSHA256)
		if !idPattern.MatchString(entry.ID) {
			return Registry{}, fmt.Errorf("registry package %d has invalid id %q", index, entry.ID)
		}
		if !versionPattern.MatchString(entry.Version) {
			return Registry{}, fmt.Errorf(
				"registry package %q has invalid version %q",
				entry.ID,
				entry.Version,
			)
		}
		if entry.Source == "" || entry.Root == "" || entry.ManifestSHA256 == "" {
			return Registry{}, fmt.Errorf(
				"registry package %q requires source, root, and manifestSha256",
				entry.ID,
			)
		}
		if !filepath.IsAbs(entry.Root) {
			return Registry{}, fmt.Errorf("registry package %q root must be absolute", entry.ID)
		}
		if _, exists := seen[entry.ID]; exists {
			return Registry{}, fmt.Errorf("registry contains duplicate package id %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
	}
	sort.Slice(registry.Packages, func(left, right int) bool {
		return registry.Packages[left].ID < registry.Packages[right].ID
	})
	return registry, nil
}

// WriteRegistry atomically replaces a registry with mode 0600.
func WriteRegistry(path string, registry Registry) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("registry path is required")
	}
	registry.SchemaVersion = RegistrySchema
	sort.Slice(registry.Packages, func(left, right int) bool {
		return registry.Packages[left].ID < registry.Packages[right].ID
	})
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create registry directory: %w", err)
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(parent, ".packages-*.json")
	if err != nil {
		return fmt.Errorf("create registry temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set registry permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write registry: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync registry: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close registry: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace registry: %w", err)
	}
	return nil
}

// Installed validates each pinned manifest and returns packages in ID order.
func Installed(path string) ([]Package, error) {
	registry, err := LoadRegistry(path)
	if err != nil {
		return nil, err
	}
	result := make([]Package, 0, len(registry.Packages))
	for _, entry := range registry.Packages {
		pkg, err := Load(entry.Root)
		if err != nil {
			return nil, fmt.Errorf("load installed package %q: %w", entry.ID, err)
		}
		if pkg.Manifest.ID != entry.ID || pkg.Manifest.Version != entry.Version {
			return nil, fmt.Errorf(
				"installed package %q manifest identity changed; run j package update %s",
				entry.ID,
				entry.ID,
			)
		}
		digest, err := manifestDigest(pkg.Root)
		if err != nil {
			return nil, err
		}
		if digest != entry.ManifestSHA256 {
			return nil, fmt.Errorf(
				"installed package %q manifest changed; run j package update %s",
				entry.ID,
				entry.ID,
			)
		}
		result = append(result, pkg)
	}
	return result, nil
}

func manifestDigest(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, ManifestFilename))
	if err != nil {
		return "", fmt.Errorf("read package manifest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func unwrapPathError(err error) error {
	for err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return pathErr.Err
		}
		err = errors.Unwrap(err)
	}
	return nil
}
