// Package settings owns J-tui's private on-disk configuration.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxFileSize = 1 << 20

// File is J-tui's experimental configuration file.
type File struct {
	DefaultProfile string             `json:"defaultProfile"`
	Profiles       map[string]Profile `json:"profiles"`
}

// Profile describes one concrete model connection.
type Profile struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	BaseURL         string `json:"baseURL"`
	APIKeyEnv       string `json:"apiKeyEnv,omitempty"`
	ReasoningField  string `json:"reasoningField,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
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
				Model:          "Qwen3.6-35B-A3B-oQ4e-mtp",
				BaseURL:        "http://127.0.0.1:8000/v1",
				APIKeyEnv:      "OMLX_API_KEY",
				ReasoningField: "reasoning_content",
			},
			"deepseek": {
				Provider:       "openai",
				Model:          "deepseek-chat",
				BaseURL:        "https://api.deepseek.com",
				APIKeyEnv:      "DEEPSEEK_API_KEY",
				ReasoningField: "reasoning_content",
			},
			"ollama": {
				Provider:       "openai",
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
	return nil
}
