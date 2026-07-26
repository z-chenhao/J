// Package bash provides a first-party Bash implementation of agent.Tool.
//
// The package executes in the caller's environment. It fixes the working
// directory at construction time, bounds model-visible output, and honors
// context cancellation. Isolation remains the responsibility of the process
// boundary, normally the J container.
package bash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/z-chenhao/J/J-agent/agent"
)

const (
	maxOutputBytes   = 50 * 1024
	maxOutputLines   = 2000
	maxTimeoutSecond = float64(math.MaxInt64 / int64(time.Second))
)

var inputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"command": {
			"type": "string",
			"description": "Bash command to execute"
		},
		"timeout": {
			"type": "number",
			"exclusiveMinimum": 0,
			"description": "Optional timeout in seconds; no timeout is applied when omitted"
		}
	},
	"required": ["command"],
	"additionalProperties": false
}`)

type tool struct {
	dir   string
	shell string
}

type input struct {
	Command string   `json:"command"`
	Timeout *float64 `json:"timeout,omitempty"`
}

// New constructs a Bash tool rooted at dir. The directory and Bash executable
// are resolved once so later calls cannot silently change the execution root.
func New(dir string) (agent.Tool, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("bash working directory is required")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve bash working directory: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("inspect bash working directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("bash working directory %q is not a directory", absolute)
	}
	shell, err := exec.LookPath("bash")
	if err != nil {
		return nil, errors.New("bash executable was not found on PATH")
	}
	shell, err = filepath.Abs(shell)
	if err != nil {
		return nil, fmt.Errorf("resolve bash executable: %w", err)
	}
	return &tool{dir: absolute, shell: shell}, nil
}

func (t *tool) Spec() agent.ToolSpec {
	return agent.ToolSpec{
		Name: "bash",
		Description: "Execute a Bash command in the fixed workspace. Returns combined stdout and stderr. " +
			"Output is limited to the last 2000 lines or 50KB, whichever is reached first. " +
			"An optional timeout may be supplied in seconds.",
		InputSchema: append(json.RawMessage(nil), inputSchema...),
	}
}

func (t *tool) Call(ctx context.Context, arguments json.RawMessage) (string, error) {
	if ctx == nil {
		return "", errors.New("bash context is required")
	}
	var request input
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return "", fmt.Errorf("decode bash arguments: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return "", err
	}
	request.Command = strings.TrimSpace(request.Command)
	if request.Command == "" {
		return "", errors.New("bash command is required")
	}

	runCtx := ctx
	cancel := func() {}
	if request.Timeout != nil {
		if math.IsNaN(*request.Timeout) || math.IsInf(*request.Timeout, 0) ||
			*request.Timeout <= 0 || *request.Timeout > maxTimeoutSecond {
			return "", fmt.Errorf("bash timeout must be greater than zero and at most %.0f seconds", maxTimeoutSecond)
		}
		timeout := time.Duration(*request.Timeout * float64(time.Second))
		if timeout <= 0 {
			return "", errors.New("bash timeout must be at least one nanosecond")
		}
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	output := &tailOutput{}
	command := exec.CommandContext(runCtx, t.shell, "-c", request.Command)
	command.Dir = t.dir
	command.Stdout = output
	command.Stderr = output
	configureCommand(command)
	err := command.Run()
	text := output.String()

	if runCtx.Err() != nil {
		status := "Command canceled"
		if ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			status = fmt.Sprintf("Command timed out after %g seconds", *request.Timeout)
		}
		return appendStatus(text, status), runCtx.Err()
	}
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return appendStatus(text, fmt.Sprintf("Command exited with code %d", exitError.ExitCode())), err
		}
		return appendStatus(text, "Command could not be started"), err
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode bash arguments: multiple JSON values")
		}
		return fmt.Errorf("decode bash arguments: %w", err)
	}
	return nil
}

func appendStatus(output, status string) string {
	if output == "" {
		return status
	}
	return output + "\n\n" + status
}

type tailOutput struct {
	data        []byte
	totalBytes  int64
	newlines    int64
	sawOutput   bool
	lastNewline bool
}

func (w *tailOutput) Write(input []byte) (int, error) {
	sanitized := sanitize(input)
	if len(sanitized) == 0 {
		return len(input), nil
	}
	w.sawOutput = true
	w.totalBytes += int64(len(sanitized))
	w.newlines += int64(bytes.Count(sanitized, []byte{'\n'}))
	w.lastNewline = sanitized[len(sanitized)-1] == '\n'
	w.data = append(w.data, sanitized...)
	if len(w.data) > maxOutputBytes+utf8.UTFMax {
		w.data = append([]byte(nil), w.data[len(w.data)-(maxOutputBytes+utf8.UTFMax):]...)
	}
	return len(input), nil
}

func (w *tailOutput) String() string {
	if !w.sawOutput {
		return ""
	}
	data := w.data
	truncatedByBytes := w.totalBytes > maxOutputBytes
	if len(data) > maxOutputBytes {
		data = data[len(data)-maxOutputBytes:]
		for len(data) > 0 && !utf8.RuneStart(data[0]) {
			data = data[1:]
		}
	}

	totalLines := w.newlines
	if !w.lastNewline {
		totalLines++
	}
	truncatedByLines := totalLines > maxOutputLines
	if truncatedByLines {
		start := tailLineStart(data, maxOutputLines)
		data = data[start:]
	}
	text := strings.Map(func(value rune) rune {
		if value == '\t' || value == '\n' || !unicode.IsControl(value) {
			return value
		}
		return -1
	}, string(bytes.ToValidUTF8(data, []byte("�"))))
	text = strings.TrimRight(text, "\n")
	if !truncatedByBytes && !truncatedByLines {
		return text
	}
	shownLines := int64(bytes.Count(data, []byte{'\n'}))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		shownLines++
	}
	marker := fmt.Sprintf(
		"[output truncated: showing the last %d bytes and %d lines of %d bytes and %d lines]",
		len(data),
		shownLines,
		w.totalBytes,
		totalLines,
	)
	if text == "" {
		return marker
	}
	return marker + "\n" + text
}

func tailLineStart(data []byte, lines int64) int {
	if lines <= 0 {
		return len(data)
	}
	remaining := lines
	for index := len(data) - 1; index >= 0; index-- {
		if data[index] != '\n' {
			continue
		}
		if index == len(data)-1 {
			continue
		}
		remaining--
		if remaining == 0 {
			return index + 1
		}
	}
	return 0
}

func sanitize(input []byte) []byte {
	output := make([]byte, 0, len(input))
	for _, value := range input {
		switch {
		case value == '\t' || value == '\n':
			output = append(output, value)
		case value == '\r':
			// Normalize CR and CRLF output to LF-only terminal text.
		case value >= 0x20 && value != 0x7f:
			output = append(output, value)
		}
	}
	return output
}
