package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/J/agent"
)

type fixedModel struct {
	content string
}

func (m fixedModel) Complete(
	_ context.Context,
	_ agent.ModelRequest,
	emit func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	if emit != nil {
		emit(agent.ModelDelta{Type: agent.DeltaText, Index: 0, Delta: m.content})
	}
	return testResponse(m.content), nil
}

type gatedModel struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *gatedModel) Complete(
	ctx context.Context,
	_ agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	m.once.Do(func() { close(m.started) })
	select {
	case <-ctx.Done():
		return agent.ModelResponse{}, ctx.Err()
	case <-m.release:
		return testResponse("released"), nil
	}
}

func testResponse(content string) agent.ModelResponse {
	return agent.ModelResponse{
		Message:    agent.TextMessage(agent.RoleAssistant, content),
		Provider:   "test",
		Model:      "test-model",
		StopReason: agent.StopReasonStop,
		Usage: &agent.Usage{
			InputTokens:  1,
			OutputTokens: 1,
			TotalTokens:  2,
		},
	}
}

func newTestRuntime(t *testing.T, model agent.Model, out *bytes.Buffer) *Runtime {
	t.Helper()
	runner, err := agent.New(model)
	if err != nil {
		t.Fatalf("agent.New() error: %v", err)
	}
	rt, err := New(runner, out)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return rt
}

func decodeLines(t *testing.T, output string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	decoded := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		decoded = append(decoded, value)
	}
	return decoded
}

func TestRunLoopSubmitSettlesWithoutDeadlock(t *testing.T) {
	var out bytes.Buffer
	rt := newTestRuntime(t, fixedModel{content: "done"}, &out)
	input := strings.NewReader(`{"id":"1","type":"submit","message":"hello"}` + "\n")

	done := make(chan error, 1)
	go func() {
		done <- RunLoop(input, rt)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop() error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunLoop() did not settle")
	}

	records := decodeLines(t, out.String())
	if len(records) < 2 {
		t.Fatalf("records=%v", records)
	}
	if records[0]["type"] != "response" || records[0]["success"] != true {
		t.Fatalf("first record is not accepted response: %#v", records[0])
	}

	terminalCount := 0
	var lastSequence float64
	messageIDs := make(map[string]bool)
	for _, record := range records[1:] {
		if sequence, ok := record["sequence"].(float64); ok {
			if sequence <= lastSequence {
				t.Fatalf("non-monotonic sequence: %v after %v", sequence, lastSequence)
			}
			lastSequence = sequence
		}
		if record["type"] == "task.completed" {
			terminalCount++
		}
		if record["type"] == string(agent.EventMessageStarted) {
			id, _ := record["messageId"].(string)
			if id == "" {
				t.Fatal("message.started is missing messageId")
			}
			if messageIDs[id] {
				t.Fatalf("duplicate messageId %q", id)
			}
			messageIDs[id] = true
		}
	}
	if terminalCount != 1 {
		t.Fatalf("terminal task events=%d, want 1: %#v", terminalCount, records)
	}
}

func TestQueueIsFIFOAndQueuedTaskCanBeCanceled(t *testing.T) {
	var out bytes.Buffer
	model := &gatedModel{started: make(chan struct{}), release: make(chan struct{})}
	rt := newTestRuntime(t, model, &out)

	rt.Handle(command{ID: "1", Type: commandSubmit, Message: "first"})
	select {
	case <-model.started:
	case <-time.After(time.Second):
		t.Fatal("first task did not start")
	}
	rt.Handle(command{ID: "2", Type: commandSubmit, Message: "second"})
	rt.Handle(command{ID: "3", Type: commandCancel, TaskID: "task-000002"})
	close(model.release)
	rt.Wait()

	first, ok := rt.task("task-000001")
	if !ok || first.Status != taskCompleted {
		t.Fatalf("first task=%#v, ok=%v", first, ok)
	}
	second, ok := rt.task("task-000002")
	if !ok || second.Status != taskCanceled {
		t.Fatalf("second task=%#v, ok=%v", second, ok)
	}
	if len(rt.runner.History()) != 2 {
		t.Fatalf("canceled queued task reached model: history=%#v", rt.runner.History())
	}
}

