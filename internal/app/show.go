// Package app implements the `rgit show FILE:SYMBOL...` command surface: each named symbol's own
// bytes, verbatim, at the worktree or at a revision. It is a thin caller over
// the same anchor resolution blame and log use -- no new resolution
// machinery, and nothing is written. The extent it prints is byte-for-byte
// the extent commit would splice, which is the point: reading a symbol and
// staging it must never disagree about where it starts and ends.
package app

import (
	"bytes"
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

const showHelp = `usage: rgit show FILE:SYMBOL... [--source <rev>] [--follow-rename] [--porcelain] [--with-header]

Print each named symbol's own bytes -- exactly the extent the anchor resolves
to, with no trailing newline the file did not already carry.

--source <rev>  Read <rev>:FILE instead of the worktree, the only way to read
                a symbol as of a tag, branch, or commit. FILE is the path's
                current name. Mutually exclusive with --follow-rename (exit 129).
--follow-rename Re-resolves the symbol at each rename boundary and prints its
                bytes from the oldest path segment. With no rename, reads the
                HEAD blob rather than a dirty worktree. A plain path is refused
                (exit 129).
--porcelain     Frame every extent the way several are framed, so a script
                need not special-case an argument list that happens to hold
                one. Single-anchor and multi-anchor output are both framed;
                this is the machine-readable framing scripts should parse.
--with-header   Deprecated alias for --porcelain, retained so existing
                scripts keep working. Identical framing; prefer --porcelain
                in new scripts.

With one anchor and neither --porcelain nor --with-header, stdout is those bytes and nothing else,
so it pipes. With several, each extent is framed by a header line
"FILE:ANCHOR<TAB>NBYTES" followed by exactly NBYTES bytes: a symbol's own text
can contain anything, including a line that looks like a header, so the length
is what makes the stream unambiguous rather than a delimiter that could
collide.

By default a symbol is read from the worktree file, falling back to its HEAD
blob when the worktree copy is gone -- the same source blame resolves against,
so what this prints is what commit would stage.

Every anchor is resolved before any byte is written, so a failure anywhere in
the list leaves stdout untouched rather than half a stream. An anchor that
does not resolve is exit 3 (or 4 if ambiguous, 9 if the language has no
grammar) -- never a silently widened whole-file dump.

Full reference: docs/USAGE.md
`

// runShow resolves every named anchor and writes their extents to stdout.
// It shares blame's and log's own arg loop rather than reaching for pflag,
// so every usage refusal -- a bare pathspec, a lone "--" -- is worded
// identically across all three.
func runShow(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	source := ""
	withHeader := false
	porcelain := false
	followRename := false
	positionals, code, done := parseAnchorCommandArgs("show", args,
		[]anchorCommandFlag{
			{tokens: []string{"--source"}, val: &source},
			{tokens: []string{"--porcelain"}, set: &porcelain},
			{tokens: []string{"--with-header"}, set: &withHeader},
			{tokens: []string{"--follow-rename"}, set: &followRename},
		},
		0, showHelp, stdout, stderr)
	if done {
		return code
	}
	if source != "" && followRename {
		fmt.Fprintln(stderr, "rgit: --source and --follow-rename are mutually exclusive")
		fmt.Fprint(stderr, showHelp)
		return exitcode.InvalidUsage
	}

	// Resolved in full before anything is written, the same "resolve every
	// target before staging any" rule commit's own staging follows: a
	// half-written stream is worse than none, because a caller reading it
	// cannot tell a truncated extent from a complete one.
	fetch := showSource(source)
	if followRename {
		fetch = showHeadSource()
	}
	extents := make([][]byte, 0, len(positionals))
	for _, positional := range positionals {
		repo, file, src, res, anchorName, code := resolveAnchorExtent(ctx, dir, stderr, positional, "show", showHelp, fetch)
		if code != exitcode.Success {
			return code
		}
		warnIfOrdinalAnchor(stderr, file, anchorName)
		ext := src[res.Extent.Start:res.Extent.End]
		if followRename {
			ext, code = followRenameSymbol(ctx, repo, file, src, res, anchorName, stderr)
			if code != exitcode.Success {
				return code
			}
		}
		extents = append(extents, ext)
	}

	framed := porcelain || withHeader || len(extents) > 1
	var out bytes.Buffer
	for i, ext := range extents {
		if framed {
			fmt.Fprintf(&out, "%s\t%d\n", positionals[i], len(ext))
		}
		out.Write(ext)
	}
	if _, err := stdout.Write(out.Bytes()); err != nil {
		fmt.Fprintf(stderr, "rgit: cannot write: %v\n", err)
		return exitcode.GitFailure
	}
	return exitcode.Success
}

// showHeadSource reads the HEAD blob only. Rename history is committed
// content: a dirty worktree must not supply the extent --follow-rename walks.
func showHeadSource() anchorSourceFunc {
	return func(ctx context.Context, repo *gitx.Repo, _, file string) ([]byte, string, bool, error) {
		blob, exists, err := repo.CatFile(ctx, "HEAD", file)
		if err != nil {
			return nil, "", false, err
		}
		if !exists {
			return nil, "has no HEAD history", false, nil
		}
		return blob, "", true, nil
	}
}

// followRenameSymbol walks file's rename history via gitx.FindRename and
// returns the symbol's bytes on the oldest path segment. Each boundary is
// one re-resolve against the pre-rename blob, the same segment walk blame
// and log use. With no rename, the HEAD extent the caller already resolved
// is returned unchanged.
func followRenameSymbol(ctx context.Context, repo *gitx.Repo, file string, src []byte, res *resolve.Resolution, anchorName string, stderr io.Writer) ([]byte, exitcode.Code) {
	ignoreCase, err := repo.IgnoreCase(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot read core.ignorecase: %v\n", err)
		return nil, exitcode.GitFailure
	}

	rev := "HEAD"
	for {
		renameCommit, oldPath, found, err := repo.FindRename(ctx, rev, file)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return nil, exitcode.GitFailure
		}
		if !found {
			return src[res.Extent.Start:res.Extent.End], exitcode.Success
		}

		rev = renameCommit + "~1"
		file = oldPath
		var exists bool
		src, exists, err = repo.CatFile(ctx, rev, file)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return nil, exitcode.GitFailure
		}
		if !exists {
			fmt.Fprintf(stderr, "rgit: show: %q does not exist at %s\n", file, rev)
			return nil, exitcode.AnchorUnresolvable
		}
		// The blob is read before the language lookup so the name a file
		// carried before the rename gets the shebang match docs/ANCHORS.md
		// promises. Keying on the extension alone stranded an extensionless
		// script mid-history: the current name resolved, the old one did not.
		lang, ok := resolve.ForPathFolding(file, src, ignoreCase)
		if !ok {
			fmt.Fprintf(stderr, "rgit: show: %q: unsupported language before the rename to its current name%s\n", file, unsupportedLanguageHint(filepath.Ext(file)))
			return nil, exitcode.UnsupportedLanguage
		}
		res, err = resolve.Resolve(lang, src, anchorName)
		if err != nil {
			if rerr, ok := errors.AsType[*resolve.ResolveError](err); ok {
				fmt.Fprintf(stderr, "rgit: %s\n", rerr.Error())
				return nil, rerr.Code
			}
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return nil, exitcode.GitFailure
		}
	}
}

