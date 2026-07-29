package packages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryRoundTripUsesPrivatePermissions(t *testing.T) {
	root := testSkillPackage(t, "dev.usej.registry", "1.0.0")
	digest, err := manifestDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state", "packages.json")
	registry := Registry{
		Packages: []Entry{{
			ID:             "dev.usej.registry",
			Version:        "1.0.0",
			Source:         "local:" + root,
			Root:           root,
			ManifestSHA256: digest,
		}},
	}
	if err := WriteRegistry(path, registry); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o, want 600", info.Mode().Perm())
	}
	loaded, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Packages) != 1 || loaded.Packages[0].ID != "dev.usej.registry" {
		t.Fatalf("registry=%+v", loaded)
	}
}

func TestInstalledRejectsManifestDriftUntilUpdate(t *testing.T) {
	root := testSkillPackage(t, "dev.usej.drift", "1.0.0")
	digest, err := manifestDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "packages.json")
	if err := WriteRegistry(path, Registry{Packages: []Entry{{
		ID:             "dev.usej.drift",
		Version:        "1.0.0",
		Source:         "local:" + root,
		Root:           root,
		ManifestSHA256: digest,
	}}}); err != nil {
		t.Fatal(err)
	}
	writeTestManifest(t, root, `{
		"schemaVersion":"j.package.v0.1",
		"id":"dev.usej.drift",
		"version":"1.0.1",
		"contributes":{"skills":["skills"]}
	}`)
	_, err = Installed(path)
	if err == nil || !strings.Contains(err.Error(), "run j package update") {
		t.Fatalf("error=%v", err)
	}
}

func TestMissingRegistryIsEmpty(t *testing.T) {
	registry, err := LoadRegistry(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if registry.SchemaVersion != RegistrySchema || len(registry.Packages) != 0 {
		t.Fatalf("registry=%+v", registry)
	}
}

func testSkillPackage(t *testing.T, id, version string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestManifest(t, root, `{
		"schemaVersion":"j.package.v0.1",
		"id":"`+id+`",
		"version":"`+version+`",
		"contributes":{"skills":["skills"]}
	}`)
	return root
}