func TestCancelActiveTaskProducesOneTerminalEvent(t *testing.T) {
	var out bytes.Buffer
	model := &gatedModel{started: make(chan struct{}), release: make(chan struct{})}
	rt := newTestRuntime(t, model, &out)
	rt.Handle(command{ID: "1", Type: commandSubmit, Message: "first"})
	select {
	case <-model.started:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}
	rt.Handle(command{ID: "2", Type: commandCancel, TaskID: "task-000001"})
	rt.Wait()

	records := decodeLines(t, out.String())
	terminalCount := 0
	for _, record := range records {
		if record["type"] == "task.canceled" {
			terminalCount++
		}
	}
	if terminalCount != 1 {
		t.Fatalf("task.canceled events=%d, want 1: %#v", terminalCount, records)
	}
}

func TestStateContainsOnlyObservedRuntimeFacts(t *testing.T) {
	var out bytes.Buffer
	rt := newTestRuntime(t, fixedModel{content: "done"}, &out)
	state := rt.state()
	if state.Running || state.QueuedTasks != 0 || state.MessageCount != 0 {
		t.Fatalf("unexpected initial state: %#v", state)
	}

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	for _, forbidden := range []string{"model", "sessionFile", "isStreaming", "protocols", "thinkingLevel"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("state contains placeholder field %q: %s", forbidden, encoded)
		}
	}
}

