package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const symbolsHelp = `usage: rgit symbols [--for-commit] [--with-lines] <file>

List every declared symbol that can be resolved from the worktree file or its HEAD blob.
--for-commit  Omit structured-data symbols that commit refuses.
--with-lines  Emit "start,end<TAB>symbol" instead of the bare name, where the
              range is git's own -L range for that symbol -- the same range
              blame and log bound themselves to.

Full reference: docs/USAGE.md
`

func runSymbols(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	forCommit := false
	withLines := false
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
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) != 1 {
		fmt.Fprintln(stderr, "rgit: symbols requires exactly one file argument")
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

	path := positionals[0]
	if filepath.IsAbs(path) {
		var err error
		path, err = filepath.Rel(root, path)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: cannot resolve file %q: %v\n", positionals[0], err)
			return exitcode.GitFailure
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
		return exitcode.InvalidUsage
	}

	src, err := os.ReadFile(filepath.Join(root, path))
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
			fmt.Fprintf(stderr, "rgit: cannot read %q: %v\n", positionals[0], err)
			return exitcode.GitFailure
		}
	}

	lang, ok, _, err := resolve.LanguageForPathFolding(root, path, ignoreCase, func() ([]byte, bool, error) {
		return headSrc, headExists, nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot read %q: %v\n", positionals[0], err)
		return exitcode.GitFailure
	}
	if !ok {
		fmt.Fprintf(stderr, "rgit: unsupported language for %q%s\n", positionals[0], unsupportedLanguageHint(filepath.Ext(path)))
		return exitcode.UnsupportedLanguage
	}
	if forCommit && resolve.IsStructuredData(lang) {
		return exitcode.Success
	}

	if withLines {
		decls, err := resolve.DeclExtents(lang, src)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: cannot resolve symbols in %q: %v\n", positionals[0], err)
			return exitcode.GitFailure
		}
		for _, decl := range decls {
			// lineRange, not a second conversion: blame and log turn the
			// same extent into the same range through it.
			start, end := lineRange(src, decl.Extent)
			fmt.Fprintf(stdout, "%d,%d\t%s\n", start, end, decl.Anchor)
		}
		return exitcode.Success
	}

	symbols, err := resolve.DeclOrder(lang, src)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot resolve symbols in %q: %v\n", positionals[0], err)
		return exitcode.GitFailure
	}
	for _, symbol := range symbols {
		fmt.Fprintln(stdout, symbol)
	}
	return exitcode.Success
}
