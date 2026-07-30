package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreatePersistsMode0600Token(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "token")
	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 32 || first != second {
		t.Fatalf("first=%q second=%q", first, second)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestBearerAndEqual(t *testing.T) {
	if Bearer("Bearer secret") != "secret" || Bearer("Basic secret") != "" {
		t.Fatal("unexpected bearer parsing")
	}
	if !Equal("same", "same") || Equal("same", "different") || Equal("", "") {
		t.Fatal("unexpected constant-time comparison result")
	}
}
