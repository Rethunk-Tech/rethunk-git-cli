// Command rgit is git add <pathspec> && git commit at symbol granularity.
// See AGENTS.md for the governing invariant and docs/USAGE.md for the
// full command reference.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/pflag"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=vX.Y.Z"
var version = "dev"

// phase1Unimplemented is a placeholder for command execution, which lands
// in later phases (internal/resolve, internal/synth, internal/diff own
// that ground). It is deliberately not part of internal/exitcode's public
// table in docs/USAGE.md — nothing outside this file may ever produce it,
// and it disappears once real execution ships.
const phase1Unimplemented exitcode.Code = 1

func main() {
	os.Exit(int(run(os.Args[1:], os.Stdout, os.Stderr)))
}

func run(args []string, stdout, stderr io.Writer) exitcode.Code {
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

const usageLine = "usage: rgit [--version] <diff|commit> [flags] [target...]"

// diffFlags mirrors the `rgit diff` flag surface in docs/USAGE.md § Flags.
type diffFlags struct {
	unstaged  bool
	staged    bool
	rangeFlag string
	porcelain bool
	exitCode  bool
	quiet     bool
	syms      []string
	files     []string
}

func runDiff(args []string, stdout, stderr io.Writer) exitcode.Code {
	fs := pflag.NewFlagSet("diff", pflag.ContinueOnError)
	fs.SetInterspersed(true) // git accepts flags after positionals; stdlib flag does not
	fs.SetOutput(io.Discard) // errors are reported by us, not pflag's own usage printer

	var f diffFlags
	fs.BoolVar(&f.unstaged, "unstaged", false, "worktree vs index (git's bare diff)")
	fs.BoolVar(&f.staged, "staged", false, "index vs HEAD")
	fs.BoolVar(&f.staged, "cached", false, "alias for --staged")
	fs.StringVar(&f.rangeFlag, "range", "", "explicit form of a positional revision range")
	fs.BoolVar(&f.porcelain, "porcelain", false, "stable tab-separated records")
	fs.BoolVar(&f.exitCode, "exit-code", false, "exit 1 when anything is committable")
	fs.BoolVar(&f.quiet, "quiet", false, "implies --exit-code and suppresses output")
	fs.StringArrayVar(&f.syms, "sym", nil, "explicit FILE:NAME anchor (repeatable)")
	fs.StringArrayVar(&f.files, "file", nil, "explicit pathspec (repeatable)")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}

	if f.staged && f.rangeFlag != "" {
		fmt.Fprintln(stderr, "rgit: --staged and --range are mutually exclusive")
		return exitcode.InvalidUsage
	}
	if f.staged && f.unstaged {
		fmt.Fprintln(stderr, "rgit: --staged and --unstaged are mutually exclusive")
		return exitcode.InvalidUsage
	}

	if code := anchorFileContradiction(f.syms, f.files, stderr); code != exitcode.Success {
		return code
	}

	root, repo, code := openRepo(stderr)
	if code != exitcode.Success {
		return code
	}

	ctx := context.Background()
	classified, err := cli.ClassifyArgs(fs.Args(), true, cli.GitPathChecker{Root: root, Repo: repo, Ctx: ctx}, cli.GitRevisionResolver{Repo: repo, Ctx: ctx})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}
	_ = classified // rendering (--porcelain, MODE, BINARY rows) is internal/diff's job

	fmt.Fprintln(stderr, "rgit: diff execution is not implemented yet (Phase 1 spine only)")
	return phase1Unimplemented
}

// commitFlags mirrors the `rgit commit` flag surface in docs/USAGE.md §
// Flags.
type commitFlags struct {
	messages   []string
	msgFile    string
	signoff    bool
	trailers   []string
	amend      bool
	allowEmpty bool
	push       bool
	dryRun     bool
	noVerify   bool
	syms       []string
	files      []string
}

