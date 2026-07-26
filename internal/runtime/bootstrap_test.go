package runtime

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunCLIOneShot(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	if err := RunCLI(context.Background(), strings.NewReader(""), &out, &errOut, "hello"); err != nil {
		t.Fatalf("RunCLI() error: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "J received: hello" {
		t.Fatalf("output=%q", got)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr=%q", errOut.String())
	}
}

func TestRunRPCState(t *testing.T) {
	var out bytes.Buffer
	input := strings.NewReader(`{"id":"1","type":"state"}` + "\n")
	if err := RunRPC(input, &out); err != nil {
		t.Fatalf("RunRPC() error: %v", err)
	}
	if !strings.Contains(out.String(), `"protocol":"j-core"`) {
		t.Fatalf("unexpected output: %s", out.String())
	}
}
