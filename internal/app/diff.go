// The `rgit diff` command surface: flag parsing, validation, and dispatch
// into internal/diff. Flags and exit codes are specified in docs/USAGE.md.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/pflag"

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
	checker := cli.GitPathChecker{Root: root, Repo: repo, Ctx: ctx}

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

	allFiles := append(files, f.files...)
	allSyms := append(syms, symFlags...)
	for _, p := range allFiles {
		if err := checkPathEscape(root, p); err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.InvalidUsage
		}
	}
	for _, s := range allSyms {
		if err := checkPathEscape(root, s.File); err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.InvalidUsage
		}
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
