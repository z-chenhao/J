package packages

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagerAddsUpdatesAndRemovesLocalPackage(t *testing.T) {
	root := testSkillPackage(t, "dev.usej.local", "1.0.0")
	manager, err := NewManager(
		filepath.Join(t.TempDir(), "packages.json"),
		filepath.Join(t.TempDir(), "cache"),
	)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := manager.Add(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ID != "dev.usej.local" || entry.Source != "local:"+resolvedRoot {
		t.Fatalf("entry=%+v", entry)
	}
	if _, err := manager.Add(context.Background(), root); err == nil ||
		!strings.Contains(err.Error(), "already installed") {
		t.Fatalf("duplicate add error=%v", err)
	}

	writeTestManifest(t, root, `{
		"schemaVersion":"j.package.v0.1",
		"id":"dev.usej.local",
		"version":"1.1.0",
		"contributes":{"skills":["skills"]}
	}`)
	updated, err := manager.Update(context.Background(), "dev.usej.local")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 1 || updated[0].Version != "1.1.0" {
		t.Fatalf("updated=%+v", updated)
	}
	removed, err := manager.Remove("dev.usej.local")
	if err != nil {
		t.Fatal(err)
	}
	if removed.ID != "dev.usej.local" {
		t.Fatalf("removed=%+v", removed)
	}
	entries, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestManagerPinsAndUpdatesGitPackageWithoutDeletingOldCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	runGit(t, repository, "config", "user.name", "J Package Test")
	runGit(t, repository, "config", "user.email", "test@example.invalid")
	if err := os.Mkdir(filepath.Join(repository, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(
		t,
		filepath.Join(repository, "skills", "SKILL.md"),
		"---\nname: memory\ndescription: test\n---\n",
		0o600,
	)
	writeTestManifest(t, repository, `{
		"schemaVersion":"j.package.v0.1",
		"id":"dev.usej.git",
		"version":"1.0.0",
		"contributes":{"skills":["skills"]}
	}`)
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "first")

	manager, err := NewManager(
		filepath.Join(t.TempDir(), "packages.json"),
		filepath.Join(t.TempDir(), "cache"),
	)
	if err != nil {
		t.Fatal(err)
	}
	source := "git:" + repository + "@main"
	first, err := manager.Add(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if first.Resolved == "" || first.Root == repository {
		t.Fatalf("first=%+v", first)
	}

	writeTestManifest(t, repository, `{
		"schemaVersion":"j.package.v0.1",
		"id":"dev.usej.git",
		"version":"1.1.0",
		"contributes":{"skills":["skills"]}
	}`)
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "second")
	updated, err := manager.Update(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 1 || updated[0].Resolved == first.Resolved ||
		updated[0].Version != "1.1.0" {
		t.Fatalf("updated=%+v first=%+v", updated, first)
	}
	if _, err := os.Stat(first.Root); err != nil {
		t.Fatalf("old checkout should be retained: %v", err)
	}
}

func TestParseGitSourceRequiresExplicitRef(t *testing.T) {
	if _, _, err := parseGitSource("git:github.com/example/pkg"); err == nil {
		t.Fatal("expected missing ref error")
	}
	repository, reference, err := parseGitSource("git:github.com/example/pkg@v1")
	if err != nil {
		t.Fatal(err)
	}
	if repository != "https://github.com/example/pkg.git" || reference != "v1" {
		t.Fatalf("repository=%q reference=%q", repository, reference)
	}
	for _, source := range []string{
		"git:https://example.invalid/pkg.git@--upload-pack=bad",
		"git:--bad@main",
		"git:https://example.invalid/a b.git@main",
	} {
		if _, _, err := parseGitSource(source); err == nil {
			t.Fatalf("unsafe source %q was accepted", source)
		}
	}
}

func runGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
}
