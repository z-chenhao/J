package main

import (
	"context"
	"slices"
	"testing"

	"github.com/z-chenhao/J/J-agent/agent"
)

func TestCommunityHostComposesAndRestoresOnlyPublicSeams(t *testing.T) {
	model := &recordingModel{inner: exampleModel{}}
	runner, err := assemble(model, nil)
	if err != nil {
		t.Fatal(err)
	}
	var events []agent.EventType
	result, err := runner.Run(context.Background(), "first", func(event agent.Event) {
		events = append(events, event.Type)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.Text() != "community host: assembled outside J" {
		t.Fatalf("message=%q", result.Message.Text())
	}
	if model.Calls() != 2 {
		t.Fatalf("model calls=%d", model.Calls())
	}
	if !slices.Contains(events, agent.EventToolCompleted) {
		t.Fatalf("events=%q", events)
	}

	history := runner.History()
	restored, err := assemble(&recordingModel{inner: exampleModel{}}, history)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.History()) != len(history) {
		t.Fatalf("restored history=%d, want %d", len(restored.History()), len(history))
	}
	if _, err := restored.Run(context.Background(), "second", nil); err != nil {
		t.Fatal(err)
	}
}
