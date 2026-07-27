package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/z-chenhao/J/J-agent/agent"
)

func TestStoreRoundTripsHistory(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "transcripts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	history := []agent.Message{
		agent.TextMessage(agent.RoleUser, "remember this"),
		{
			Role: agent.RoleAssistant,
			Content: []agent.Content{
				{Type: agent.ContentReasoning, Text: "private continuation"},
				{Type: agent.ContentText, Text: "remembered"},
				{
					Type: agent.ContentToolCall,
					ToolCall: &agent.ToolCall{
						ID:        "call-1",
						Name:      "memory_store",
						Arguments: json.RawMessage(`{"content":"remember this"}`),
					},
				},
			},
		},
		{
			Role:       agent.RoleTool,
			Content:    []agent.Content{{Type: agent.ContentText, Text: `{"id":"mem-1"}`}},
			ToolCallID: "call-1",
			ToolName:   "memory_store",
		},
		agent.TextMessage(agent.RoleAssistant, "done"),
	}
	if err := store.Save(context.Background(), "session-1", history); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	loaded[0].Content[0].Text = "changed"
	reloaded, err := store.Load(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded[0].Content[0].Text != "remember this" {
		t.Fatal("loaded transcript was not a defensive snapshot")
	}
	if _, err := agent.New(stubModel{}, agent.WithHistory(reloaded...)); err != nil {
		t.Fatalf("stored history could not restore an Agent: %v", err)
	}
}

func TestStoreReplacesAndDeletesTranscript(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "transcripts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Save(ctx, "session", []agent.Message{
		agent.TextMessage(agent.RoleUser, "first"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, "session", []agent.Message{
		agent.TextMessage(agent.RoleUser, "second"),
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded[0].Text(); got != "second" {
		t.Fatalf("got %q, want second", got)
	}
	if err := store.Delete(ctx, "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx, "session"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got error %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, "session"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got error %v, want ErrNotFound", err)
	}
}

func TestStoreValidatesInputs(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "transcripts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(nil, "session", nil); err == nil {
		t.Fatal("expected nil context error")
	}
	if err := store.Save(context.Background(), " ", nil); err == nil {
		t.Fatal("expected empty session ID error")
	}
	if _, err := store.Load(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got error %v, want ErrNotFound", err)
	}
}

type stubModel struct{}

func (stubModel) Complete(
	context.Context,
	agent.ModelRequest,
	func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	return agent.ModelResponse{}, errors.New("not implemented")
}
