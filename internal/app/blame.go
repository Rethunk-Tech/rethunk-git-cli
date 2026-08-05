// The `rgit blame FILE:SYMBOL` command surface: git blame bounded to one
// symbol's own extent instead of a whole file. It is a thin caller over
// anchor resolution and `git blame -L` -- no new resolution machinery, no
// second attribution path. docs/USAGE.md § Blame's own guardrail is
// non-negotiable: an anchor that does not resolve is an error, never a
// silently widened whole-file blame.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

const blameHelp = `usage: rgit blame FILE:SYMBOL [-p|--porcelain]

Blame bounded to one symbol's own extent: git blame -L over just the lines
the anchor resolves to in the current worktree file, never the whole file.
An anchor that does not resolve is exit 3 (or 4 if ambiguous, 9 if the
language has no grammar) -- never a silently widened, whole-file blame.

-p, --porcelain passes straight through to git's own "git blame --porcelain"
format (see docs/CODES.md#output-records) -- both spellings, matching git
blame's own flag exactly: unlike "git log -p" or "git diff -p", git blame's
"-p" already means "--porcelain", not "patch" (blame has no patch mode of
its own to opt into; it annotates lines, it does not diff them). The
default is git's own human-readable blame output, likewise unmodified.

Full reference: docs/USAGE.md
`

// runBlame's flag surface is exactly rgit languages': one positional
// FILE:SYMBOL anchor plus -p/--porcelain, hand-parsed via shared.go's
// parseAnchorCommandArgs rather than pulling in pflag for a command this
// small (languages.go's own precedent).
func runBlame(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	porcelain := false
	positional, code, done := parseAnchorCommandArgs("blame", args,
		[]anchorCommandFlag{{tokens: []string{"-p", "--porcelain"}, set: &porcelain}},
		blameHelp, stdout, stderr)
	if done {
		return code
	}

	// Blame operates on the worktree file, not HEAD: there is nothing to
	// resolve or blame in a revision this command never names.
	repo, file, src, res, code := resolveAnchorExtent(ctx, dir, stderr, positional, "blame", blameHelp,
		func(_ context.Context, _ *gitx.Repo, root, file string) ([]byte, string, bool, error) {
			src, err := os.ReadFile(filepath.Join(root, file))
			if err != nil {
				if os.IsNotExist(err) {
					return nil, "no longer exists in the worktree", false, nil
				}
				return nil, "", false, err
			}
			return src, "", true, nil
		})
	if code != exitcode.Success {
		return code
	}

	start, end := lineRange(src, res.Extent)

	var extra []string
	if porcelain {
		extra = append(extra, "--porcelain")
	}
	out, err := repo.Blame(ctx, file, start, end, extra...)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	_, _ = stdout.Write(out)
	return exitcode.Success
}
