// The `rgit blame FILE:SYMBOL` command surface: git blame bounded to one
// symbol's own extent instead of a whole file. It is a thin caller over
// anchor resolution and `git blame -L` -- no new resolution machinery, no
// second attribution path. TODO.md's own guardrail is non-negotiable: an
// anchor that does not resolve is an error, never a silently widened
// whole-file blame.
package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const blameHelp = `usage: rgit blame FILE:SYMBOL [--porcelain]

Blame bounded to one symbol's own extent: git blame -L over just the lines
the anchor resolves to in the current worktree file, never the whole file.
An anchor that does not resolve is exit 3 (or 4 if ambiguous, 9 if the
language has no grammar) -- never a silently widened, whole-file blame.

--porcelain passes straight through to git's own "git blame --porcelain"
format (see docs/CODES.md#output-records); the default is git's own
human-readable blame output, likewise unmodified.

Full reference: docs/USAGE.md
`

// runBlame's flag surface is exactly rgit languages': one positional
// FILE:SYMBOL anchor plus --porcelain, hand-parsed rather than pulling in
// pflag for a command this small (languages.go's own precedent).
func runBlame(ctx context.Context, args []string, stdout, stderr io.Writer) exitcode.Code {
	porcelain := false
	var positional string
	havePositional := false
	for _, a := range args {
		switch {
		case a == "--help" || a == "-h":
			fmt.Fprint(stdout, blameHelp)
			return exitcode.Success
		case a == "--porcelain":
			porcelain = true
		case havePositional:
			fmt.Fprintf(stderr, "rgit: blame: unrecognized argument %q\n", a)
			fmt.Fprint(stderr, blameHelp)
			return exitcode.InvalidUsage
		case a != "--" && strings.HasPrefix(a, "-"):
			// A "-"-prefixed token that is none of the flags above must be
			// refused here, before it can fall through to the default case
			// and be silently treated as the positional (e.g. an unknown
			// "--foo" becoming the FILE:SYMBOL anchor itself). "--" is
			// exempt: it is a legitimate bare positional here, refused by
			// resolveAnchorExtent's own classification instead (a bare "--"
			// classifies to nothing).
			fmt.Fprintf(stderr, "rgit: blame: unrecognized argument %q\n", a)
			fmt.Fprint(stderr, blameHelp)
			return exitcode.InvalidUsage
		default:
			positional = a
			havePositional = true
		}
	}
	if !havePositional {
		fmt.Fprintln(stderr, "rgit: blame requires a FILE:SYMBOL anchor")
		fmt.Fprint(stderr, blameHelp)
		return exitcode.InvalidUsage
	}

	// Blame operates on the worktree file, not HEAD: there is nothing to
	// resolve or blame in a revision this command never names.
	repo, file, src, res, code := resolveAnchorExtent(ctx, stderr, positional, "blame", blameHelp,
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

// lineRange converts ext's byte offsets in src to the 1-based, inclusive
// line range git's own `blame -L start,end` wants. ext.End is exclusive
// and a tree-sitter node's own EndByte() never includes a trailing
// newline, so the extent's last real byte sits at End-1, not End -- using
// End directly would pull the following line into the blamed range
// whenever the extent's own content ends exactly at a line boundary.
func lineRange(src []byte, ext resolve.Extent) (start, end int) {
	startLine := bytes.Count(src[:ext.Start], []byte{'\n'})
	endOffset := ext.End
	if endOffset > ext.Start {
		endOffset--
	}
	endLine := bytes.Count(src[:endOffset], []byte{'\n'})
	return startLine + 1, endLine + 1
}
