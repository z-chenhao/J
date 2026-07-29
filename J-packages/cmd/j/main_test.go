package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageCLIManagesLocalPackage(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"schemaVersion":"j.package.v0.1",
		"id":"dev.usej.cli",
		"version":"1.0.0",
		"contributes":{"skills":["skills"]}
	}`
	if err := os.WriteFile(
		filepath.Join(root, "j-package.json"),
		[]byte(manifest),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("J_PACKAGES_REGISTRY", filepath.Join(t.TempDir(), "packages.json"))
	t.Setenv("J_PACKAGES_CACHE", filepath.Join(t.TempDir(), "cache"))
	var out bytes.Buffer
	if err := run(
		context.Background(),
		[]string{"package", "add", root},
		&out,
		&out,
	); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Installed dev.usej.cli 1.0.0") {
		t.Fatalf("output=%q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), []string{"package", "doctor"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Validated 1 installed J packages.\n" {
		t.Fatalf("output=%q", out.String())
	}
	out.Reset()
	if err := run(
		context.Background(),
		[]string{"package", "remove", "dev.usej.cli"},
		&out,
		&out,
	); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "cached source was retained") {
		t.Fatalf("output=%q", out.String())
	}
}

func TestVersion(t *testing.T) {
	previous := version
	version = "1.2.3"
	defer func() { version = previous }()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "j 1.2.3\n" {
		t.Fatalf("output=%q", out.String())
	}
}
