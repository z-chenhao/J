package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/z-chenhao/J-agent/agent"
)

// RunCLI runs the reference CLI around an injected agent.
func RunCLI(
	ctx context.Context,
	runner *agent.Agent,
	in io.Reader,
	out, errOut io.Writer,
	args ...string,
) error {
	if runner == nil {
		return errors.New("runner is required")
	}
	if len(args) > 0 {
		_, runErr := runStreaming(ctx, runner, strings.Join(args, " "), out)
		if runErr != nil {
			return runErr
		}
		return nil
	}
	return runInteractive(ctx, runner, in, out, errOut)
}

// RunRPC runs the experimental JSONL reference transport around an injected
// agent.
func RunRPC(ctx context.Context, runner *agent.Agent, in io.Reader, out io.Writer) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	rt, err := New(runner, out)
	if err != nil {
		return err
	}
	rt.ctx = ctx
	return RunLoop(in, rt)
}

func runInteractive(ctx context.Context, runner *agent.Agent, in io.Reader, out, errOut io.Writer) error {
	if out != nil {
		fmt.Fprintln(out, "J-agent started. Empty lines are skipped; type exit to quit.")
	}
	scanner := bufio.NewScanner(in)
	for {
		if out != nil {
			fmt.Fprint(out, "user> ")
		}
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			if out != nil {
				fmt.Fprintln(out, "bye")
			}
			break
		}
		if _, err := runStreaming(ctx, runner, line, out); err != nil {
			if errOut != nil {
				fmt.Fprintln(errOut, "error:", err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	return nil
}

func runStreaming(
	ctx context.Context,
	runner *agent.Agent,
	input string,
	out io.Writer,
) (agent.RunResult, error) {
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		streamed bool
		writeErr error
	)
	result, err := runner.Run(runContext, input, func(event agent.Event) {
		if out == nil || writeErr != nil ||
			event.Type != agent.EventMessageDelta || event.Delta == nil {
			return
		}
		if event.Delta.Type == agent.DeltaText {
			streamed = true
			if _, writeErr = io.WriteString(out, event.Delta.Delta); writeErr != nil {
				cancel()
			}
		}
	})
	if writeErr != nil {
		return result, fmt.Errorf("write output: %w", writeErr)
	}
	if err != nil {
		return result, err
	}
	if out != nil {
		if !streamed {
			if _, err := io.WriteString(out, result.Message.Text()); err != nil {
				return result, fmt.Errorf("write output: %w", err)
			}
		}
		if _, err := io.WriteString(out, "\n"); err != nil {
			return result, fmt.Errorf("write output: %w", err)
		}
	}
	return result, nil
}
