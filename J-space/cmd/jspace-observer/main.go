package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	jspaceobserver "github.com/z-chenhao/J/J-space/internal/observer"
)

const maxInputBytes = 8 << 20

func main() {
	if err := run(context.Background(), os.Stdin, os.LookupEnv); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "jspace-observer:", err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	input io.Reader,
	lookup func(string) (string, bool),
) error {
	content, err := io.ReadAll(io.LimitReader(input, maxInputBytes+1))
	if err != nil {
		return err
	}
	if len(content) > maxInputBytes {
		return errors.New("observer run exceeds 8 MiB")
	}
	var observed jspaceobserver.Run
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&observed); err != nil {
		return fmt.Errorf("decode observer run: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("observer input must contain one JSON value")
	}
	url := environment(lookup, "JSPACE_CAPTURE_URL")
	token := environment(lookup, "JSPACE_CAPTURE_TOKEN")
	outbox := environment(lookup, "JSPACE_OUTBOX")
	if outbox == "" {
		home := environment(lookup, "HOME")
		if home == "" {
			return errors.New("HOME or JSPACE_OUTBOX is required")
		}
		outbox = filepath.Join(home, ".j", "jspace-outbox")
	}
	return jspaceobserver.Deliver(ctx, jspaceobserver.Config{
		URL:    url,
		Token:  token,
		Outbox: outbox,
	}, observed)
}

func environment(lookup func(string) (string, bool), name string) string {
	value, _ := lookup(name)
	return strings.TrimSpace(value)
}
