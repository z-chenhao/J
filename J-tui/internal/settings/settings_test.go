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
				"model": "qwen",
				"baseURL": "http://127.0.0.1:8000/v1",
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
	if name != "local" || profile.Model != "qwen" ||
		profile.ReasoningField != "reasoning_content" {
		t.Fatalf("name=%q profile=%#v", name, profile)
	}
	if _, _, err := file.Resolve("missing"); err == nil {
		t.Fatal("missing profile was accepted")
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
	if file.DefaultProfile != "omlx" || len(file.Profiles) != 3 {
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

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
