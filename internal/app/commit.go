// The `rgit commit` command surface: flag parsing, validation, and dispatch
// into internal/synth. Flags and exit codes are specified in docs/USAGE.md.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/pflag"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/synth"
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
	// -m's long spelling matches git commit's own --message, and -F's long
	// spelling is --message-file (to avoid collision with rgit's --file
	// pathspec flag).
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

	root, prefix, repo, code := openRepo(stderr)
	if code != exitcode.Success {
		return code
	}

	ctx := context.Background()
	classified, err := cli.ClassifyArgs(restoreDoubleDash(fs), false, cli.GitPathChecker{Root: root, Prefix: prefix, Repo: repo, Ctx: ctx}, cli.GitRevisionResolver{Repo: repo, Ctx: ctx})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}

	targets, err := commitTargets(root, prefix, classified, f.files, f.syms)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}

	plan, err := synth.PlanStage(ctx, repo, root, targets)
	if err != nil {
		code, msg := mapStageError(err)
		fmt.Fprintf(stderr, "rgit: %s\n", msg)
		return code
	}

	if plan.TSOnly() {
		fmt.Fprintln(stderr, "rgit: [ts-only] no live language server reached in time; extents unverified")
	}

	allUnchanged := len(plan.Results()) > 0
	for _, r := range plan.Results() {
		if r.Outcome != synth.Unchanged {
			allUnchanged = false
			continue
		}
		fmt.Fprintf(stderr, "[warning] target '%s' has no uncommitted changes; skipping\n", targetLabel(r.Target))
	}

	// docs/USAGE.md § Targets with nothing to commit: exit 11 only when
	// EVERY named target turned out unchanged, and only then -- a mix of
	// changed and unchanged targets is a warning plus a commit of the rest,
	// not a failure. --allow-empty suppresses it.
	if allUnchanged && !f.allowEmpty {
		return exitcode.NothingToCommit
	}

	if f.dryRun {
		// docs/USAGE.md: dry-run "writes no objects, stages nothing, runs
		// no hooks" -- resolution (including the cross-check above) already
		// happened as a pure read; nothing past this point may execute.
		//
		// It still has to say what it resolved. A preview that prints
		// nothing and exits 0 is indistinguishable from one that found
		// nothing, which is the opposite of what a preview is for.
		fmt.Fprintln(stdout, "dry run: nothing written, nothing staged. Would commit:")
		writeTargetListing(stdout, plan.Results())
		return exitcode.Success
	}

	if err := plan.Apply(ctx, repo, root); err != nil {
		code, msg := mapStageError(err)
		fmt.Fprintf(stderr, "rgit: %s\n", msg)
		return code
	}

	opts := gitx.CommitOptions{
		Messages:   f.messages,
		Signoff:    f.signoff,
		Trailers:   f.trailers,
		Amend:      f.amend,
		AllowEmpty: f.allowEmpty,
		NoVerify:   f.noVerify,
	}
	if f.msgFile == "-" {
		data, rerr := io.ReadAll(os.Stdin)
		if rerr != nil {
			fmt.Fprintf(stderr, "rgit: reading commit message from stdin: %v\n", rerr)
			return exitcode.GitFailure
		}
		opts.MessageFile = "-"
		opts.StdinMessage = data
	} else if f.msgFile != "" {
		opts.MessageFile = f.msgFile
	}

	// AGENTS.md: a hook rejecting the commit leaves staging in place, and
	// rgit does not roll it back -- Commit's own error is simply reported.
	res, err := repo.Commit(ctx, opts)
	// Hook output goes to the user either way: on success it is the
	// formatter or codegen telling them what it did, and on failure it is
	// usually the reason.
	if len(res.Stderr) > 0 {
		_, _ = stderr.Write(res.Stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	// git's own summary -- branch, new SHA, and the changed/insertion/
	// deletion counts. Relaying it verbatim is what stops a caller having to
	// run `git show` afterwards just to find out what landed.
	_, _ = stdout.Write(res.Stdout)

	// Then the part git cannot report: which symbols went in, and by how
	// much. Same listing and same order as --dry-run, so a preview and the
	// commit it previews are comparable line for line.
	writeTargetListing(stdout, plan.Results())

	if f.push {
		// A push failure does not roll back the commit that preceded it
		// (docs/USAGE.md § Flags, AGENTS.md's delegation boundary).
		if err := repo.Push(ctx); err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.PushFailed
		}
	}

	return exitcode.Success
}

