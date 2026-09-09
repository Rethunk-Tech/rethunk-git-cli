// The `rgit show FILE:SYMBOL` command surface: one symbol's own bytes,
// verbatim, at the worktree or at a revision. It is a thin caller over the
// same anchor resolution blame and log use -- no new resolution machinery,
// and nothing is written. The extent it prints is byte-for-byte the extent
// commit would splice, which is the point: reading a symbol and staging it
// must never disagree about where it starts and ends.
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

const showHelp = `usage: rgit show FILE:SYMBOL [--source <rev>]

Print one symbol's own bytes and nothing else -- exactly the extent the
anchor resolves to, with no header, no decoration, and no trailing newline
the file did not already carry. Suitable for piping.

By default the symbol is read from the worktree file, falling back to its
HEAD blob when the worktree copy is gone -- the same source blame resolves
against, so what this prints is what commit would stage.

--source <rev> reads <rev>:FILE instead, which is the only way to read a
symbol as of a tag, branch, or commit. FILE is always the path's *current*
name; this does not follow renames.

An anchor that does not resolve is exit 3 (or 4 if ambiguous, 9 if the
language has no grammar) -- never a silently widened whole-file dump.

Full reference: docs/USAGE.md
`

// runShow resolves one FILE:SYMBOL anchor and writes its extent to stdout.
// It shares blame's and log's own arg loop rather than reaching for pflag,
// so every usage refusal -- a bare pathspec, a second positional, a lone
// "--" -- is worded identically across all three.
func runShow(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	source := ""
	positional, code, done := parseAnchorCommandArgs("show", args,
		[]anchorCommandFlag{{tokens: []string{"--source"}, val: &source}},
		showHelp, stdout, stderr)
	if done {
		return code
	}

	_, file, src, res, anchorName, code := resolveAnchorExtent(ctx, dir, stderr, positional, "show", showHelp,
		func(ctx context.Context, repo *gitx.Repo, root, file string) ([]byte, string, bool, error) {
			if source != "" {
				// Verified before the read, because cat-file reports a
				// typo'd revision and a path genuinely absent at a real
				// one identically -- both are simply "no such object".
				// Without this, `--source mian` would read as "your symbol
				// is not in that commit" rather than "that is not a
				// commit".
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
			blob, err := os.ReadFile(filepath.Join(root, file))
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
		})
	if code != exitcode.Success {
		return code
	}
	warnIfOrdinalAnchor(stderr, file, anchorName)

	if _, err := stdout.Write(src[res.Extent.Start:res.Extent.End]); err != nil {
		fmt.Fprintf(stderr, "rgit: cannot write %q: %v\n", positional, err)
		return exitcode.GitFailure
	}
	return exitcode.Success
}
