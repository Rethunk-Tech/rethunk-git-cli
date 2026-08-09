package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const symbolsHelp = `usage: rgit symbols <file>

List every declared symbol that can be resolved from the worktree file.
`

func runSymbols(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			fmt.Fprint(stdout, symbolsHelp)
			return exitcode.Success
		}
	}
	if len(args) != 1 {
		fmt.Fprintln(stderr, "rgit: symbols requires exactly one file argument")
		fmt.Fprint(stderr, symbolsHelp)
		return exitcode.InvalidUsage
	}

	root, prefix, _, code := openRepo(ctx, dir, stderr)
	if code != exitcode.Success {
		return code
	}

	path := args[0]
	if filepath.IsAbs(path) {
		var err error
		path, err = filepath.Rel(root, path)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: cannot resolve file %q: %v\n", args[0], err)
			return exitcode.GitFailure
		}
	} else {
		path = filepath.Join(prefix, path)
	}
	path = filepath.Clean(path)

	src, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot read %q: %v\n", args[0], err)
		return exitcode.GitFailure
	}

	lang, ok, _ := resolve.LanguageForWorktreePath(root, path)
	if !ok {
		fmt.Fprintf(stderr, "rgit: unsupported language for %q\n", args[0])
		return exitcode.UnsupportedLanguage
	}
	switch strings.ToLower(lang.Name()) {
	case "json", "yaml", "toml":
		fmt.Fprintf(stderr, "rgit: symbol anchors are not supported for %q\n", args[0])
		return exitcode.UnsupportedLanguage
	}

	symbols, err := resolve.DeclOrder(lang, src)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot resolve symbols in %q: %v\n", args[0], err)
		return exitcode.GitFailure
	}
	for _, symbol := range symbols {
		fmt.Fprintln(stdout, symbol)
	}
	return exitcode.Success
}
