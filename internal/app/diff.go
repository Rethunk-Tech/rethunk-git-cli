// The `rgit diff` command surface: flag parsing, validation, and dispatch
// into internal/diff. Flags and exit codes are specified in docs/USAGE.md.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	diffpkg "github.com/Rethunk-Tech/rethunk-git-cli/internal/diff"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// diffCommittable is docs/USAGE.md § Flags' --exit-code/--quiet value:
// "exit 1 when anything is committable, 0 when clean". It mirrors git's own
// --exit-code convention and is deliberately not in exitcode's named table
// — unlike every other exit status there, its meaning is conditional on a
// flag rather than fixed.
const diffCommittable exitcode.Code = 1

// diffFlags mirrors the `rgit diff` flag surface in docs/USAGE.md § Flags.
type diffFlags struct {
	unstaged  bool
	staged    bool
	rangeFlag string
	porcelain bool
	exitCode  bool
	quiet     bool
	patch     bool
	syms      []string
	files     []string
}

func runDiff(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	var f diffFlags
	fs := newTargetFlagSet("diff", &f.syms, &f.files)
	fs.BoolVar(&f.unstaged, "unstaged", false, "worktree vs index (git's bare diff)")
	fs.BoolVar(&f.staged, "staged", false, "index vs HEAD")
	fs.BoolVar(&f.staged, "cached", false, "alias for --staged")
	fs.StringVar(&f.rangeFlag, "range", "", "explicit form of a positional revision range")
	fs.BoolVar(&f.porcelain, "porcelain", false, "stable tab-separated records")
	fs.BoolVar(&f.exitCode, "exit-code", false, "exit 1 when anything is committable")
	fs.BoolVar(&f.quiet, "quiet", false, "implies --exit-code and suppresses output")
	fs.BoolVarP(&f.patch, "patch", "p", false, "include the real patch body")

	help := "usage: rgit diff [flags] [target...]\n\n" +
		"Show what is committable -- staged, unstaged, and untracked -- broken\n" +
		"down by symbol. Filter with a FILE:NAME anchor or a pathspec.\n\n" +
		fs.FlagUsages() +
		"\nFull reference: docs/USAGE.md\n"
	if code, done := parseFlagsOrHelp(fs, args, stdout, stderr, help); done {
		return code
	}

	if f.staged && f.rangeFlag != "" {
		fmt.Fprintln(stderr, "rgit: --staged and --range are mutually exclusive")
		return exitcode.InvalidUsage
	}
	if f.staged && f.unstaged {
		fmt.Fprintln(stderr, "rgit: --staged and --unstaged are mutually exclusive")
		return exitcode.InvalidUsage
	}
	// Same contradiction commit.go already refuses: machine-readable
	// output and no output at all, together, would leave a script parsing
	// an empty stream it cannot tell apart from "nothing to commit".
	if f.porcelain && f.quiet {
		fmt.Fprintln(stderr, "rgit: --porcelain and --quiet are mutually exclusive")
		return exitcode.InvalidUsage
	}
	if f.porcelain && f.patch {
		fmt.Fprintln(stderr, "rgit: --porcelain and --patch are mutually exclusive")
		return exitcode.InvalidUsage
	}

	root, prefix, repo, code := openRepo(ctx, dir, stderr)
	if code != exitcode.Success {
		return code
	}

	checker := cli.GitPathChecker{Root: root, Prefix: prefix, Repo: repo}

	rangeToken, rest, err := diffpkg.ExtractRangeToken(ctx, restoreDoubleDash(fs), checker)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}
	// Same wording as internal/diff/scope.go's own check (ResolveScope would
	// catch this identically further down), surfaced here as soon as both
	// sides are known instead of after classification and BucketClassified
	// have already done their own git calls for nothing.
	if f.rangeFlag != "" && rangeToken != "" {
		fmt.Fprintln(stderr, "rgit: --range and a positional revision range are mutually exclusive")
		return exitcode.InvalidUsage
	}

	classified, err := cli.ClassifyArgs(ctx, rest, true, checker, cli.GitRevisionResolver{Repo: repo})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}

	revisions, files, syms, revPaths, err := diffpkg.BucketClassified(classified)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}
	// Same wording as internal/diff/scope.go's own check. --staged and
	// --range are already caught above with no repo call at all; this is
	// the rest of that same contradiction -- --unstaged, and either flag
	// against a bare revision positional -- which cannot be known until
	// classification has resolved what the positionals are.
	if (f.staged || f.unstaged) && (f.rangeFlag != "" || rangeToken != "" || len(revisions) > 0) {
		fmt.Fprintln(stderr, "rgit: --staged/--unstaged and a revision range are mutually exclusive")
		return exitcode.InvalidUsage
	}

	symFlags, err := symRefsFromFlag(f.syms)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}

	allFiles := slices.Concat(files, f.files)
	allSyms := slices.Concat(syms, symFlags)
	for i, p := range allFiles {
		resolved, err := repoPath(root, prefix, p)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.InvalidUsage
		}
		allFiles[i] = resolved
	}
	for i, s := range allSyms {
		resolved, err := repoPath(root, prefix, s.File)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.InvalidUsage
		}
		s.File = resolved
		allSyms[i] = s
	}

	// Same rule and same point in the flow as rgit commit: positionals and
	// flags have merged and every path carries the invocation prefix, so the
	// two spellings of the same contradiction are caught identically.
	anchorFiles := make([]string, 0, len(allSyms))
	for _, s := range allSyms {
		anchorFiles = append(anchorFiles, s.File)
	}
	if code := pathAnchorContradiction(allFiles, anchorFiles, stderr); code != exitcode.Success {
		return code
	}

	opts := diffpkg.Options{
		Staged:          f.staged,
		Unstaged:        f.unstaged,
		RangeFlag:       f.rangeFlag,
		PositionalRange: rangeToken,
		Revisions:       revisions,
		Files:           allFiles,
		Syms:            allSyms,
		RevPaths:        revPaths,
		// Skip fetching the patch entirely when output is suppressed by
		// --quiet: nothing would ever read it, so there is no reason to pay
		// for the extra `git diff` invocation.
		Patch: f.patch && !f.quiet,
	}

	report, err := diffpkg.Run(ctx, repo, root, opts)
	if err != nil {
		var uerr *diffpkg.UsageError
		if errors.As(err, &uerr) {
			fmt.Fprintf(stderr, "rgit: %v\n", uerr)
			return exitcode.InvalidUsage
		}
		// A --sym/bare-anchor value that does not resolve (internal/diff's
		// validateSyms): the same *resolve.ResolveError rgit commit produces
		// for the identical anchor, mapped through its own Code field rather
		// than a second table of what each code means (mapStageError in
		// internal/app/commit.go does the equivalent dispatch for commit).
		var rerr *resolve.ResolveError
		if errors.As(err, &rerr) {
			msg := rerr.Error()
			if rerr.Code == exitcode.UnsupportedLanguage {
				// Unlike synth.PathError (commit.go's mapStageError),
				// ResolveError does not always carry the file the anchor
				// resolved against -- but validateSym (internal/diff/run.go)
				// attaches it via Path for this exact failure, so it is read
				// straight off the error rather than recovered by matching
				// the failed anchor's bare name back against allSyms.
				if ext, ok := extForFailedSym(rerr); ok {
					msg += unsupportedLanguageHint(ext)
				}
			}
			fmt.Fprintf(stderr, "rgit: %s\n", msg)
			return rerr.Code
		}
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}

	// tsOnlyNotice (app.go) is shared with rgit commit's own identical
	// print, so the two staging commands cannot answer "was the
	// cross-check live?" differently. Printed regardless of --quiet, which
	// suppresses the report on stdout rather than diagnostics, exactly as
	// the warnings below already are.
	if report.TSOnly {
		fmt.Fprintln(stderr, tsOnlyNotice)
	}

	// Reported, never fatal: the identical disagreement is exit 6 at commit
	// time, and the point of saying so here is that the caller finds out
	// while reading the diff rather than mid-commit.
	for _, w := range report.Warnings {
		fmt.Fprintf(stderr, "[warning] %s\n", w)
	}
	// diffpkg.Run above already resolved every entry in allSyms successfully
	// (a failure returned above instead), so any that is ordinal-shaped is
	// commit's own advisory, surfaced here too rather than only at stage time.
	for _, s := range allSyms {
		warnIfOrdinalAnchor(stderr, s.File, s.Name)
	}

	dirty := report.Dirty()
	if !f.quiet {
		if f.porcelain {
			fmt.Fprint(stdout, diffpkg.RenderPorcelain(report))
		} else {
			fmt.Fprint(stdout, diffpkg.RenderText(report))
		}
		if f.patch {
			_, _ = stdout.Write(report.Patch)
		}
	}

	if (f.quiet || f.exitCode) && dirty {
		return diffCommittable
	}
	return exitcode.Success
}

