// Package app is rgit's command surface: argument parsing, validation,
// and dispatch. It lives outside main so every entry point is reachable
// from a test without building and executing a binary.
package app

import (
	"fmt"
	"io"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

// NotImplemented marks a command whose parsing and validation succeed but
// whose execution has not landed yet. It is deliberately outside the
// public table in docs/USAGE.md, and disappears once every command
// executes.
const NotImplemented exitcode.Code = 1

const usageLine = "usage: rgit [--version] <diff|commit> [flags] [target...]"

// Run dispatches one rgit invocation and returns its exit code. version is
// supplied by the caller so the build-time -ldflags value stays attached to
// package main.
func Run(version string, args []string, stdout, stderr io.Writer) exitcode.Code {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usageLine)
		return exitcode.InvalidUsage
	}

	switch args[0] {
	case "--version":
		fmt.Fprintf(stdout, "rgit %s\n", version)
		return exitcode.Success
	case "diff":
		return runDiff(args[1:], stdout, stderr)
	case "commit":
		return runCommit(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "rgit: unknown command %q\n", args[0])
		fmt.Fprintln(stderr, usageLine)
		return exitcode.InvalidUsage
	}
}
