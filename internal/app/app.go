// Package app is rgit's command surface: argument parsing, validation,
// and dispatch. It lives outside main so every entry point is reachable
// from a test without building and executing a binary.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

const usageLine = "usage: rgit [--version] [-C <path>] <diff|commit|blame|log|context|languages|doctor|completion> [flags] [target...]"

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
  log         Patch-free history of one symbol
  context     One-call repository orientation, as a record stream
  languages   List grammars compiled into this binary
  doctor      Report environment health (language servers, grammars, git)
  completion  Print a shell completion script (bash, zsh)

Global flags (before the command):
  -C <path>    run as if rgit was started in <path>
  --version    print the version and exit
  -h, --help   show this help and exit

Run 'rgit diff --help' or 'rgit commit --help' for that command's flags.
Full reference: docs/USAGE.md
`

// Run dispatches one rgit invocation and returns its exit code. version is
// supplied by the caller so the build-time -ldflags value stays attached to
// package main.
func Run(ctx context.Context, version string, args []string, stdout, stderr io.Writer) exitcode.Code {
	dir, args, code, ok := parseChdir(args, stderr)
	if !ok {
		return code
	}

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
		return runDiff(ctx, dir, args[1:], stdout, stderr)
	case "commit":
		return runCommit(ctx, dir, args[1:], stdout, stderr)
	case "blame":
		return runBlame(ctx, dir, args[1:], stdout, stderr)
	case "log":
		return runLog(ctx, dir, args[1:], stdout, stderr)
	case "context":
		return runContext(ctx, dir, args[1:], stdout, stderr)
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

// parseChdir consumes the leading `-C <path>` options and returns the
// directory every repository-opening subcommand should resolve from, plus
// the remaining arguments. dir is "" when none was given, meaning "the
// process working directory" -- openRepo's own default, so an invocation
// without -C reaches git through exactly the path it always did.
//
// This is git's own option, matched rather than reinvented (AGENTS.md's one
// invariant), down to each observable: -C is accepted only *before* the
// command, since `git commit -C <commit>` already means "reuse that
// commit's message" and the two spellings must not collide; options
// accumulate, each read relative to the last (`-C a -C b` is `-C a/b`) with
// an absolute path resetting; and `-C ""` is a documented no-op rather than
// an error or a jump to the root.
//
// Where it diverges is the *message* on two refusals, never the exit code.
// git rejects the glued `-C<path>` as an unknown option, which rgit does
// too -- but it names the fix, because -C exists here for the caller
// writing git-shaped commands from memory, and "unknown command" would send
// them looking for a missing subcommand instead of a missing space.
//
// ok is false when the caller must return code immediately; the refusal has
// already been written to stderr.
func parseChdir(args []string, stderr io.Writer) (dir string, rest []string, code exitcode.Code, ok bool) {
	for len(args) > 0 && strings.HasPrefix(args[0], "-C") {
		if args[0] != "-C" {
			fmt.Fprintf(stderr, "rgit: -C takes its directory as a separate argument (-C <path>), not %q\n", args[0])
			fmt.Fprintln(stderr, usageLine)
			return "", nil, exitcode.InvalidUsage, false
		}
		if len(args) < 2 {
			fmt.Fprintln(stderr, "rgit: no directory given for '-C' option")
			fmt.Fprintln(stderr, usageLine)
			return "", nil, exitcode.InvalidUsage, false
		}
		switch next := args[1]; {
		case next == "":
		case dir == "" || filepath.IsAbs(next):
			dir = next
		default:
			dir = filepath.Join(dir, next)
		}
		args = args[2:]
	}
	if dir == "" {
		return "", args, exitcode.Success, true
	}

	// Validated here, before dispatch, rather than left to the first git
	// call: git chdirs up front and dies whatever command followed, so a
	// broken -C must not be silent on `doctor`, `languages`, `completion`
	// or `--version` merely because those never open a repository. The
	// exit code is git's own for a chdir it cannot perform (128), not the
	// 129 a malformed option gets.
	info, err := os.Stat(dir)
	if err != nil {
		// The errno alone, matching git's own "cannot change to '<path>':
		// No such file or directory". os.Stat's wrapper would repeat the
		// path back and name a syscall the caller never asked for.
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		fmt.Fprintf(stderr, "rgit: cannot change to %q: %v\n", dir, err)
		return "", nil, exitcode.GitFailure, false
	}
	if !info.IsDir() {
		fmt.Fprintf(stderr, "rgit: cannot change to %q: not a directory\n", dir)
		return "", nil, exitcode.GitFailure, false
	}
	return dir, args, exitcode.Success, true
}
