// The `rgit diff` command surface: flag parsing, validation, and dispatch
// into internal/diff. Flags and exit codes are specified in docs/USAGE.md.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	diffpkg "github.com/Rethunk-Tech/rethunk-git-cli/internal/diff"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
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
	syms      []string
	files     []string
}

func runDiff(ctx context.Context, args []string, stdout, stderr io.Writer) exitcode.Code {
	var f diffFlags
	fs := newTargetFlagSet("diff", &f.syms, &f.files)
	fs.BoolVar(&f.unstaged, "unstaged", false, "worktree vs index (git's bare diff)")
	fs.BoolVar(&f.staged, "staged", false, "index vs HEAD")
	fs.BoolVar(&f.staged, "cached", false, "alias for --staged")
	fs.StringVar(&f.rangeFlag, "range", "", "explicit form of a positional revision range")
	fs.BoolVar(&f.porcelain, "porcelain", false, "stable tab-separated records")
	fs.BoolVar(&f.exitCode, "exit-code", false, "exit 1 when anything is committable")
	fs.BoolVar(&f.quiet, "quiet", false, "implies --exit-code and suppresses output")

	if code := parseFlags(fs, args, stderr); code != exitcode.Success {
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

	root, prefix, repo, code := openRepo(ctx, stderr)
	if code != exitcode.Success {
		return code
	}

	checker := cli.GitPathChecker{Root: root, Prefix: prefix, Repo: repo, Ctx: ctx}

	rangeToken, rest, err := diffpkg.ExtractRangeToken(restoreDoubleDash(fs), checker)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}

	classified, err := cli.ClassifyArgs(rest, true, checker, cli.GitRevisionResolver{Repo: repo, Ctx: ctx})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}

	revisions, files, syms, err := diffpkg.BucketClassified(classified)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
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
	}

	report, err := diffpkg.Run(ctx, repo, root, opts)
	if err != nil {
		var uerr *diffpkg.UsageError
		if errors.As(err, &uerr) {
			fmt.Fprintf(stderr, "rgit: %v\n", uerr)
			return exitcode.InvalidUsage
		}
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}

	dirty := report.Dirty()
	if !f.quiet {
		if f.porcelain {
			fmt.Fprint(stdout, diffpkg.RenderPorcelain(report))
		} else {
			fmt.Fprint(stdout, diffpkg.RenderText(report))
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
