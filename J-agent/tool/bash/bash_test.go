package bash

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestToolExecutesInFixedDirectory(t *testing.T) {
	dir := t.TempDir()
	bashTool, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := bashTool.Call(
		context.Background(),
		json.RawMessage(`{"command":"printf 'out'; printf 'err' >&2; printf '\\n%s' \"$PWD\""}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if output != "outerr\n"+dir {
		t.Fatalf("output=%q", output)
	}
}

func TestToolReportsExitAndPartialOutput(t *testing.T) {
	bashTool, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	output, err := bashTool.Call(context.Background(), json.RawMessage(`{"command":"printf failure; exit 7"}`))
	if err == nil {
		t.Fatal("expected an exit error")
	}
	if !strings.Contains(output, "failure") || !strings.Contains(output, "Command exited with code 7") {
		t.Fatalf("output=%q", output)
	}
}

func TestToolRejectsInvalidArguments(t *testing.T) {
	bashTool, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, arguments := range []string{
		`{}`,
		`{"command":" "}`,
		`{"command":"true","unknown":true}`,
		`{"command":"true","timeout":0}`,
		`{"command":"true"} {}`,
	} {
		if _, err := bashTool.Call(context.Background(), json.RawMessage(arguments)); err == nil {
			t.Fatalf("arguments %q unexpectedly succeeded", arguments)
		}
	}
}

func TestToolBoundsAndSanitizesOutput(t *testing.T) {
	bashTool, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	output, err := bashTool.Call(
		context.Background(),
		json.RawMessage(`{"command":"i=0; while [ $i -lt 2500 ]; do printf '\\033[31mline-%04d\\033[0m\\n' \"$i\"; i=$((i+1)); done"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(output, '\x1b') {
		t.Fatalf("output retained terminal escape: %q", output[:min(len(output), 100)])
	}
	if !strings.Contains(output, "[output truncated:") || !strings.Contains(output, "line-2499") {
		t.Fatalf("output was not tail-truncated:\n%s", output[:min(len(output), 500)])
	}
	if strings.Contains(output, "line-0000") {
		t.Fatal("output retained the beginning instead of the tail")
	}
}

func TestToolTimeoutKillsDescendants(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process-group cancellation is supported on Darwin and Linux")
	}
	dir := t.TempDir()
	bashTool, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "descendant-survived")
	arguments, err := json.Marshal(map[string]any{
		"command": "(sleep 0.5; touch " + shellQuote(marker) + ") & wait",
		"timeout": 0.05,
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := bashTool.Call(context.Background(), arguments)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, output=%q", err, output)
	}
	if !strings.Contains(output, "timed out") {
		t.Fatalf("output=%q", output)
	}
	time.Sleep(650 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant process survived cancellation: %v", err)
	}
}

func TestNewRejectsNonDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path); err == nil {
		t.Fatal("expected an error")
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
