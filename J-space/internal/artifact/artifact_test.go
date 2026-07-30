package artifact

import (
	"path/filepath"
	"testing"
	"time"
)

func validTrace() Trace {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	return Trace{
		SchemaVersion: SchemaVersion,
		ID:            "run-1",
		Label:         "measured run",
		CreatedAt:     now,
		UpdatedAt:     now,
		Status:        "completed",
		Agent:         Agent{Model: "qwen"},
		Measurement:   Measurement{Kind: "posthoc_replay"},
		Turns: []Turn{{
			Index: 0,
			SelectedPositions: []Position{{
				Index: 3,
				Token: "spider",
				Role:  "assistant",
				Layers: []LayerRead{{
					Layer:  12,
					Region: "workspace",
					Top:    []Concept{{Token: "spider", Rank: 1}},
				}},
			}},
		}},
	}
}

func TestWriteAtomicRoundTrip(t *testing.T) {
	directory := t.TempDir()
	trace := validTrace()
	if err := WriteAtomic(directory, trace); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(filepath.Join(directory, trace.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != trace.ID || loaded.Turns[0].SelectedPositions[0].Token != "spider" {
		t.Fatalf("loaded=%#v", loaded)
	}
}

func TestValidateRejectsInvalidConceptRank(t *testing.T) {
	trace := validTrace()
	trace.Turns[0].SelectedPositions[0].Layers[0].Top[0].Rank = 0
	if err := trace.Validate(); err == nil {
		t.Fatal("invalid rank was accepted")
	}
}

func TestValidateAcceptsRelativeTokenPosition(t *testing.T) {
	trace := validTrace()
	trace.Turns[0].SelectedPositions[0].Index = -3
	if err := trace.Validate(); err != nil {
		t.Fatalf("negative relative token position was rejected: %v", err)
	}
}

func TestPublicProjectionNeverContainsTranscript(t *testing.T) {
	trace := validTrace()
	trace.Transcript = []Message{{Role: "user", Content: "private prompt"}}
	public := trace.Public()
	if len(public.Transcript) != 0 {
		t.Fatalf("public transcript=%#v", public.Transcript)
	}
	if len(trace.Transcript) != 1 {
		t.Fatal("public projection mutated the local artifact")
	}
}

func TestLoadAllSortsNewestFirst(t *testing.T) {
	directory := t.TempDir()
	first := validTrace()
	first.ID = "first"
	if err := WriteAtomic(directory, first); err != nil {
		t.Fatal(err)
	}
	second := validTrace()
	second.ID = "second"
	second.CreatedAt = first.CreatedAt.Add(time.Minute)
	second.UpdatedAt = second.CreatedAt
	if err := WriteAtomic(directory, second); err != nil {
		t.Fatal(err)
	}
	traces, err := LoadAll(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 2 || traces[0].ID != "second" {
		t.Fatalf("traces=%#v", traces)
	}
}
