package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	jpackages "github.com/z-chenhao/J/J-packages"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		_, err := fmt.Fprintf(out, "j %s\n", version)
		return err
	}
	if len(args) == 0 || args[0] != "package" {
		writeUsage(errOut)
		return errors.New("expected: j package <command>")
	}
	if len(args) == 1 {
		writePackageUsage(errOut)
		return errors.New("package command is required")
	}
	manager, err := newManager()
	if err != nil {
		return err
	}
	switch args[1] {
	case "add":
		if len(args) != 3 {
			return errors.New("usage: j package add <local-path|git:repository@ref>")
		}
		entry, err := manager.Add(ctx, args[2])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(
			out,
			"Installed %s %s from %s\n",
			entry.ID,
			entry.Version,
			entry.Source,
		)
		return err
	case "update":
		if len(args) > 3 {
			return errors.New("usage: j package update [id]")
		}
		id := ""
		if len(args) == 3 {
			id = args[2]
		}
		entries, err := manager.Update(ctx, id)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if _, err := fmt.Fprintf(out, "Updated %s %s\n", entry.ID, entry.Version); err != nil {
				return err
			}
		}
		return nil
	case "remove":
		if len(args) != 3 {
			return errors.New("usage: j package remove <id>")
		}
		entry, err := manager.Remove(args[2])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(
			out,
			"Removed %s; cached source was retained at %s\n",
			entry.ID,
			entry.Root,
		)
		return err
	case "list":
		if len(args) != 2 {
			return errors.New("usage: j package list")
		}
		entries, err := manager.List()
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			_, err = fmt.Fprintln(out, "No J packages installed.")
			return err
		}
		for _, entry := range entries {
			resolved := entry.Resolved
			if resolved == "" {
				resolved = "local"
			}
			if _, err := fmt.Fprintf(
				out,
				"%s\t%s\t%s\t%s\n",
				entry.ID,
				entry.Version,
				resolved,
				entry.Source,
			); err != nil {
				return err
			}
		}
		return nil
	case "check":
		if len(args) != 3 {
			return errors.New("usage: j package check <local-path>")
		}
		pkg, err := manager.Check(args[2])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Valid %s %s\n", pkg.Manifest.ID, pkg.Manifest.Version)
		return err
	case "doctor":
		if len(args) != 2 {
			return errors.New("usage: j package doctor")
		}
		installed, err := manager.Doctor()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Validated %d installed J packages.\n", len(installed))
		return err
	default:
		writePackageUsage(errOut)
		return fmt.Errorf("unknown package command %q", args[1])
	}
}

func newManager() (*jpackages.Manager, error) {
	return jpackages.NewManager(
		strings.TrimSpace(os.Getenv("J_PACKAGES_REGISTRY")),
		strings.TrimSpace(os.Getenv("J_PACKAGES_CACHE")),
	)
}

func writeUsage(out io.Writer) {
	_, _ = fmt.Fprintln(out, "Usage: j package <add|update|remove|list|check|doctor>")
}

func writePackageUsage(out io.Writer) {
	flags := flag.NewFlagSet("j package", flag.ContinueOnError)
	flags.SetOutput(out)
	writeUsage(out)
}
