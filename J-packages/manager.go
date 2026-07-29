package packages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Manager installs explicit local or pinned Git packages into one registry.
type Manager struct {
	RegistryPath string
	CacheRoot    string
}

// NewManager constructs a manager using defaults for omitted paths.
func NewManager(registryPath, cacheRoot string) (*Manager, error) {
	var err error
	if strings.TrimSpace(registryPath) == "" {
		registryPath, err = DefaultRegistryPath()
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(cacheRoot) == "" {
		cacheRoot, err = DefaultCacheRoot()
		if err != nil {
			return nil, err
		}
	}
	registryPath, err = filepath.Abs(registryPath)
	if err != nil {
		return nil, fmt.Errorf("resolve registry path: %w", err)
	}
	cacheRoot, err = filepath.Abs(cacheRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve package cache: %w", err)
	}
	return &Manager{RegistryPath: registryPath, CacheRoot: cacheRoot}, nil
}

// Add validates and registers one local path or pinned git:<source>@<ref>.
func (manager *Manager) Add(ctx context.Context, source string) (Entry, error) {
	if ctx == nil {
		return Entry{}, errors.New("context is required")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return Entry{}, errors.New("package source is required")
	}
	entry, err := manager.resolve(ctx, source)
	if err != nil {
		return Entry{}, err
	}
	registry, err := LoadRegistry(manager.RegistryPath)
	if err != nil {
		return Entry{}, err
	}
	for _, existing := range registry.Packages {
		if existing.ID == entry.ID {
			return Entry{}, fmt.Errorf(
				"package %q is already installed; run j package update %s",
				entry.ID,
				entry.ID,
			)
		}
	}
	registry.Packages = append(registry.Packages, entry)
	if err := WriteRegistry(manager.RegistryPath, registry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// Update refreshes one named package, or all packages when id is empty.
func (manager *Manager) Update(ctx context.Context, id string) ([]Entry, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	id = strings.TrimSpace(id)
	registry, err := LoadRegistry(manager.RegistryPath)
	if err != nil {
		return nil, err
	}
	found := false
	updated := make([]Entry, 0, len(registry.Packages))
	for index, current := range registry.Packages {
		if id != "" && current.ID != id {
			continue
		}
		found = true
		next, err := manager.resolve(ctx, current.Source)
		if err != nil {
			return nil, fmt.Errorf("update package %q: %w", current.ID, err)
		}
		if next.ID != current.ID {
			return nil, fmt.Errorf(
				"package source %q changed id from %q to %q",
				current.Source,
				current.ID,
				next.ID,
			)
		}
		registry.Packages[index] = next
		updated = append(updated, next)
	}
	if id != "" && !found {
		return nil, fmt.Errorf("package %q is not installed", id)
	}
	if err := WriteRegistry(manager.RegistryPath, registry); err != nil {
		return nil, err
	}
	sort.Slice(updated, func(left, right int) bool { return updated[left].ID < updated[right].ID })
	return updated, nil
}

// Remove unregisters a package without destructively deleting cached source.
func (manager *Manager) Remove(id string) (Entry, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Entry{}, errors.New("package id is required")
	}
	registry, err := LoadRegistry(manager.RegistryPath)
	if err != nil {
		return Entry{}, err
	}
	for index, entry := range registry.Packages {
		if entry.ID != id {
			continue
		}
		registry.Packages = append(registry.Packages[:index], registry.Packages[index+1:]...)
		if err := WriteRegistry(manager.RegistryPath, registry); err != nil {
			return Entry{}, err
		}
		return entry, nil
	}
	return Entry{}, fmt.Errorf("package %q is not installed", id)
}

// List returns the validated registry entries.
func (manager *Manager) List() ([]Entry, error) {
	registry, err := LoadRegistry(manager.RegistryPath)
	if err != nil {
		return nil, err
	}
	return append([]Entry(nil), registry.Packages...), nil
}

// Check validates one local package without installing it.
func (manager *Manager) Check(path string) (Package, error) {
	if strings.HasPrefix(path, "local:") {
		path = strings.TrimPrefix(path, "local:")
	}
	return Load(path)
}

// Doctor validates the registry and every installed manifest.
func (manager *Manager) Doctor() ([]Package, error) {
	return Installed(manager.RegistryPath)
}

func (manager *Manager) resolve(ctx context.Context, source string) (Entry, error) {
	if strings.HasPrefix(source, "git:") {
		return manager.resolveGit(ctx, source)
	}
	path := strings.TrimPrefix(source, "local:")
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Entry{}, fmt.Errorf("resolve local package source: %w", err)
	}
	pkg, err := Load(absolute)
	if err != nil {
		return Entry{}, err
	}
	digest, err := manifestDigest(pkg.Root)
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		ID:             pkg.Manifest.ID,
		Version:        pkg.Manifest.Version,
		Source:         "local:" + pkg.Root,
		Root:           pkg.Root,
		ManifestSHA256: digest,
	}, nil
}

