// Package replay runs the local MLX Jacobian-lens probe for captured model
// frames. It owns process plumbing, not capture transport or artifact policy.
package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-space/internal/artifact"
)

type Frame struct {
	Request  agent.ModelRequest  `json:"request"`
	Response agent.ModelResponse `json:"response"`
}

type Config struct {
	Python        string
	Script        string
	ModelPath     string
	LensPath      string
	ModelID       string
	ModelRepo     string
	LensRepo      string
	TailPositions int
}

type Output struct {
	Measurement artifact.Measurement `json:"measurement"`
	Turns       []artifact.Turn      `json:"turns"`
	Notes       []string             `json:"notes,omitempty"`
}

type input struct {
	SchemaVersion string  `json:"schemaVersion"`
	ModelPath     string  `json:"modelPath"`
	LensPath      string  `json:"lensPath"`
	ModelID       string  `json:"modelId"`
	ModelRepo     string  `json:"modelRepository"`
	LensRepo      string  `json:"lensRepository"`
	TailPositions int     `json:"tailPositions"`
	Frames        []Frame `json:"frames"`
}

func Run(ctx context.Context, config Config, frames []Frame) (Output, error) {
	content, err := json.Marshal(input{
		SchemaVersion: artifact.SchemaVersion,
		ModelPath:     config.ModelPath,
		LensPath:      config.LensPath,
		ModelID:       config.ModelID,
		ModelRepo:     config.ModelRepo,
		LensRepo:      config.LensRepo,
		TailPositions: config.TailPositions,
		Frames:        frames,
	})
	if err != nil {
		return Output{}, err
	}
	command := exec.CommandContext(ctx, config.Python, config.Script)
	command.Stdin = bytes.NewReader(content)
	var stderr bytes.Buffer
	command.Stderr = &limitedWriter{writer: &stderr, remaining: 32 << 10}
	output, err := command.Output()
	if err != nil {
		return Output{}, fmt.Errorf(
			"run J-lens probe: %w: %s",
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	var measured Output
	if err := json.Unmarshal(output, &measured); err != nil {
		return Output{}, fmt.Errorf("decode J-lens probe output: %w", err)
	}
	return measured, nil
}

type limitedWriter struct {
	writer    io.Writer
	remaining int
}

func (writer *limitedWriter) Write(content []byte) (int, error) {
	original := len(content)
	if writer.remaining <= 0 {
		return original, nil
	}
	if len(content) > writer.remaining {
		content = content[:writer.remaining]
	}
	written, err := writer.writer.Write(content)
	writer.remaining -= written
	if err != nil {
		return written, err
	}
	return original, nil
}
