package runtime

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/z-chenhao/J/agent"
	"github.com/z-chenhao/J/internal/demo"
)

// RunCLI runs the deterministic reference CLI.
func RunCLI(ctx context.Context, in io.Reader, out, errOut io.Writer, args ...string) error {
	runner, err := agent.New(demo.Model{})
	if err != nil {
		return err
	}
	if len(args) > 0 {
		result, runErr := runner.Run(ctx, strings.Join(args, " "), nil)
		if runErr != nil {
			if errOut != nil {
				fmt.Fprintln(errOut, "error:", runErr)
			}
			return runErr
		}
		if out != nil {
			fmt.Fprintln(out, result.Content)
		}
		return nil
	}
	return runInteractive(ctx, runner, in, out, errOut)
}

// RunRPC runs the experimental JSONL reference transport.
func RunRPC(in io.Reader, out io.Writer) error {
	runner, err := agent.New(demo.Model{})
	if err != nil {
		return err
	}
	rt, err := New(runner, out)
	if err != nil {
		return err
	}
	return RunLoop(in, rt)
}

func runInteractive(ctx context.Context, runner *agent.Agent, in io.Reader, out, errOut io.Writer) error {
	if out != nil {
		fmt.Fprintln(out, "J started. Empty lines are skipped; type exit to quit.")
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
		result, err := runner.Run(ctx, line, nil)
		if err != nil {
			if errOut != nil {
				fmt.Fprintln(errOut, "error:", err)
			}
			continue
		}
		if out != nil {
			fmt.Fprintln(out, result.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	return nil
}
