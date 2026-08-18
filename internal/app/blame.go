// The `rgit blame FILE:SYMBOL` command surface: git blame bounded to one
// symbol's own extent instead of a whole file. It is a thin caller over
// anchor resolution and `git blame -L` -- no new resolution machinery, no
// second attribution path. docs/USAGE.md § Blame's own guardrail is
// non-negotiable: an anchor that does not resolve is an error, never a
// silently widened whole-file blame.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const blameHelp = `usage: rgit blame FILE:SYMBOL [--follow-rename] [-p|--porcelain]

Blame bounded to one symbol's own extent: git blame -L over just the lines
the anchor resolves to in the current worktree file, never the whole file.
If the worktree file is gone, the default resolves against its HEAD blob.
An anchor that does not resolve is exit 3 (or 4 if ambiguous, 9 if the
language has no grammar) -- never a silently widened, whole-file blame.

--follow-rename walks the file's rename history, re-resolving the symbol at
each rename boundary against the HEAD blob, never the dirty worktree.
Without it, blame stops at the current file name.

-p, --porcelain passes straight through to git's own "git blame --porcelain"
format (see docs/CODES.md#output-records) -- both spellings, matching git
blame's own flag exactly: unlike "git log -p" or "git diff -p", git blame's
"-p" already means "--porcelain", not "patch" (blame has no patch mode of
its own to opt into; it annotates lines, it does not diff them). The
default is git's own human-readable blame output, likewise unmodified.

Full reference: docs/USAGE.md
`

// runBlame's flag surface is one FILE:SYMBOL anchor plus -p/--porcelain and
// --follow-rename, hand-parsed via parseAnchorCommandArgs.
func runBlame(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	porcelain := false
	followRename := false
	positional, code, done := parseAnchorCommandArgs("blame", args,
		[]anchorCommandFlag{
			{tokens: []string{"-p", "--porcelain"}, set: &porcelain},
			{tokens: []string{"--follow-rename"}, set: &followRename},
		},
		blameHelp, stdout, stderr)
	if done {
		return code
	}

	headOnly := false
	repo, file, src, res, anchorName, code := resolveAnchorExtent(ctx, dir, stderr, positional, "blame", blameHelp,
		func(ctx context.Context, repo *gitx.Repo, root, file string) ([]byte, string, bool, error) {
			if followRename {
				src, exists, err := repo.CatFile(ctx, "HEAD", file)
				if err != nil {
					return nil, "", false, err
				}
				if !exists {
					return nil, "has no HEAD history", false, nil
				}
				headOnly = true
				return src, "", true, nil
			}
			src, err := os.ReadFile(filepath.Join(root, file))
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
					return nil, "is absent from the worktree", false, nil
				}
				return nil, "", false, err
			}
			return src, "", true, nil
		})
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
		return runBlameFollowRename(ctx, repo, file, src, res, anchorName, extra, stdout, stderr)
	}

	var out []byte
	var err error
	if headOnly {
		out, err = repo.BlameRevision(ctx, "HEAD", file, start, end, extra...)
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
func runBlameFollowRename(ctx context.Context, repo *gitx.Repo, file string, src []byte, res *resolve.Resolution, anchorName string, extra []string, stdout, stderr io.Writer) exitcode.Code {
	ignoreCase, err := repo.IgnoreCase(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot read core.ignorecase: %v\n", err)
		return exitcode.GitFailure
	}

	rev := "HEAD"
	for {
		start, end := lineRange(src, res.Extent)

		renameCommit, oldPath, found, err := repo.FindRename(ctx, rev, file)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.GitFailure
		}

		out, err := repo.BlameRevision(ctx, rev, file, start, end, extra...)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.GitFailure
		}
		_, _ = stdout.Write(out)

		if !found {
			return exitcode.Success
		}

		rev = renameCommit + "~1"
		file = oldPath
		lang, ok := resolve.ForExtensionFolding(filepath.Ext(file), ignoreCase)
		if !ok {
			fmt.Fprintf(stderr, "rgit: blame: %q: unsupported language before the rename to its current name\n", file)
			return exitcode.UnsupportedLanguage
		}
		src, exists, err := repo.CatFile(ctx, rev, file)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.GitFailure
		}
		if !exists {
			fmt.Fprintf(stderr, "rgit: blame: %q does not exist at %s\n", file, rev)
			return exitcode.AnchorUnresolvable
		}
		res, err = resolve.Resolve(lang, src, anchorName)
		if err != nil {
			if rerr, ok := errors.AsType[*resolve.ResolveError](err); ok {
				fmt.Fprintf(stderr, "rgit: %s\n", rerr.Error())
				return rerr.Code
			}
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.GitFailure
		}
	}
}
