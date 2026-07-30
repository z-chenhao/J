package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestRunAdaptsObserverInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	environment := map[string]string{
		"JSPACE_CAPTURE_URL":   server.URL + "/jspace/api/captures",
		"JSPACE_CAPTURE_TOKEN": "secret",
		"JSPACE_OUTBOX":        filepath.Join(t.TempDir(), "outbox"),
	}
	input := bytes.NewBufferString(`{
		"schemaVersion":"j.observer.run.v0.1",
		"id":"run-1",
		"label":"J-tui test",
		"product":"J-tui",
		"commit":"abcdef",
		"model":"qwen",
		"succeeded":true,
		"frames":[{
			"request":{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]},
			"response":{
				"message":{"role":"assistant","content":[{"type":"text","text":"world"}]},
				"provider":"test",
				"model":"qwen",
				"stopReason":"stop"
			}
		}]
	}`)
	err := run(context.Background(), input, func(name string) (string, bool) {
		value, exists := environment[name]
		return value, exists
	})
	if err != nil {
		t.Fatal(err)
	}
}
