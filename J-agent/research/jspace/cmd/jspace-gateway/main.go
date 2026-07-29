package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	jspaceauth "github.com/z-chenhao/J/J-agent/research/jspace/internal/auth"
)

const (
	defaultListenAddress  = "127.0.0.1:8088"
	defaultModelUpstream  = "http://127.0.0.1:8000"
	defaultJSpaceUpstream = "http://127.0.0.1:8090"
	maxRequestBytes       = 2 << 20
	modelRequestsPerMin   = 20
	viewRequestsPerMin    = 120
)

type settings struct {
	Auth struct {
		APIKey string `json:"api_key"`
	} `json:"auth"`
}

type rateWindow struct {
	start time.Time
	count int
}

type limiter struct {
	mu      sync.Mutex
	windows map[string]rateWindow
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

type gateway struct {
	modelProxy  http.Handler
	jspaceProxy http.Handler
	viewerToken string
	modelLimit  *limiter
	viewLimit   *limiter
	modelActive chan struct{}
}

func main() {
	handler, err := buildGateway()
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr:              envOrDefault("J_GATEWAY_LISTEN", defaultListenAddress),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	log.Printf("J public gateway listening address=%s", server.Addr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func buildGateway() (http.Handler, error) {
	modelKey, err := loadModelAPIKey()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	tokenFile := envOrDefault(
		"JSPACE_TOKEN_FILE",
		filepath.Join(home, ".j", "jspace", "access-token"),
	)
	viewerToken, err := jspaceauth.LoadOrCreate(tokenFile)
	if err != nil {
		return nil, fmt.Errorf("load J-Space viewer token: %w", err)
	}
	modelUpstream, err := url.Parse(envOrDefault("J_GATEWAY_UPSTREAM", defaultModelUpstream))
	if err != nil {
		return nil, fmt.Errorf("parse model upstream: %w", err)
	}
	jspaceUpstream, err := url.Parse(
		envOrDefault("J_GATEWAY_JSPACE_UPSTREAM", defaultJSpaceUpstream),
	)
	if err != nil {
		return nil, fmt.Errorf("parse J-Space upstream: %w", err)
	}
	application := &gateway{
		modelProxy:  reverseProxy(modelUpstream, modelKey),
		jspaceProxy: reverseProxy(jspaceUpstream, ""),
		viewerToken: viewerToken,
		modelLimit:  &limiter{windows: make(map[string]rateWindow)},
		viewLimit:   &limiter{windows: make(map[string]rateWindow)},
		modelActive: make(chan struct{}, 1),
	}
	return application, nil
}

func (application *gateway) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	started := time.Now()
	tracked := &statusWriter{ResponseWriter: writer}
	defer func() {
		log.Printf(
			"request method=%s path=%s client=%s status=%d duration=%s",
			request.Method,
			request.URL.Path,
			clientIP(request),
			tracked.status,
			time.Since(started).Round(time.Millisecond),
		)
	}()

	switch classify(request.Method, request.URL.Path) {
	case routeModel:
		if !application.modelLimit.allow(clientIP(request), started, modelRequestsPerMin) {
			tracked.Header().Set("Retry-After", "60")
			writeJSONError(tracked, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		if err := boundBody(request); err != nil {
			writeJSONError(tracked, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		select {
		case application.modelActive <- struct{}{}:
			defer func() { <-application.modelActive }()
		default:
			tracked.Header().Set("Retry-After", "5")
			writeJSONError(tracked, http.StatusTooManyRequests, "model is busy")
			return
		}
		application.modelProxy.ServeHTTP(tracked, request)
	case routeJSpacePage:
		addPageHeaders(tracked.Header())
		application.jspaceProxy.ServeHTTP(tracked, request)
	case routeJSpaceAPI:
		if !jspaceauth.Equal(
			jspaceauth.Bearer(request.Header.Get("Authorization")),
			application.viewerToken,
		) {
			tracked.Header().Set("WWW-Authenticate", `Bearer realm="J-Space viewer"`)
			writeJSONError(tracked, http.StatusUnauthorized, "viewer token required")
			return
		}
		if !application.viewLimit.allow(clientIP(request), started, viewRequestsPerMin) {
			tracked.Header().Set("Retry-After", "60")
			writeJSONError(tracked, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		tracked.Header().Set("Cache-Control", "no-store")
		application.jspaceProxy.ServeHTTP(tracked, request)
	default:
		writeJSONError(tracked, http.StatusNotFound, "not found")
	}
}

type route int

const (
	routeDenied route = iota
	routeModel
	routeJSpacePage
	routeJSpaceAPI
)

func classify(method, path string) route {
	switch {
	case method == http.MethodGet && path == "/v1/models":
		return routeModel
	case method == http.MethodPost && path == "/v1/chat/completions":
		return routeModel
	case (method == http.MethodGet || method == http.MethodHead) &&
		(path == "/jspace" || path == "/jspace/" ||
			path == "/jspace/app.js" ||
			path == "/jspace/styles.css" ||
			path == "/jspace/api/demo"):
		return routeJSpacePage
	case method == http.MethodGet && strings.HasPrefix(path, "/jspace/api/"):
		return routeJSpaceAPI
	default:
		return routeDenied
	}
}

func reverseProxy(upstream *url.URL, modelKey string) http.Handler {
	return reverseProxyWithTransport(upstream, modelKey, nil)
}

func reverseProxyWithTransport(
	upstream *url.URL,
	modelKey string,
	transport http.RoundTripper,
) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	if transport != nil {
		proxy.Transport = transport
	}
	originalDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalDirector(request)
		request.Host = upstream.Host
		request.Header.Del("Proxy-Authorization")
		if modelKey != "" {
			request.Header.Set("Authorization", "Bearer "+modelKey)
		}
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, proxyErr error) {
		log.Printf("proxy_error error=%q", proxyErr)
		writeJSONError(writer, http.StatusBadGateway, "upstream unavailable")
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Header.Del("Server")
		response.Header.Del("X-Powered-By")
		return nil
	}
	return proxy
}

func boundBody(request *http.Request) error {
	if request.Body == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxRequestBytes+1))
	_ = request.Body.Close()
	if err != nil {
		return errors.New("invalid request body")
	}
	if len(body) > maxRequestBytes {
		return errors.New("request body too large")
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	return nil
}

func (window *limiter) allow(key string, now time.Time, maximum int) bool {
	window.mu.Lock()
	defer window.mu.Unlock()
	current := window.windows[key]
	if current.start.IsZero() || now.Sub(current.start) >= time.Minute {
		window.windows[key] = rateWindow{start: now, count: 1}
		return true
	}
	if current.count >= maximum {
		return false
	}
	current.count++
	window.windows[key] = current
	return true
}

func (writer *statusWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusWriter) Write(content []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(content)
}

func (writer *statusWriter) Flush() {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func loadModelAPIKey() (string, error) {
	if value := strings.TrimSpace(os.Getenv("OMLX_API_KEY")); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	path := envOrDefault(
		"OMLX_SETTINGS_PATH",
		filepath.Join(home, ".omlx", "settings.json"),
	)
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read oMLX settings: %w", err)
	}
	var config settings
	if err := json.Unmarshal(content, &config); err != nil {
		return "", fmt.Errorf("decode oMLX settings: %w", err)
	}
	if strings.TrimSpace(config.Auth.APIKey) == "" {
		return "", errors.New("oMLX API key is not configured")
	}
	return config.Auth.APIKey, nil
}

func clientIP(request *http.Request) string {
	if forwarded := request.Header.Get("X-Forwarded-For"); forwarded != "" {
		if value := strings.TrimSpace(strings.Split(forwarded, ",")[0]); value != "" {
			return value
		}
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil {
		return host
	}
	return request.RemoteAddr
}

func addPageHeaders(header http.Header) {
	header.Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func writeJSONError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "gateway_error",
		},
	})
}
