package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/z-chenhao/J/J-space/internal/artifact"
	jspaceauth "github.com/z-chenhao/J/J-space/internal/auth"
	jspaceweb "github.com/z-chenhao/J/J-space/web"
)

const basePath = "/jspace"

type config struct {
	listen    string
	stateDir  string
	tokenFile string
}

type server struct {
	config config
	token  string
	static http.Handler
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(arguments []string) error {
	config, err := parseConfig(arguments)
	if err != nil {
		return err
	}
	var token string
	if config.tokenFile != "" {
		token, err = jspaceauth.LoadOrCreate(config.tokenFile)
		if err != nil {
			return fmt.Errorf("load viewer token: %w", err)
		}
	}
	sub, err := fs.Sub(jspaceweb.Files, ".")
	if err != nil {
		return err
	}
	application := &server{
		config: config,
		token:  token,
		static: http.FileServer(http.FS(sub)),
	}
	httpServer := &http.Server{
		Addr:              config.listen,
		Handler:           application.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	ctx, stop := signal.NotifyContext(
		contextBackground(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := contextWithTimeout(5 * time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	log.Printf("J-Space viewer listening at http://%s%s/", config.listen, basePath)
	err = httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func parseConfig(arguments []string) (config, error) {
	flags := flag.NewFlagSet("jspace-server", flag.ContinueOnError)
	home, err := os.UserHomeDir()
	if err != nil {
		return config{}, err
	}
	var value config
	flags.StringVar(&value.listen, "listen", env("JSPACE_LISTEN", "127.0.0.1:8090"), "listen address")
	flags.StringVar(
		&value.stateDir,
		"state-dir",
		env("JSPACE_STATE_DIR", filepath.Join(home, ".j", "jspace")),
		"local research state directory",
	)
	flags.StringVar(
		&value.tokenFile,
		"token-file",
		env("JSPACE_TOKEN_FILE", ""),
		"optional viewer token file",
	)
	if err := flags.Parse(arguments); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, errors.New("jspace-server does not accept positional arguments")
	}
	if _, _, err := net.SplitHostPort(value.listen); err != nil {
		return config{}, fmt.Errorf("invalid listen address: %w", err)
	}
	if strings.TrimSpace(value.stateDir) == "" {
		return config{}, errors.New("state directory is required")
	}
	return value, nil
}

func (application *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(basePath, func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, basePath+"/", http.StatusPermanentRedirect)
	})
	mux.HandleFunc(basePath+"/api/status", application.requireToken(application.status))
	mux.HandleFunc(basePath+"/api/runs", application.requireToken(application.runs))
	mux.HandleFunc(basePath+"/api/runs/", application.requireToken(application.runByID))
	mux.HandleFunc(basePath+"/api/stream", application.requireToken(application.stream))
	mux.HandleFunc(basePath+"/api/demo", application.demo)
	mux.Handle(basePath+"/", application.staticHandler())
	return securityHeaders(mux)
}

func (application *server) staticHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-cache")
		path := strings.TrimPrefix(request.URL.Path, basePath+"/")
		if path == "" {
			path = "/"
		} else {
			path = "/" + path
		}
		if strings.HasPrefix(strings.TrimPrefix(path, "/"), "api/") ||
			strings.Contains(path, "..") {
			http.NotFound(writer, request)
			return
		}
		request.URL.Path = path
		application.static.ServeHTTP(writer, request)
	})
}

func (application *server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if application.token != "" &&
			!jspaceauth.Equal(jspaceauth.Bearer(request.Header.Get("Authorization")), application.token) {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="J-Space viewer"`)
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "viewer token required"})
			return
		}
		next(writer, request)
	}
}

func (application *server) status(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	traces, err := artifact.LoadAll(application.runsDirectory())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "load runs"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"status":          "ready",
		"readOnly":        true,
		"authenticated":   application.token != "",
		"runCount":        len(traces),
		"schemaVersion":   artifact.SchemaVersion,
		"measurement":     "posthoc_replay",
		"publicMutations": false,
	})
}

func (application *server) runs(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	traces, err := artifact.LoadAll(application.runsDirectory())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "load runs"})
		return
	}
	summaries := make([]artifact.Summary, 0, len(traces))
	for _, trace := range traces {
		summaries = append(summaries, trace.Summary())
	}
	writeJSON(writer, http.StatusOK, summaries)
}

func (application *server) runByID(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	id := strings.TrimPrefix(request.URL.Path, basePath+"/api/runs/")
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		http.NotFound(writer, request)
		return
	}
	trace, err := artifact.Load(filepath.Join(application.runsDirectory(), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(writer, request)
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "load run"})
		return
	}
	writeJSON(writer, http.StatusOK, trace.Public())
}

func (application *server) stream(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "stream unavailable"})
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastSignature := ""
	for {
		traces, err := artifact.LoadAll(application.runsDirectory())
		if err == nil {
			signature := signatureOf(traces)
			if signature != lastSignature {
				summaries := make([]artifact.Summary, 0, len(traces))
				for _, trace := range traces {
					summaries = append(summaries, trace.Summary())
				}
				payload, _ := json.Marshal(summaries)
				_, _ = fmt.Fprintf(writer, "event: runs\ndata: %s\n\n", payload)
				flusher.Flush()
				lastSignature = signature
			}
		}
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
			_, _ = fmt.Fprint(writer, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func (application *server) demo(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	content, err := fs.ReadFile(jspaceweb.Files, "demo.json")
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "load demo"})
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "public, max-age=300")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(content)
}

func (application *server) runsDirectory() string {
	return filepath.Join(application.config.stateDir, "runs")
}

func signatureOf(traces []artifact.Trace) string {
	var builder strings.Builder
	for _, trace := range traces {
		builder.WriteString(trace.ID)
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatInt(trace.UpdatedAt.UnixNano(), 10))
		builder.WriteByte(';')
	}
	return builder.String()
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(request.URL.Path, basePath+"/api/") {
			writer.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(writer, request)
	})
}

func methodNotAllowed(writer http.ResponseWriter, allowed ...string) {
	writer.Header().Set("Allow", strings.Join(allowed, ", "))
	writeJSON(writer, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