func (manager *Manager) resolveGit(ctx context.Context, source string) (Entry, error) {
	repository, reference, err := parseGitSource(source)
	if err != nil {
		return Entry{}, err
	}
	if _, err := exec.LookPath("git"); err != nil {
		return Entry{}, errors.New("git is required to install Git packages")
	}
	if err := os.MkdirAll(manager.CacheRoot, 0o700); err != nil {
		return Entry{}, fmt.Errorf("create package cache: %w", err)
	}
	temporary, err := os.MkdirTemp(manager.CacheRoot, ".install-*")
	if err != nil {
		return Entry{}, fmt.Errorf("create package checkout: %w", err)
	}
	defer os.RemoveAll(temporary)
	commands := [][]string{
		{"-c", "init.templateDir=", "init", "--quiet", temporary},
		{
			"-c", "core.hooksPath=/dev/null",
			"-C", temporary,
			"remote", "add", "--", "origin", repository,
		},
		{
			"-c", "core.hooksPath=/dev/null",
			"-C", temporary,
			"fetch", "--quiet", "--depth", "1", "--", "origin", reference,
		},
		{
			"-c", "core.hooksPath=/dev/null",
			"-C", temporary,
			"checkout", "--quiet", "--detach", "FETCH_HEAD",
		},
	}
	for _, arguments := range commands {
		command := exec.CommandContext(ctx, "git", arguments...)
		output, commandErr := command.CombinedOutput()
		if commandErr != nil {
			return Entry{}, fmt.Errorf(
				"git %s: %w: %s",
				strings.Join(arguments, " "),
				commandErr,
				strings.TrimSpace(string(output)),
			)
		}
	}
	resolvedOutput, err := exec.CommandContext(
		ctx,
		"git",
		"-C",
		temporary,
		"rev-parse",
		"HEAD",
	).Output()
	if err != nil {
		return Entry{}, fmt.Errorf("resolve Git package commit: %w", err)
	}
	resolved := strings.TrimSpace(string(resolvedOutput))
	pkg, err := Load(temporary)
	if err != nil {
		return Entry{}, err
	}
	finalRoot := filepath.Join(manager.CacheRoot, pkg.Manifest.ID, resolved)
	if err := os.MkdirAll(filepath.Dir(finalRoot), 0o700); err != nil {
		return Entry{}, fmt.Errorf("create package version directory: %w", err)
	}
	if _, err := os.Stat(finalRoot); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(temporary, finalRoot); err != nil {
			return Entry{}, fmt.Errorf("install package checkout: %w", err)
		}
	} else if err != nil {
		return Entry{}, fmt.Errorf("stat package checkout: %w", err)
	}
	installed, err := Load(finalRoot)
	if err != nil {
		return Entry{}, err
	}
	digest, err := manifestDigest(installed.Root)
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		ID:             installed.Manifest.ID,
		Version:        installed.Manifest.Version,
		Source:         source,
		Root:           installed.Root,
		Resolved:       resolved,
		ManifestSHA256: digest,
	}, nil
}

func parseGitSource(source string) (string, string, error) {
	body := strings.TrimPrefix(source, "git:")
	separator := strings.LastIndex(body, "@")
	if separator <= 0 || separator == len(body)-1 {
		return "", "", errors.New("Git package source must be git:<repository>@<ref>")
	}
	repository := body[:separator]
	reference := body[separator+1:]
	if strings.ContainsAny(repository, " \t\r\n") {
		return "", "", errors.New("Git package repository contains whitespace")
	}
	if strings.ContainsAny(reference, " \t\r\n") || strings.HasPrefix(reference, "-") {
		return "", "", errors.New("Git package ref contains whitespace or starts with dash")
	}
	if strings.HasPrefix(repository, "github.com/") {
		repository = "https://" + repository
	}
	if strings.HasPrefix(repository, "https://github.com/") &&
		!strings.HasSuffix(repository, ".git") {
		repository += ".git"
	}
	if repository == "" || strings.HasPrefix(repository, "-") {
		return "", "", errors.New("Git package repository is required")
	}
	return repository, reference, nil
}