// showSource builds the anchorSourceFunc for one --source value: a revision's
// blob when given, otherwise the worktree with blame's own HEAD fallback.
func showSource(source string) anchorSourceFunc {
	return func(ctx context.Context, repo *gitx.Repo, root, file string) ([]byte, string, bool, error) {
		if source != "" {
			// Verified before the read, because cat-file reports a typo'd
			// revision and a path genuinely absent at a real one
			// identically -- both are simply "no such object". Without
			// this, `--source mian` would read as "your symbol is not in
			// that commit" rather than "that is not a commit".
			if _, ok, err := repo.RevParseVerify(ctx, source); err != nil {
				return nil, "", false, err
			} else if !ok {
				return nil, "", false, fmt.Errorf("not a valid revision: %s", source)
			}
			blob, exists, err := repo.CatFile(ctx, source, file)
			if err != nil {
				return nil, "", false, err
			}
			if !exists {
				return nil, fmt.Sprintf("is absent at %s", source), false, nil
			}
			return blob, "", true, nil
		}
		blob, err := readWorktreeFile(root, file)
		if err == nil {
			return blob, "", true, nil
		}
		if !os.IsNotExist(err) {
			return nil, "", false, err
		}
		blob, exists, err := repo.CatFile(ctx, "HEAD", file)
		if err != nil {
			return nil, "", false, err
		}
		if !exists {
			return nil, "is absent from the worktree and has no HEAD history", false, nil
		}
		return blob, "", true, nil
	}
}
