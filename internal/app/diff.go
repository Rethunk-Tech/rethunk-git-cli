// The `rgit diff` command surface: flag parsing, validation, and dispatch
// into internal/diff. Flags and exit codes are specified in docs/USAGE.md.
package app

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/pflag"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

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
	classified, err := cli.ClassifyArgs(restoreDoubleDash(fs), true, cli.GitPathChecker{Root: root, Repo: repo, Ctx: ctx}, cli.GitRevisionResolver{Repo: repo, Ctx: ctx})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}
	_ = classified // rendering (--porcelain, MODE, BINARY rows) is internal/diff's job

	fmt.Fprintln(stderr, "rgit: diff execution is not implemented yet (Phase 1 spine only)")
	return NotImplemented
}
