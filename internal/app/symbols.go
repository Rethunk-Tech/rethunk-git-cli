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

const symbolsHelp = `usage: rgit symbols [--for-commit] <file>

List every declared symbol that can be resolved from the worktree file.
--for-commit  Omit structured-data symbols that commit refuses.
Full reference: docs/USAGE.md
`

func runSymbols(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	forCommit := false
	positionals := make([]string, 0, 1)
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			fmt.Fprint(stdout, symbolsHelp)
			return exitcode.Success
		case "--for-commit":
			forCommit = true
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) != 1 {
		fmt.Fprintln(stderr, "rgit: symbols requires exactly one file argument")
		fmt.Fprint(stderr, symbolsHelp)
		return exitcode.InvalidUsage
	}

	root, prefix, _, code := openRepo(ctx, dir, stderr)
	if code != exitcode.Success {
		return code
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

	src, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot read %q: %v\n", positionals[0], err)
		return exitcode.GitFailure
	}

	lang, ok, _ := resolve.LanguageForWorktreePath(root, path)
	if !ok {
		fmt.Fprintf(stderr, "rgit: unsupported language for %q\n", positionals[0])
		return exitcode.UnsupportedLanguage
	}
	if forCommit && resolve.IsStructuredData(lang) {
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
