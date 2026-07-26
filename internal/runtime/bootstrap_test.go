package runtime

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/z-chenhao/J/agent"
)

func testAgent(t *testing.T) *agent.Agent {
	t.Helper()
	runner, err := agent.New(fixedModel{content: "done"})
	if err != nil {
		t.Fatalf("agent.New() error: %v", err)
	}
	return runner
}

type cliFailingWriter struct{}

func (cliFailingWriter) Write([]byte) (int, error) {
	return 0, errors.New("closed")
}

func TestRunCLIReportsOutputFailure(t *testing.T) {
	err := RunCLI(
		context.Background(),
		testAgent(t),
		strings.NewReader(""),
		cliFailingWriter{},
		nil,
		"hello",
	)
	if err == nil || !strings.Contains(err.Error(), "write output") {
		t.Fatalf("error=%v", err)
	}
}

func TestRunCLIOneShot(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	if err := RunCLI(
		context.Background(),
		testAgent(t),
		strings.NewReader(""),
		&out,
		&errOut,
		"hello",
	); err != nil {
		t.Fatalf("RunCLI() error: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "done" {
		t.Fatalf("output=%q", got)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr=%q", errOut.String())
	}
}

func TestRunRPCState(t *testing.T) {
	var out bytes.Buffer
	input := strings.NewReader(`{"id":"1","type":"state"}` + "\n")
	if err := RunRPC(testAgent(t), input, &out); err != nil {
		t.Fatalf("RunRPC() error: %v", err)
	}
	if !strings.Contains(out.String(), `"protocol":"j-core"`) {
		t.Fatalf("unexpected output: %s", out.String())
	}
}
