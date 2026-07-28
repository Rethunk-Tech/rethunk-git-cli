// The `rgit commit` command surface: flag parsing, validation, and dispatch
// into internal/synth. Flags and exit codes are specified in docs/USAGE.md.
package app

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/pflag"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

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
	classified, err := cli.ClassifyArgs(restoreDoubleDash(fs), false, cli.GitPathChecker{Root: root, Repo: repo, Ctx: ctx}, cli.GitRevisionResolver{Repo: repo, Ctx: ctx})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}
	_ = classified // staging (blob synthesis, git add) is internal/resolve + internal/synth's job

	fmt.Fprintln(stderr, "rgit: commit execution is not implemented yet (Phase 1 spine only)")
	return NotImplemented
}