// targetLabel renders a synth.Target the way docs/USAGE.md's warning
// example does: "FILE:NAME" for a symbol anchor, the bare pathspec
// otherwise -- matching exact copy-paste syntax, same as rgit diff's own
// anchor labels.
func targetLabel(t synth.Target) string {
	if t.Pathspec != "" {
		return t.Pathspec
	}
	return t.Symbol.Path + ":" + t.Symbol.Anchor
}

// mapStageError turns a synth/resolve error into the exit code
// docs/USAGE.md's table assigns it. Both error types already carry their
// own Code field and format their own message via Error(), so this is a
// pure dispatch, not a second source of truth about what each code means.
func mapStageError(err error) (exitcode.Code, string) {
	var perr *synth.PathError
	if errors.As(err, &perr) {
		return perr.Code, perr.Error()
	}
	var rerr *resolve.ResolveError
	if errors.As(err, &rerr) {
		return rerr.Code, rerr.Error()
	}
	// Anything else reaching here ran through gitx (hash-object,
	// update-index, git add, check-ignore, ls-tree) and failed at the git
	// or system level -- docs/USAGE.md's exit 128, mirroring git's own
	// convention for a fatal failure that is not a usage error.
	return exitcode.GitFailure, err.Error()
}

// commitTargets turns rule-classified positionals plus explicit --file/
// --sym flags into synth targets, in the order docs/USAGE.md documents
// pathspecs and anchors mixing freely. It also enforces the one piece of
// path safety rgit owns rather than delegating to git: a pathspec or
// anchor file that resolves outside root is an invalid-usage error (exit
// 129) caught before anything runs, not a fatal git failure discovered
// only after `git add` itself refuses it.
func commitTargets(root, prefix string, classified []cli.Classification, files, syms []string) ([]synth.Target, error) {
	targets := make([]synth.Target, 0, len(classified)+len(files)+len(syms))

	addPathspec := func(p string) error {
		p = cli.PrefixPath(prefix, p)
		if err := checkPathEscape(root, p); err != nil {
			return err
		}
		targets = append(targets, synth.PathTarget(p))
		return nil
	}
	addAnchor := func(file, name string) error {
		file = cli.PrefixPath(prefix, file)
		if err := checkPathEscape(root, file); err != nil {
			return err
		}
		targets = append(targets, synth.AnchorTarget(file, name))
		return nil
	}

	for _, c := range classified {
		switch c.Kind {
		case cli.KindPathspec:
			if err := addPathspec(c.Pathspec); err != nil {
				return nil, err
			}
		case cli.KindAnchor:
			if err := addAnchor(c.Anchor.File, c.Anchor.Name); err != nil {
				return nil, err
			}
		}
	}
	for _, file := range files {
		if err := addPathspec(file); err != nil {
			return nil, err
		}
	}
	for _, sym := range syms {
		file, name, ok := splitAnchor(sym)
		if !ok {
			return nil, fmt.Errorf("malformed --sym value %q", sym)
		}
		if err := addAnchor(file, name); err != nil {
			return nil, err
		}
	}
	return targets, nil
}

// writeTargetListing prints one aligned line per staged target with its
// +N/-M, in the plan's order: alphabetical by path, then ascending by
// position within each file. Shared by --dry-run and a successful commit so
// the preview and the real thing are comparable line for line, and so
// neither has to be re-derived by running `rgit diff` again afterwards.
//
// Unchanged targets are omitted; they already got their own warning on
// stderr and nothing was staged for them.
func writeTargetListing(stdout io.Writer, results []synth.TargetResult) {
	width := 0
	for _, r := range results {
		if r.Outcome == synth.Unchanged {
			continue
		}
		if n := len(targetLabel(r.Target)); n > width {
			width = n
		}
	}
	for _, r := range results {
		if r.Outcome == synth.Unchanged {
			continue
		}
		fmt.Fprintf(stdout, "  %-*s  +%d/-%d\n", width, targetLabel(r.Target), r.Added, r.Deleted)
	}
}
