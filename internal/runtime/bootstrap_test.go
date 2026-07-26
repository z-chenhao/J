package runtime

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/J-agent/agent"
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
	if err := RunRPC(context.Background(), testAgent(t), input, &out); err != nil {
		t.Fatalf("RunRPC() error: %v", err)
	}
	if !strings.Contains(out.String(), `"protocol":"j-agent"`) {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestRunRPCPropagatesCancellationToActiveTask(t *testing.T) {
	model := &gatedModel{started: make(chan struct{}), release: make(chan struct{})}
	runner, err := agent.New(model)
	if err != nil {
		t.Fatalf("agent.New() error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- RunRPC(
			ctx,
			runner,
			strings.NewReader(`{"id":"1","type":"submit","message":"hello"}`+"\n"),
			&out,
		)
	}()
	select {
	case <-model.started:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunRPC() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunRPC() did not settle after cancellation")
	}
	if !strings.Contains(out.String(), `"type":"task.failed"`) ||
		!strings.Contains(out.String(), context.Canceled.Error()) {
		t.Fatalf("canceled task output=%s", out.String())
	}
}
