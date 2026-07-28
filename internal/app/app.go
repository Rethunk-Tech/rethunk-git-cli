// Package app is rgit's command surface: argument parsing, validation,
// and dispatch. It lives outside main so every entry point is reachable
// from a test without building and executing a binary.
package app

import (
	"context"
	"fmt"
	"io"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

const usageLine = "usage: rgit [--version] <diff|commit> [flags] [target...]"

// Run dispatches one rgit invocation and returns its exit code. version is
// supplied by the caller so the build-time -ldflags value stays attached to
// package main.
func Run(ctx context.Context, version string, args []string, stdout, stderr io.Writer) exitcode.Code {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usageLine)
		return exitcode.InvalidUsage
	}

	switch args[0] {
	case "--version":
		fmt.Fprintf(stdout, "rgit %s\n", version)
		return exitcode.Success
	case "diff":
		return runDiff(ctx, args[1:], stdout, stderr)
	case "commit":
		return runCommit(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "rgit: unknown command %q\n", args[0])
		fmt.Fprintln(stderr, usageLine)
		return exitcode.InvalidUsage
	}
}
