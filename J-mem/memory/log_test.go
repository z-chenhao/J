package memory

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/J/J-agent/agent"
)

func TestLogStoreModifyRetrieveForget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	log, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	times := []time.Time{
		time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 27, 2, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 27, 4, 0, 0, 0, time.UTC),
	}
	log.now = func() time.Time {
		next := times[0]
		times = times[1:]
		return next
	}

	first, err := log.Store(context.Background(), "User prefers concise answers")
	if err != nil {
		t.Fatal(err)
	}
	second, err := log.Store(context.Background(), "Project J uses Go")
	if err != nil {
		t.Fatal(err)
	}
	modified, err := log.Modify(
		context.Background(),
		first.ID,
		"User prefers concise Chinese answers",
	)
	if err != nil {
		t.Fatal(err)
	}
	if modified.CreatedAt != first.CreatedAt || modified.UpdatedAt == first.UpdatedAt {
		t.Fatalf("modify did not preserve creation and advance update: %+v", modified)
	}

	records, err := log.Retrieve(context.Background(), "chinese", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != first.ID {
		t.Fatalf("unexpected retrieval: %+v", records)
	}
	recent, err := log.Retrieve(context.Background(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].ID != first.ID {
		t.Fatalf("unexpected recent memory: %+v", recent)
	}

	if err := log.Forget(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	records, err = log.Retrieve(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != second.ID {
		t.Fatalf("unexpected records after forget: %+v", records)
	}
	if err := log.Forget(context.Background(), first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got error %v, want ErrNotFound", err)
	}

	handle, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	scanner := bufio.NewScanner(handle)
	lines := 0
	for scanner.Scan() {
		lines++
		if !json.Valid(scanner.Bytes()) {
			t.Fatalf("line %d is not valid JSON", lines)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lines != 4 {
		t.Fatalf("got %d JSONL events, want 4", lines)
	}
}

func TestMemoryToolsImplementAgentTool(t *testing.T) {
	log, err := Open(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	tools := log.Tools()
	if len(tools) != 4 {
		t.Fatalf("got %d tools, want 4", len(tools))
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Spec().Name)
	}
	want := []string{
		"memory_retrieve",
		"memory_store",
		"memory_modify",
		"memory_forget",
	}
	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("got names %v, want %v", names, want)
		}
	}
	if _, err := agent.New(memoryStubModel{}, agent.WithTools(tools...)); err != nil {
		t.Fatalf("memory tools could not construct an Agent: %v", err)
	}

	storedJSON, err := tools[1].Call(
		context.Background(),
		json.RawMessage(`{"content":"Remember me"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var stored Record
	if err := json.Unmarshal([]byte(storedJSON), &stored); err != nil {
		t.Fatal(err)
	}
	retrievedJSON, err := tools[0].Call(
		context.Background(),
		json.RawMessage(`{"query":"remember","limit":5}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(retrievedJSON, stored.ID) {
		t.Fatalf("retrieval %q did not contain stored ID %q", retrievedJSON, stored.ID)
	}
	if _, err := tools[3].Call(
		context.Background(),
		json.RawMessage(`{"id":"`+stored.ID+`"}`),
	); err != nil {
		t.Fatal(err)
	}
}

func TestLogRejectsMalformedHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	if err := os.WriteFile(path, []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected malformed JSONL error")
	}
}

func TestLogValidatesInputs(t *testing.T) {
	log, err := Open(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Store(nil, "content"); err == nil {
		t.Fatal("expected nil context error")
	}
	if _, err := log.Store(context.Background(), " "); err == nil {
		t.Fatal("expected empty content error")
	}
	if _, err := log.Retrieve(context.Background(), "", 101); err == nil {
		t.Fatal("expected invalid limit error")
	}
}

type memoryStubModel struct{}

func (memoryStubModel) Complete(
	context.Context,
	agent.ModelRequest,
	func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	return agent.ModelResponse{}, errors.New("not implemented")
}