// symRefsFromFlag parses --sym's repeatable FILE:NAME values. A malformed
// value is rejected rather than skipped: silently dropping it would leave
// the caller looking at an unfiltered diff believing it was filtered, and
// rgit commit already refuses the same value with the same message.
func symRefsFromFlag(syms []string) ([]diffpkg.SymRef, error) {
	out := make([]diffpkg.SymRef, 0, len(syms))
	for _, s := range syms {
		file, name, ok := splitAnchor(s)
		if !ok {
			return nil, fmt.Errorf("malformed --sym value %q", s)
		}
		out = append(out, diffpkg.SymRef{File: file, Name: name})
	}
	return out, nil
}

// extForFailedSym returns the file extension of a failed *resolve.
// ResolveError's own Path, for unsupportedLanguageHint's benefit.
// internal/diff/run.go's validateSym populates Path on this exact error
// (Code == exitcode.UnsupportedLanguage) precisely so this never has to
// recover the file by matching the failed anchor's bare name back against
// the --sym list, which is what this function used to do: two files
// sharing a bare symbol name resolved to whichever came first in that
// list. Path empty (ok=false) should not happen on this error, since
// validateSym always sets it before returning, but a caller with no other
// site to attach one from is still an honest "no hint" rather than a
// panic.
func extForFailedSym(rerr *resolve.ResolveError) (string, bool) {
	if rerr.Path == "" {
		return "", false
	}
	return filepath.Ext(rerr.Path), true
}
