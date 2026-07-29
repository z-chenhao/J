package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndReadSkillResources(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "zeta", "Use for zeta work.", "# Zeta\nRun {baseDir}/scripts/run.sh.\n")
	writeSkill(t, root, "alpha", "Use for alpha work.", "# Alpha\nRead references/guide.md.\n")
	writeFile(t, filepath.Join(root, "alpha", "references", "guide.md"), "alpha guide")

	catalog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	found := catalog.Skills()
	if len(found) != 2 || found[0].Name != "alpha" || found[1].Name != "zeta" {
		t.Fatalf("skills=%#v", found)
	}
	found[0].Name = "mutated"
	if catalog.Skills()[0].Name != "alpha" {
		t.Fatal("Skills returned shared state")
	}

	tool, err := catalog.Tool()
	if err != nil {
		t.Fatal(err)
	}
	spec := tool.Spec()
	if spec.Name != "skill_read" ||
		!strings.Contains(spec.Description, "alpha: Use for alpha work.") ||
		!strings.Contains(spec.Description, "zeta: Use for zeta work.") {
		t.Fatalf("spec=%#v", spec)
	}
	output, err := tool.Call(context.Background(), json.RawMessage(`{"name":"zeta"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, filepath.Join(root, "zeta", "scripts", "run.sh")) ||
		!strings.Contains(output, "Resource: SKILL.md") {
		t.Fatalf("output=%q", output)
	}
	output, err = tool.Call(
		context.Background(),
		json.RawMessage(`{"name":"alpha","resource":"references/guide.md"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(output, "alpha guide") {
		t.Fatalf("output=%q", output)
	}
}

func TestLoadValidatesAgentSkillsStandard(t *testing.T) {
	tests := map[string]struct {
		directory   string
		frontmatter string
	}{
		"missing frontmatter": {"alpha", "# Alpha"},
		"invalid name":        {"Bad", "---\nname: Bad\ndescription: Useful.\n---\n"},
		"consecutive hyphens": {"a--b", "---\nname: a--b\ndescription: Useful.\n---\n"},
		"name mismatch":       {"directory", "---\nname: other\ndescription: Useful.\n---\n"},
		"blank description":   {"alpha", "---\nname: alpha\ndescription: \"\"\n---\n"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, test.directory, skillFileName), test.frontmatter)
			if _, err := Load(root); err == nil {
				t.Fatal("invalid skill was accepted")
			}
		})
	}
}

func TestLoadRejectsDuplicateNames(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeSkill(t, first, "same", "First.", "# First")
	writeSkill(t, second, "same", "Second.", "# Second")
	if _, err := Load(first, second); err == nil ||
		!strings.Contains(err.Error(), "duplicate skill name") {
		t.Fatalf("error=%v", err)
	}
}

func TestSelectReturnsExactDeterministicCatalog(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "zeta", "Zeta.", "# Zeta")
	writeSkill(t, root, "alpha", "Alpha.", "# Alpha")
	writeSkill(t, root, "beta", "Beta.", "# Beta")
	catalog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := catalog.Select("zeta", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	found := selected.Skills()
	if len(found) != 2 || found[0].Name != "alpha" || found[1].Name != "zeta" {
		t.Fatalf("skills=%#v", found)
	}
	if len(catalog.Skills()) != 3 {
		t.Fatal("Select mutated the source catalog")
	}
	tool, err := selected.Tool()
	if err != nil {
		t.Fatal(err)
	}
	if description := tool.Spec().Description; strings.Contains(description, "beta:") {
		t.Fatalf("description=%q", description)
	}
}

func TestSelectRejectsInvalidNames(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha", "Alpha.", "# Alpha")
	catalog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, selected := range map[string][]string{
		"empty":     {},
		"blank":     {" "},
		"duplicate": {"alpha", "alpha"},
		"unknown":   {"missing"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := catalog.Select(selected...); err == nil {
				t.Fatal("invalid selection was accepted")
			}
		})
	}
}

func TestLoadRejectsEmptyAndInvalidRoots(t *testing.T) {
	if _, err := Load(); err == nil {
		t.Fatal("empty roots were accepted")
	}
	empty := t.TempDir()
	if _, err := Load(empty); err == nil {
		t.Fatal("root without skills was accepted")
	}
	path := filepath.Join(t.TempDir(), "file")
	writeFile(t, path, "not a directory")
	if _, err := Load(path); err == nil {
		t.Fatal("file root was accepted")
	}
}

func TestReadRejectsTraversalUnknownFieldsAndEscapingSymlinks(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha", "Useful.", "# Alpha")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, "secret")
	if err := os.Symlink(outside, filepath.Join(root, "alpha", "escape")); err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	tool, err := catalog.Tool()
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"traversal":     `{"name":"alpha","resource":"../outside"}`,
		"escaping link": `{"name":"alpha","resource":"escape"}`,
		"unknown field": `{"name":"alpha","extra":true}`,
		"unknown skill": `{"name":"missing"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tool.Call(context.Background(), json.RawMessage(input)); err == nil {
				t.Fatal("invalid read was accepted")
			}
		})
	}
}

func TestLoadRejectsOversizedSkillFile(t *testing.T) {
	root := t.TempDir()
	content := "---\nname: huge\ndescription: Huge.\n---\n" +
		strings.Repeat("x", maxSkillFileSize)
	writeFile(t, filepath.Join(root, "huge", skillFileName), content)
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadRejectsEscapingSkillFileSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), skillFileName)
	writeFile(t, outside, "---\nname: alpha\ndescription: Outside.\n---\n")
	directory := filepath.Join(root, "alpha")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, skillFileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("escaping SKILL.md symlink was accepted")
	}
}

func TestReadRejectsBinaryResource(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha", "Useful.", "# Alpha")
	path := filepath.Join(root, "alpha", "assets", "binary")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{0xff, 0xfe}, 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	tool, err := catalog.Tool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Call(
		context.Background(),
		json.RawMessage(`{"name":"alpha","resource":"assets/binary"}`),
	); err == nil || !strings.Contains(err.Error(), "UTF-8 text") {
		t.Fatalf("error=%v", err)
	}
}

func writeSkill(t *testing.T, root, name, description, body string) {
	t.Helper()
	writeFile(
		t,
		filepath.Join(root, name, skillFileName),
		"---\nname: "+name+"\ndescription: "+description+"\n---\n\n"+body,
	)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
