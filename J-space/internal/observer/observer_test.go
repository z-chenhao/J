package observer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-space/internal/replay"
)

func testRun() Run {
	return Run{
		SchemaVersion: SchemaVersion,
		ID:            "run-1",
		Label:         "J-tui test",
		Product:       "J-tui",
		Commit:        "abcdef",
		Model:         "qwen",
		Succeeded:     true,
		Frames: []replay.Frame{{
			Request: agent.ModelRequest{
				Messages: []agent.Message{agent.TextMessage(agent.RoleUser, "hello")},
			},
			Response: agent.ModelResponse{
				Message:    agent.TextMessage(agent.RoleAssistant, "world"),
				Provider:   "test",
				Model:      "qwen",
				StopReason: agent.StopReasonStop,
			},
		}},
	}
}

func TestDeliverPostsAuthenticatedCapture(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/jspace/api/captures" ||
			request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request=%s auth=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		var body struct {
			SchemaVersion string `json:"schemaVersion"`
			ID            string `json:"id"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		received = body.SchemaVersion + ":" + body.ID
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	err := Deliver(context.Background(), Config{
		URL:        server.URL + "/jspace/api/captures",
		Token:      "secret",
		Outbox:     filepath.Join(t.TempDir(), "outbox"),
		HTTPClient: server.Client(),
	}, testRun())
	if err != nil {
		t.Fatal(err)
	}
	if received != "jspace.capture.v0.1:run-1" {
		t.Fatalf("received=%q", received)
	}
}

func TestDeliverQueuesRetryableFailureAndFlushesNextRun(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	var accepted atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		if failing.Load() {
			http.Error(writer, "later", http.StatusServiceUnavailable)
			return
		}
		accepted.Add(1)
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	outbox := filepath.Join(t.TempDir(), "outbox")
	config := Config{
		URL:        server.URL + "/jspace/api/captures",
		Token:      "secret",
		Outbox:     outbox,
		HTTPClient: server.Client(),
	}
	if err := Deliver(context.Background(), config, testRun()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(outbox, "run-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	next := testRun()
	next.ID = "run-2"
	if err := Deliver(context.Background(), config, next); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outbox, "run-2.json")); err != nil {
		t.Fatal(err)
	}
	failing.Store(false)
	third := testRun()
	third.ID = "run-3"
	if err := Deliver(context.Background(), config, third); err != nil {
		t.Fatal(err)
	}
	if accepted.Load() != 3 {
		t.Fatalf("accepted=%d", accepted.Load())
	}
	if entries, err := os.ReadDir(outbox); err != nil || len(entries) != 0 {
		t.Fatalf("entries=%v error=%v", entries, err)
	}
}

func TestDeliverDoesNotQueueRejectedCapture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		http.Error(writer, "bad", http.StatusBadRequest)
	}))
	defer server.Close()
	outbox := filepath.Join(t.TempDir(), "outbox")
	err := Deliver(context.Background(), Config{
		URL:        server.URL + "/jspace/api/captures",
		Token:      "secret",
		Outbox:     outbox,
		HTTPClient: server.Client(),
	}, testRun())
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(outbox); !os.IsNotExist(statErr) {
		t.Fatalf("outbox error=%v", statErr)
	}
}
