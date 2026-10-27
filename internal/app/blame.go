// Package app implements the `rgit blame FILE:SYMBOL` command surface: git blame bounded to one
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

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/resolve"
)

const blameHelp = `usage: rgit blame FILE:SYMBOL [--source <rev>] [--follow-rename] [-p|--porcelain]

Blame bounded to one symbol's own extent: git blame -L over just the lines
the anchor resolves to, never the whole file. By default that extent is the
current worktree file. If the worktree file is gone, the default resolves
against its HEAD blob.
An anchor that does not resolve is exit 3 (or 4 if ambiguous, 9 if the
language has no grammar) -- never a silently widened, whole-file blame.

--source <rev>  Blame <rev>:FILE instead: the lines the anchor resolves to
                in that revision's blob, via the same read show --source
                uses. FILE is the path's current name. Combined with
                --follow-rename, the walk starts at <rev> and still
                re-resolves at each later rename boundary.
                An unknown revision is exit 128; a path absent at a known
                one is exit 3.
--follow-rename walks the file's rename history, re-resolving the symbol at
each rename boundary against committed blobs, never the dirty worktree.
Without it, blame stops at the current file name.

-p, --porcelain passes straight through to git's own "git blame --porcelain"
format (see docs/CODES.md#output-records) -- both spellings, matching git
blame's own flag exactly: unlike "git log -p" or "git diff -p", git blame's
"-p" already means "--porcelain", not "patch" (blame has no patch mode of
its own to opt into; it annotates lines, it does not diff them). The
default is git's own human-readable blame output, likewise unmodified.

Full reference: docs/USAGE.md
`

// runBlame's flag surface is one FILE:SYMBOL anchor plus --source,
// -p/--porcelain, and --follow-rename, hand-parsed via parseAnchorCommandArgs.
func runBlame(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	porcelain := false
	followRename := false
	source := ""
	positionals, code, done := parseAnchorCommandArgs("blame", args,
		[]anchorCommandFlag{
			{tokens: []string{"-p", "--porcelain"}, set: &porcelain},
			{tokens: []string{"--follow-rename"}, set: &followRename},
			{tokens: []string{"--source"}, val: &source},
		},
		1, blameHelp, stdout, stderr, false)
	if done {
		return code
	}
	positional := positionals[0]

	headOnly := false
	headFetch := showHeadSource()
	fetch := func(ctx context.Context, repo *gitx.Repo, root, file string) ([]byte, string, bool, error) {
		if followRename {
			src, detail, ok, err := headFetch(ctx, repo, root, file)
			if ok {
				headOnly = true
			}
			return src, detail, ok, err
		}
		src, err := readWorktreeFile(root, file)
		if err != nil {
			if os.IsNotExist(err) {
				src, exists, err := repo.CatFile(ctx, "HEAD", file)
				if err != nil {
					return nil, "", false, err
				}
				if exists {
					headOnly = true
					return src, "", true, nil
				}
				return nil, "no longer exists in the worktree", false, nil
			}
			return nil, "", false, err
		}
		return src, "", true, nil
	}
	if source != "" {
		fetch = showSource(source)
	}
	repo, file, src, res, anchorName, code := resolveAnchorExtent(ctx, dir, stderr, positional, "blame", blameHelp, fetch)
	if code != exitcode.Success {
		return code
	}
	warnIfOrdinalAnchor(stderr, file, anchorName)

	start, end := lineRange(src, res.Extent)

	var extra []string
	if porcelain {
		extra = append(extra, "--porcelain")
	}
	if followRename {
		startRev := "HEAD"
		if source != "" {
			startRev = source
		}
		return runBlameFollowRename(ctx, repo, startRev, file, src, res, anchorName, extra, stdout, stderr)
	}

	var out []byte
	var err error
	if source != "" || headOnly {
		rev := source
		if rev == "" {
			rev = "HEAD"
		}
		out, err = repo.BlameRevision(ctx, rev, file, start, end, extra...)
	} else {
		out, err = repo.Blame(ctx, file, start, end, extra...)
	}
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	_, _ = stdout.Write(out)
	return exitcode.Success
}

// runBlameFollowRename walks one path segment at a time, newest first. A
// segment's blame is bounded by git's normal path history; after the nearest
// rename, the next segment starts from the pre-rename path's parent commit and
// resolves the anchor against that blob.
func runBlameFollowRename(ctx context.Context, repo *gitx.Repo, startRev, file string, src []byte, res *resolve.Resolution, anchorName string, extra []string, stdout, stderr io.Writer) exitcode.Code {
	return walkRenameSegments(ctx, repo, "blame", startRev, file, anchorName, src, res, stderr, func(rev, file string, src []byte, res *resolve.Resolution, _ string, _ bool) exitcode.Code {
		start, end := lineRange(src, res.Extent)
		out, err := repo.BlameRevision(ctx, rev, file, start, end, extra...)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.GitFailure
		}
		_, _ = stdout.Write(out)
		return exitcode.Success
	})
}
