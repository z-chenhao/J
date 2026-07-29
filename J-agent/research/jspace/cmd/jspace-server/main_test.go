package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/z-chenhao/J/J-agent/research/jspace/internal/artifact"
	jspaceweb "github.com/z-chenhao/J/J-agent/research/jspace/web"
)

func testServer(t *testing.T, token string) (*server, string) {
	t.Helper()
	state := t.TempDir()
	sub, err := fsSub()
	if err != nil {
		t.Fatal(err)
	}
	return &server{
		config: config{stateDir: state},
		token:  token,
		static: http.FileServer(http.FS(sub)),
	}, state
}

func TestAPIRequiresTokenAndIsReadOnly(t *testing.T) {
	application, _ := testServer(t, "a-very-long-test-token-that-is-secret")
	for _, test := range []struct {
		method string
		path   string
		token  string
		status int
	}{
		{http.MethodGet, basePath + "/api/status", "", http.StatusUnauthorized},
		{http.MethodGet, basePath + "/api/status", "wrong", http.StatusUnauthorized},
		{http.MethodGet, basePath + "/api/status", application.token, http.StatusOK},
		{http.MethodPost, basePath + "/api/runs", application.token, http.StatusMethodNotAllowed},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		if test.token != "" {
			request.Header.Set("Authorization", "Bearer "+test.token)
		}
		response := httptest.NewRecorder()
		application.routes().ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s %s status=%d want=%d body=%s",
				test.method, test.path, response.Code, test.status, response.Body.String())
		}
	}
}

func TestRunsReturnsSafeArtifact(t *testing.T) {
	application, state := testServer(t, "")
	now := time.Now().UTC()
	trace := artifact.Trace{
		SchemaVersion: artifact.SchemaVersion,
		ID:            "run-1",
		Label:         "private-free",
		CreatedAt:     now,
		UpdatedAt:     now,
		Status:        "completed",
		Agent:         artifact.Agent{Model: "qwen"},
		Measurement:   artifact.Measurement{Kind: "posthoc_replay"},
	}
	if err := artifact.WriteAtomic(filepath.Join(state, "runs"), trace); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, basePath+"/api/runs/run-1", nil)
	response := httptest.NewRecorder()
	application.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var loaded artifact.Trace
	if err := json.Unmarshal(response.Body.Bytes(), &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.ID != trace.ID {
		t.Fatalf("loaded=%#v", loaded)
	}
}

func TestStaticPageHasSecurityHeaders(t *testing.T) {
	application, _ := testServer(t, "")
	request := httptest.NewRequest(http.MethodGet, basePath+"/", nil)
	response := httptest.NewRecorder()
	application.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Security-Policy") == "" ||
		response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("headers=%v", response.Header())
	}
}

func fsSub() (fs.FS, error) {
	return fs.Sub(jspaceweb.Files, ".")
}
