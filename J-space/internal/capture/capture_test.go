package capture

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-space/internal/artifact"
	"github.com/z-chenhao/J/J-space/internal/replay"
)

func TestServicePersistsThenCompletesCapturedRun(t *testing.T) {
	state := t.TempDir()
	service, err := New(Config{
		StateDir:       state,
		SupportedModel: "qwen",
		Replay: replay.Config{
			ModelRepo: "Qwen/qwen",
			LensRepo:  "lens",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	measured := make(chan struct{}, 1)
	service.measure = func(
		context.Context,
		replay.Config,
		[]replay.Frame,
	) (replay.Output, error) {
		measured <- struct{}{}
		return replay.Output{
			Measurement: artifact.Measurement{
				Kind:            "posthoc_replay",
				ModelCheckpoint: "Qwen/qwen",
				LensRepository:  "lens",
			},
			Turns: []artifact.Turn{{Index: 0}},
		}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.Run(ctx)
	run := testRun()
	if err := service.Enqueue(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	select {
	case <-measured:
	case <-time.After(time.Second):
		t.Fatal("capture was not measured")
	}
	var trace artifact.Trace
	deadline := time.Now().Add(time.Second)
	for {
		trace, err = artifact.Load(filepath.Join(state, "runs", run.ID+".json"))
		if err == nil && trace.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("trace=%#v error=%v", trace, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if trace.Measurement.Kind != "posthoc_replay" || len(trace.Turns) != 1 {
		t.Fatalf("trace=%#v", trace)
	}
	if _, err := os.Stat(filepath.Join(state, "inbox", run.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("inbox error=%v", err)
	}
}

func TestServiceRejectsUnsupportedModel(t *testing.T) {
	service, err := New(Config{
		StateDir:       t.TempDir(),
		SupportedModel: "qwen",
	})
	if err != nil {
		t.Fatal(err)
	}
	run := testRun()
	run.Agent.Model = "other"
	if err := service.Enqueue(context.Background(), run); err == nil {
		t.Fatal("unsupported model was accepted")
	}
}

func testRun() Run {
	return Run{
		SchemaVersion: SchemaVersion,
		ID:            "remote-test",
		Label:         "Remote J-tui run",
		Agent:         artifact.Agent{Model: "qwen"},
		Frames: []replay.Frame{{
			Request: agent.ModelRequest{
				Messages: []agent.Message{agent.TextMessage(agent.RoleUser, "hi")},
			},
			Response: agent.ModelResponse{
				Message: agent.TextMessage(agent.RoleAssistant, "hello"),
				Model:   "qwen",
			},
		}},
	}
}