func runCommit(args []string, stdout, stderr io.Writer) exitcode.Code {
	fs := pflag.NewFlagSet("commit", pflag.ContinueOnError)
	fs.SetInterspersed(true)
	fs.SetOutput(io.Discard)

	var f commitFlags
	// -m's long spelling matches git commit's own --message exactly, per
	// AGENTS.md's governing principle. -F has no documented long spelling
	// in docs/USAGE.md (git's own --file would collide with rgit's
	// existing --file pathspec-target flag below), so its long form here
	// is an unadvertised, purely internal registration name pflag
	// requires; only -F is documented.
	fs.StringArrayVarP(&f.messages, "message", "m", nil, "commit message (repeatable)")
	fs.StringVarP(&f.msgFile, "message-file", "F", "", "read the message from a file, or - for stdin")
	fs.BoolVarP(&f.signoff, "signoff", "s", false, "append Signed-off-by")
	fs.StringArrayVar(&f.trailers, "trailer", nil, "append a trailer TOKEN:VALUE (repeatable)")
	fs.BoolVar(&f.amend, "amend", false, "amend the previous commit")
	fs.BoolVar(&f.allowEmpty, "allow-empty", false, "permit a commit with no changes")
	fs.BoolVar(&f.push, "push", false, "push upstream after a successful commit")
	fs.BoolVar(&f.dryRun, "dry-run", false, "preview only; writes and stages nothing")
	fs.BoolVar(&f.noVerify, "no-verify", false, "skip git hooks")
	fs.StringArrayVar(&f.syms, "sym", nil, "explicit FILE:NAME anchor (repeatable)")
	fs.StringArrayVar(&f.files, "file", nil, "explicit pathspec (repeatable)")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}

	if len(f.messages) > 0 && f.msgFile != "" {
		fmt.Fprintln(stderr, "rgit: -m and -F are mutually exclusive")
		return exitcode.InvalidUsage
	}
	if f.dryRun && f.push {
		fmt.Fprintln(stderr, "rgit: --dry-run and --push are mutually exclusive")
		return exitcode.InvalidUsage
	}
	if len(f.messages) == 0 && f.msgFile == "" {
		fmt.Fprintln(stderr, "rgit: commit requires a message (-m or -F)")
		return exitcode.InvalidUsage
	}

	positionalsGiven := fs.Args()
	if len(positionalsGiven)+len(f.syms)+len(f.files) == 0 {
		fmt.Fprintln(stderr, "rgit: commit requires at least one target")
		return exitcode.InvalidUsage
	}

	if code := anchorFileContradiction(f.syms, f.files, stderr); code != exitcode.Success {
		return code
	}

	if !hasConventionalShape(f.messages) {
		fmt.Fprintln(stderr, `rgit: warning: message does not look like "type(scope): subject"`)
	}

	root, repo, code := openRepo(stderr)
	if code != exitcode.Success {
		return code
	}

	ctx := context.Background()
	classified, err := cli.ClassifyArgs(positionalsGiven, false, cli.GitPathChecker{Root: root, Repo: repo, Ctx: ctx}, cli.GitRevisionResolver{Repo: repo, Ctx: ctx})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}
	_ = classified // staging (blob synthesis, git add) is internal/resolve + internal/synth's job

	fmt.Fprintln(stderr, "rgit: commit execution is not implemented yet (Phase 1 spine only)")
	return phase1Unimplemented
}

// anchorFileContradiction implements docs/USAGE.md's "--sym and --file on
// the same path → exit 5": naming a path both ways is a contradiction the
// caller must resolve, not a case rgit could silently pick a side on.
func anchorFileContradiction(syms, files []string, stderr io.Writer) exitcode.Code {
	fileSet := make(map[string]bool, len(files))
	for _, path := range files {
		fileSet[path] = true
	}
	for _, sym := range syms {
		idx := strings.LastIndexByte(sym, ':')
		if idx <= 0 {
			continue // malformed --sym value; not this check's job to diagnose
		}
		file := sym[:idx]
		if fileSet[file] {
			fmt.Fprintf(stderr, "rgit: --sym and --file both name %q\n", file)
			return exitcode.ContradictoryAnchors
		}
	}
	return exitcode.Success
}

// conventionalShapeRe is a loose match for "type(scope): subject" and
// "type(scope)!: subject" — just enough to decide whether the warning in
// docs/USAGE.md ("A missing conventional-commit shape ... warns on
// stderr; the commit proceeds") should fire. It is advisory only.
var conventionalShapeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*(\([^()]+\))?!?: .+`)

func hasConventionalShape(messages []string) bool {
	if len(messages) == 0 {
		// -F was used instead; the message lives in a file the execution
		// step reads, not something this parse-only phase inspects.
		return true
	}
	return conventionalShapeRe.MatchString(messages[0])
}

// openRepo resolves the current working directory's git toplevel and
// returns a Repo rooted there. Every subcommand needs this before
// classification, since rules 3-5 all query git or the filesystem.
func openRepo(stderr io.Writer) (root string, repo *gitx.Repo, code exitcode.Code) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return "", nil, exitcode.GitFailure
	}

	probe := gitx.New(cwd)
	top, err := probe.Toplevel(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return "", nil, exitcode.GitFailure
	}

	return top, gitx.New(top), exitcode.Success
}
