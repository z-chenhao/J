package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-agent/research/jspace/internal/artifact"
	jspaceauth "github.com/z-chenhao/J/J-agent/research/jspace/internal/auth"
	jspacecapture "github.com/z-chenhao/J/J-agent/research/jspace/internal/capture"
	"github.com/z-chenhao/J/J-agent/research/jspace/internal/replay"
)

func TestClassifyKeepsPublicSurfaceNarrow(t *testing.T) {
	for _, test := range []struct {
		method string
		path   string
		want   route
	}{
		{http.MethodGet, "/v1/models", routeModel},
		{http.MethodPost, "/v1/chat/completions", routeModel},
		{http.MethodGet, "/jspace/", routeJSpacePage},
		{http.MethodGet, "/jspace/app.js", routeJSpacePage},
		{http.MethodGet, "/jspace/api/demo", routeJSpacePage},
		{http.MethodGet, "/jspace/api/runs", routeJSpaceAPI},
		{http.MethodPost, "/jspace/api/captures", routeJSpaceCapture},
		{http.MethodPost, "/jspace/api/runs", routeDenied},
		{http.MethodGet, "/.env", routeDenied},
		{http.MethodGet, "/jspace/private.json", routeDenied},
		{http.MethodGet, "/jspace/app.js.map", routeDenied},
	} {
		if got := classify(test.method, test.path); got != test.want {
			t.Fatalf("%s %s got=%v want=%v", test.method, test.path, got, test.want)
		}
	}
}

func TestCaptureRequiresSeparateTokenAndPersistsProbe(t *testing.T) {
	state := t.TempDir()
	service, err := jspacecapture.New(jspacecapture.Config{
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
	application := &gateway{
		modelProxy:   http.NotFoundHandler(),
		jspaceProxy:  http.NotFoundHandler(),
		viewerToken:  "viewer-secret",
		captureToken: "capture-secret",
		capture:      service,
		modelLimit:   &limiter{windows: make(map[string]rateWindow)},
		viewLimit:    &limiter{windows: make(map[string]rateWindow)},
		captureLimit: &limiter{windows: make(map[string]rateWindow)},
		modelActive:  make(chan struct{}, 1),
	}
	payload := jspacecapture.Run{
		SchemaVersion: jspacecapture.SchemaVersion,
		ID:            "remote-1",
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
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		token  string
		status int
	}{
		{"", http.StatusUnauthorized},
		{"viewer-secret", http.StatusUnauthorized},
		{"capture-secret", http.StatusAccepted},
	} {
		request := httptest.NewRequest(
			http.MethodPost,
			"/jspace/api/captures",
			strings.NewReader(string(body)),
		)
		if test.token != "" {
			request.Header.Set("Authorization", "Bearer "+test.token)
		}
		response := httptest.NewRecorder()
		application.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("token=%q status=%d body=%s", test.token, response.Code, response.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(state, "inbox", "remote-1.json")); err != nil {
		t.Fatal(err)
	}
}

func TestViewerAPIRequiresIndependentToken(t *testing.T) {
	upstream := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !jspaceauth.Equal(jspaceauth.Bearer(request.Header.Get("Authorization")), "viewer-secret") {
			t.Fatal("viewer token was not forwarded to local server")
		}
		_, _ = io.WriteString(writer, `{"ok":true}`)
	})
	application := &gateway{
		modelProxy:  http.NotFoundHandler(),
		jspaceProxy: upstream,
		viewerToken: "viewer-secret",
		modelLimit:  &limiter{windows: make(map[string]rateWindow)},
		viewLimit:   &limiter{windows: make(map[string]rateWindow)},
		modelActive: make(chan struct{}, 1),
	}
	for _, test := range []struct {
		token  string
		status int
	}{
		{"", http.StatusUnauthorized},
		{"wrong", http.StatusUnauthorized},
		{"viewer-secret", http.StatusOK},
	} {
		request := httptest.NewRequest(http.MethodGet, "/jspace/api/runs", nil)
		if test.token != "" {
			request.Header.Set("Authorization", "Bearer "+test.token)
		}
		response := httptest.NewRecorder()
		application.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("token=%q status=%d body=%s", test.token, response.Code, response.Body.String())
		}
	}
}

func TestModelProxyInjectsOnlyModelKey(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer model-secret" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	})
	target, _ := url.Parse("http://model.local")
	proxy := reverseProxyWithTransport(target, "model-secret", transport)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer untrusted-client-value")
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