func TestHandleReadCommandsAndIdleReset(t *testing.T) {
	var out bytes.Buffer
	rt := newTestRuntime(t, fixedModel{content: "done"}, &out)
	rt.Handle(command{ID: "1", Type: commandSubmit, Message: "hello"})
	rt.Wait()
	rt.Handle(command{ID: "2", Type: commandTask, TaskID: "task-000001"})
	rt.Handle(command{ID: "3", Type: commandMessages})
	rt.Handle(command{ID: "4", Type: commandState})
	rt.Handle(command{ID: "5", Type: commandReset})

	records := decodeLines(t, out.String())
	for _, id := range []string{"2", "3", "4", "5"} {
		found := false
		for _, record := range records {
			if record["id"] == id && record["success"] == true {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing successful response %s: %#v", id, records)
		}
	}
	if len(rt.runner.History()) != 0 {
		t.Fatal("reset did not clear history")
	}
}

func TestTaskSnapshotReportsRunDiagnostics(t *testing.T) {
	var out bytes.Buffer
	rt := newTestRuntime(t, fixedModel{content: "done"}, &out)
	rt.Handle(command{ID: "1", Type: commandSubmit, Message: "hello"})
	rt.Wait()

	task, ok := rt.task("task-000001")
	if !ok {
		t.Fatal("task not found")
	}
	if task.StartedAt == "" || task.CompletedAt == "" || task.Turns == nil || *task.Turns != 1 ||
		task.Usage == nil || task.Usage.TotalTokens != 2 || task.FirstDeltaMS == nil {
		t.Fatalf("task diagnostics=%#v", task)
	}
}

func TestHandleRejectsInvalidCommands(t *testing.T) {
	var out bytes.Buffer
	rt := newTestRuntime(t, fixedModel{content: "done"}, &out)
	rt.Handle(command{ID: "1", Type: commandSubmit})
	rt.Handle(command{ID: "2", Type: commandCancel})
	rt.Handle(command{ID: "3", Type: commandTask, TaskID: "task-999999"})
	rt.Handle(command{ID: "4", Type: "unknown"})

	records := decodeLines(t, out.String())
	codes := make(map[string]bool)
	for _, record := range records {
		errorObject, _ := record["error"].(map[string]any)
		code, _ := errorObject["code"].(string)
		codes[code] = true
	}
	for _, code := range []string{codeMessageNeeded, codeTaskNeeded, codeTaskNotFound, codeInvalidCommand} {
		if !codes[code] {
			t.Fatalf("missing error code %q: %#v", code, records)
		}
	}
}

func TestResetIsRejectedWhileTaskIsRunning(t *testing.T) {
	var out bytes.Buffer
	model := &gatedModel{started: make(chan struct{}), release: make(chan struct{})}
	rt := newTestRuntime(t, model, &out)
	rt.Handle(command{ID: "1", Type: commandSubmit, Message: "first"})
	select {
	case <-model.started:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}
	rt.Handle(command{ID: "2", Type: commandReset})
	close(model.release)
	rt.Wait()

	records := decodeLines(t, out.String())
	for _, record := range records {
		if record["id"] != "2" {
			continue
		}
		errorObject, _ := record["error"].(map[string]any)
		if errorObject["code"] != codeRuntimeBusy {
			t.Fatalf("reset error=%#v", errorObject)
		}
		return
	}
	t.Fatal("missing reset response")
}

func TestCancelTerminalTaskIsRejected(t *testing.T) {
	var out bytes.Buffer
	rt := newTestRuntime(t, fixedModel{content: "done"}, &out)
	rt.Handle(command{ID: "1", Type: commandSubmit, Message: "hello"})
	rt.Wait()
	rt.Handle(command{ID: "2", Type: commandCancel, TaskID: "task-000001"})

	records := decodeLines(t, out.String())
	for _, record := range records {
		if record["id"] != "2" {
			continue
		}
		errorObject, _ := record["error"].(map[string]any)
		if errorObject["code"] != codeTaskTerminal {
			t.Fatalf("cancel error=%#v", errorObject)
		}
		return
	}
	t.Fatal("missing cancel response")
}

func TestRunLoopReportsInvalidJSON(t *testing.T) {
	var out bytes.Buffer
	rt := newTestRuntime(t, fixedModel{content: "done"}, &out)
	if err := RunLoop(strings.NewReader("{bad json}\n"), rt); err != nil {
		t.Fatalf("RunLoop() error: %v", err)
	}
	records := decodeLines(t, out.String())
	errorObject, _ := records[0]["error"].(map[string]any)
	if errorObject["code"] != codeInvalidJSON {
		t.Fatalf("invalid JSON error=%#v", errorObject)
	}
}

func TestDecodeCommandRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	if _, err := decodeCommand([]byte(`{"type":"state","unknown":true}`)); err == nil {
		t.Fatal("expected unknown field error")
	}
	if _, err := decodeCommand([]byte(`{"type":"state"} {"type":"state"}`)); err == nil {
		t.Fatal("expected trailing value error")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestProtocolWriteErrorsAreReported(t *testing.T) {
	runner, err := agent.New(fixedModel{content: "done"})
	if err != nil {
		t.Fatalf("agent.New() error: %v", err)
	}
	rt, err := New(runner, failingWriter{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	rt.Handle(command{Type: commandState})
	if err := rt.Err(); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("write error=%v", err)
	}
}

func TestConcurrentEventsAreWrittenInSequenceOrder(t *testing.T) {
	var out bytes.Buffer
	rt := newTestRuntime(t, fixedModel{content: "done"}, &out)

	const total = 100
	var wg sync.WaitGroup
	wg.Add(total)
	for i := 0; i < total; i++ {
		go func() {
			defer wg.Done()
			rt.emit(event{Type: "test"})
		}()
	}
	wg.Wait()

	records := decodeLines(t, out.String())
	if len(records) != total {
		t.Fatalf("records=%d, want %d", len(records), total)
	}
	for i, record := range records {
		if sequence := record["sequence"].(float64); sequence != float64(i+1) {
			t.Fatalf("records[%d] sequence=%v", i, sequence)
		}
	}
}
