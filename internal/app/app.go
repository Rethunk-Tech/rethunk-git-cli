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

const usageLine = "usage: rgit [--version] <diff|commit|blame|languages|doctor|completion> [flags] [target...]"

// tsOnlyNotice is what rgit diff and rgit commit both print when no live
// language server was reached in time. docs/INSTALL.md § Verify tells the
// reader to grep either command's stderr for it, so the two staging
// commands cannot answer "was the cross-check live?" differently -- one
// constant instead of two copies that could drift apart.
const tsOnlyNotice = "rgit: [ts-only] no live language server reached in time; extents unverified"

// topLevelHelp is what `rgit --help`, `-h`, and `help` print. Kept to the
// same budget as a subcommand's own help (specs/design.md:231's "--help
// tokens" measurement): orientation, not a manual -- docs/USAGE.md is that.
const topLevelHelp = usageLine + `

rgit is "git add <pathspec> && git commit" at symbol granularity: name a
FILE:NAME anchor (e.g. auth.go:ValidateToken) instead of a whole path, and
only that symbol's extent is staged.

Commands:
  diff        Show what is committable: staged, unstaged, and untracked
  commit      Stage named targets and commit them
  blame       Blame bounded to one symbol's own extent
  languages   List grammars compiled into this binary
  doctor      Report environment health (language servers, grammars, git)
  completion  Print a shell completion script (bash, zsh)

Global flags:
  --version    print the version and exit
  -h, --help   show this help and exit

Run 'rgit diff --help' or 'rgit commit --help' for that command's flags.
Full reference: docs/USAGE.md
`

// Run dispatches one rgit invocation and returns its exit code. version is
// supplied by the caller so the build-time -ldflags value stays attached to
// package main.
func Run(ctx context.Context, version string, args []string, stdout, stderr io.Writer) exitcode.Code {
	if len(args) == 0 {
		// Bare `git` prints its own full help to stdout at exit 1 -- but
		// rgit has no useful no-op mode, and every other usage error in
		// exitcode's table (missing message, no target, a path escape, ...)
		// is already pinned to exit 129. Naming no command is the same kind
		// of usage error: there is nothing to dispatch, so it keeps rgit's
		// own uniform convention rather than adopting git's
		// top-level-dispatcher-only quirk.
		fmt.Fprintln(stderr, usageLine)
		return exitcode.InvalidUsage
	}

	switch args[0] {
	case "--help", "-h", "help":
		fmt.Fprint(stdout, topLevelHelp)
		return exitcode.Success
	case "--version":
		// The first line stays byte-identical to what scripts and
		// cmd/rgit-install already parse (docs/USAGE.md) -- everything a
		// caller might want beyond the bare version goes on lines after it,
		// never on it.
		fmt.Fprintf(stdout, "rgit %s\n", version)
		fmt.Fprintln(stdout, versionGrammarsLine())
		return exitcode.Success
	case "diff":
		return runDiff(ctx, args[1:], stdout, stderr)
	case "commit":
		return runCommit(ctx, args[1:], stdout, stderr)
	case "blame":
		return runBlame(ctx, args[1:], stdout, stderr)
	case "languages":
		return runLanguages(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "completion":
		return runCompletion(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "rgit: unknown command %q\n", args[0])
		fmt.Fprintln(stderr, usageLine)
		return exitcode.InvalidUsage
	}
}
