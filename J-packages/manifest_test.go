package packages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAcceptsMCPAndSkillsPackage(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "server"), "#!/bin/sh\n", 0o755)
	if err := os.Mkdir(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestManifest(t, root, `{
		"schemaVersion":"j.package.v0.1",
		"id":"dev.usej.memory",
		"version":"1.2.3",
		"description":"Memory tools and skills.",
		"contributes":{
			"mcp":[{
				"id":"memory",
				"command":"./server",
				"env":["MEMORY_PATH"],
				"tools":["memory_store","memory_search"]
			}],
			"skills":["skills"]
		}
	}`)

	pkg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Manifest.ID != "dev.usej.memory" ||
		pkg.Manifest.Contributes.MCP[0].ID != "memory" ||
		pkg.Manifest.Contributes.Skills[0] != "skills" {
		t.Fatalf("package=%+v", pkg)
	}
}

func TestLoadRejectsUnknownFieldsAndEmptyContributions(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name: "unknown",
			manifest: `{
				"schemaVersion":"j.package.v0.1",
				"id":"dev.usej.test",
				"version":"1.0.0",
				"contributes":{"skills":["skills"]},
				"enabled":true
			}`,
			want: "unknown field",
		},
		{
			name: "empty",
			manifest: `{
				"schemaVersion":"j.package.v0.1",
				"id":"dev.usej.test",
				"version":"1.0.0",
				"contributes":{}
			}`,
			want: "must contain",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "skills"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestManifest(t, root, test.manifest)
			_, err := Load(root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsPackagePathEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "skills")); err != nil {
		t.Fatal(err)
	}
	writeTestManifest(t, root, `{
		"schemaVersion":"j.package.v0.1",
		"id":"dev.usej.escape",
		"version":"1.0.0",
		"contributes":{"skills":["skills"]}
	}`)
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "escapes package root") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadRejectsShellAndAbsoluteCommandSurfaces(t *testing.T) {
	root := t.TempDir()
	writeTestManifest(t, root, `{
		"schemaVersion":"j.package.v0.1",
		"id":"dev.usej.command",
		"version":"1.0.0",
		"contributes":{"mcp":[{
			"id":"bad",
			"command":"/bin/sh",
			"args":["-c","echo unsafe"]
		}]}
	}`)
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "must be a PATH name or package-relative") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadAcceptsVersionTwoObserverPackage(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "observer"), "#!/bin/sh\n", 0o755)
	writeTestManifest(t, root, `{
		"schemaVersion":"j.package.v0.2",
		"id":"dev.usej.observe",
		"version":"1.0.0",
		"contributes":{"observers":[{
			"id":"trace",
			"command":"./observer",
			"env":["TRACE_PATH"],
			"permissions":["agent.events","model.frames"]
		}]}
	}`)

	pkg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	observer := pkg.Manifest.Contributes.Observers[0]
	if observer.ID != "trace" || len(observer.Permissions) != 2 {
		t.Fatalf("observer=%+v", observer)
	}
}

func TestLoadRejectsObserverInVersionOneAndUnknownPermission(t *testing.T) {
	tests := []struct {
		name    string
		schema  string
		permit  string
		wantErr string
	}{
		{"version one", "j.package.v0.1", "agent.events", "require schemaVersion"},
		{"permission", "j.package.v0.2", "agent.mutate", "unsupported permission"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "observer"), "#!/bin/sh\n", 0o755)
			writeTestManifest(t, root, `{
				"schemaVersion":"`+test.schema+`",
				"id":"dev.usej.observe",
				"version":"1.0.0",
				"contributes":{"observers":[{
					"id":"trace",
					"command":"./observer",
					"permissions":["`+test.permit+`"]
				}]}
			}`)
			_, err := Load(root)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error=%v, want %q", err, test.wantErr)
			}
		})
	}
}

func writeTestManifest(t *testing.T, root, content string) {
	t.Helper()
	writeTestFile(t, filepath.Join(root, ManifestFilename), content+"\n", 0o600)
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
