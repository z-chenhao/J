package main

import (
	"context"
	"os"

	"github.com/z-chenhao/J/internal/runtime"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--rpc" {
		if err := runtime.RunRPC(os.Stdin, os.Stdout); err != nil {
			_, _ = os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		return
	}

	if err := runtime.RunCLI(context.Background(), os.Stdin, os.Stdout, os.Stderr, os.Args[1:]...); err != nil {
		os.Exit(1)
	}
}
