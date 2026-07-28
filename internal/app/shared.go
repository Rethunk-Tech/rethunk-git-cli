package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/pflag"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

// anchorFileContradiction implements docs/USAGE.md's "--sym and --file on
// the same path → exit 5": naming a path both ways is a contradiction the
// caller must resolve, not a case rgit could silently pick a side on.
func anchorFileContradiction(syms, files []string, stderr io.Writer) exitcode.Code {
	fileSet := make(map[string]bool, len(files))
	for _, path := range files {
		fileSet[path] = true
	}
	for _, sym := range syms {
		idx := strings.LastIndexByte(sym, ':')
		if idx <= 0 {
			continue // malformed --sym value; not this check's job to diagnose
		}
		file := sym[:idx]
		if fileSet[file] {
			fmt.Fprintf(stderr, "rgit: --sym and --file both name %q\n", file)
			return exitcode.ContradictoryAnchors
		}
	}
	return exitcode.Success
}

// conventionalShapeRe is a loose match for "type(scope): subject" and
// "type(scope)!: subject" — just enough to decide whether the warning in
// docs/USAGE.md ("A missing conventional-commit shape ... warns on
// stderr; the commit proceeds") should fire. It is advisory only.
var conventionalShapeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*(\([^()]+\))?!?: .+`)

func hasConventionalShape(messages []string) bool {
	if len(messages) == 0 {
		// -F was used instead; the message lives in a file the execution
		// step reads, not something this parse-only phase inspects.
		return true
	}
	return conventionalShapeRe.MatchString(messages[0])
}

// restoreDoubleDash reconstructs the "--" separator pflag consumes
// during Parse. fs.Args() alone loses the fact that "--" was ever
// present, but cli.ClassifyArgs's rule 1 ("everything after -- is a
// pathspec, always") needs to see it to force those positionals rather
// than running them through the other five rules.
func restoreDoubleDash(fs *pflag.FlagSet) []string {
	args := fs.Args()
	dashAt := fs.ArgsLenAtDash()
	if dashAt < 0 {
		return args
	}
	withDash := make([]string, 0, len(args)+1)
	withDash = append(withDash, args[:dashAt]...)
	withDash = append(withDash, "--")
	withDash = append(withDash, args[dashAt:]...)
	return withDash
}

// openRepo resolves the current working directory's git toplevel and
// returns a Repo rooted there. Every subcommand needs this before
// classification, since rules 3-5 all query git or the filesystem.
func openRepo(stderr io.Writer) (root string, repo *gitx.Repo, code exitcode.Code) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return "", nil, exitcode.GitFailure
	}

	probe := gitx.New(cwd)
	top, err := probe.Toplevel(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return "", nil, exitcode.GitFailure
	}

	return top, gitx.New(top), exitcode.Success
}
