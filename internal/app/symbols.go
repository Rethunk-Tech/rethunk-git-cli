package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const symbolsHelp = `usage: rgit symbols [--for-commit] [--with-lines] [--with-filename] [--porcelain] <file>... (parsers: use --porcelain for NUL-terminated records)

List every declared symbol that can be resolved from each worktree file or its
HEAD blob.
--for-commit     Omit structured-data symbols that commit refuses.
--with-lines     Emit "start,end<TAB>symbol" instead of the bare name, where
                 the range is git's own -L range for that symbol -- the same
                 range blame and log bound themselves to.
--with-filename  Prefix every line with "FILE<TAB>", using the path exactly as
                 given so it composes straight back into a FILE:SYMBOL anchor.
                 Implied by naming more than one file, the way grep prefixes
                 only when several are named; pass it so a script need not
                 special-case an argument list that happens to hold one.
--porcelain      Terminate each record with NUL instead of a newline. The
                 fields are unchanged; only the record separator moves, so a
                 symbol may contain any byte but NUL -- which source text
                 cannot, a file carrying one being binary and refused. Use it
                 wherever the listing is parsed rather than read.

Every file is resolved before any line is written, so an unreadable or
unsupported file anywhere in the list leaves stdout untouched rather than half
a listing.

Full reference: docs/USAGE.md
`

func runSymbols(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	forCommit := false
	withLines := false
	withFilename := false
	porcelain := false
	positionals := make([]string, 0, 1)
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			fmt.Fprint(stdout, symbolsHelp)
			return exitcode.Success
		case "--for-commit":
			forCommit = true
		case "--with-lines":
			withLines = true
		case "--with-filename":
			withFilename = true
		case "--porcelain":
			porcelain = true
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) == 0 {
		fmt.Fprintln(stderr, "rgit: symbols requires at least one file argument")
		fmt.Fprint(stderr, symbolsHelp)
		return exitcode.InvalidUsage
	}

	root, prefix, repo, code := openRepo(ctx, dir, stderr)
	if code != exitcode.Success {
		return code
	}
	ignoreCase, err := repo.IgnoreCase(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot read core.ignorecase: %v\n", err)
		return exitcode.GitFailure
	}

	// Every file is resolved before anything is written, matching show's own
	// all-or-nothing rule: a listing cut off midway reads as "that file has
	// no more symbols", which is exactly the wrong conclusion.
	listings := make([][]string, 0, len(positionals))
	for _, positional := range positionals {
		lines, code := symbolLines(ctx, root, prefix, repo, ignoreCase, positional, forCommit, withLines, stderr)
		if code != exitcode.Success {
			return code
		}
		listings = append(listings, lines)
	}

	showName := withFilename || len(positionals) > 1
	// The record separator is the only thing --porcelain moves. Every field
	// stays where it was, and the symbol stays last, so a parser splits on
	// NUL for records and on the first tabs for fields.
	end := "\n"
	if porcelain {
		end = "\x00"
	}
	for i, lines := range listings {
		for _, line := range lines {
			if showName {
				fmt.Fprintf(stdout, "%s\t%s%s", positionals[i], line, end)
				continue
			}
			fmt.Fprintf(stdout, "%s%s", line, end)
		}
	}
	return exitcode.Success
}

// symbolLines is one file's worth of runSymbols: resolve the path, pick the
// grammar, and render either bare anchors or git's own -L ranges. Split out
// so the multi-file loop resolves every file before the first line is
// written; every refusal has already been reported to stderr when code is
// not Success.
func symbolLines(ctx context.Context, root, prefix string, repo *gitx.Repo, ignoreCase bool, positional string, forCommit, withLines bool, stderr io.Writer) ([]string, exitcode.Code) {
	path := positional
	if filepath.IsAbs(path) {
		var err error
		path, err = filepath.Rel(root, path)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: cannot resolve file %q: %v\n", positional, err)
			return nil, exitcode.GitFailure
		}
	} else {
		path = filepath.Join(prefix, path)
	}
	path = filepath.Clean(path)
	// The absolute-path branch above is why this cannot simply call
	// repoPath: PrefixPath has no notion of an absolute argument, so
	// routing through it would drop the Rel normalization that lets
	// `rgit symbols /abs/file.go` work from a subdirectory. The escape
	// check is the half of repoPath that must not be skipped -- without
	// it symbols reads files above root that diff and blame refuse.
	if err := checkPathEscape(root, path); err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return nil, exitcode.InvalidUsage
	}

	src, err := readWorktreeFile(root, path)
	var headSrc []byte
	headExists := false
	if err != nil {
		if os.IsNotExist(err) {
			worktreeErr := err
			headSrc, headExists, err = repo.CatFile(ctx, "HEAD", path)
			if err == nil && headExists {
				src = headSrc
			} else if err == nil {
				err = worktreeErr
			}
		}
		if err != nil || !headExists {
			fmt.Fprintf(stderr, "rgit: cannot read %q: %v\n", positional, err)
			return nil, exitcode.GitFailure
		}
	}

	lang, ok, _, err := resolve.LanguageForPathFolding(root, path, ignoreCase, func() ([]byte, bool, error) {
		return headSrc, headExists, nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot read %q: %v\n", positional, err)
		return nil, exitcode.GitFailure
	}
	if !ok {
		fmt.Fprintf(stderr, "rgit: unsupported language for %q%s\n", positional, unsupportedLanguageHint(filepath.Ext(path)))
		return nil, exitcode.UnsupportedLanguage
	}
	if forCommit && resolve.IsStructuredData(lang) {
		return nil, exitcode.Success
	}

	if withLines {
		decls, err := resolve.DeclExtents(lang, src)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: cannot resolve symbols in %q: %v\n", positional, err)
			return nil, exitcode.GitFailure
		}
		lines := make([]string, 0, len(decls))
		for _, decl := range decls {
			// lineRange, not a second conversion: blame and log turn the
			// same extent into the same range through it.
			start, end := lineRange(src, decl.Extent)
			lines = append(lines, fmt.Sprintf("%d,%d\t%s", start, end, decl.Anchor))
		}
		return lines, exitcode.Success
	}

	symbols, err := resolve.DeclOrder(lang, src)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot resolve symbols in %q: %v\n", positional, err)
		return nil, exitcode.GitFailure
	}
	return symbols, exitcode.Success
}
