// The `rgit log FILE:SYMBOL` command surface: patch-free history of one
// symbol -- one record per commit that touched its current extent, newest
// first. A thin caller over anchor resolution and `git log -L`: git's own
// -L implementation already re-derives the touched line range at each
// ancestor commit itself, so there is no per-commit tree-sitter re-parse
// here and no second attribution path (specs/design.md § Commands).
//
// TODO.md's guardrail is non-negotiable: patches are opt-in (-p/--patch),
// never default -- the default stream is bounded by commit count, not by
// code size, which `git log -L` on its own would not be (it always shows
// the patch).
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const logHelp = `usage: rgit log FILE:SYMBOL [--porcelain | -p|--patch]

History of one symbol: one record per commit whose own diff touched its
current extent, newest first. Patch-free by default -- plain
"git log -L" always prints the full patch body for every touching commit,
which is exactly the flood this command exists to avoid.

The anchor is resolved once, against HEAD -- never the worktree -- since
history is a question about what has already been committed, and git log
-L itself walks HEAD's own history with no notion of the worktree at all.
A file renamed since a commit loses its history under the old name; query
it under its current name instead (docs/LIMITATIONS.md).

--porcelain lists stable tab-separated HASH<TAB>SUBJECT records instead of
the aligned "<abbrev-hash> <subject>" default.

-p, --patch opts into the real patch body for each commit -- git's own
"git log -L" output, unmodified, not a second record shape rgit invents.
Mutually exclusive with --porcelain.

Full reference: docs/USAGE.md
`

// runLog's flag surface is hand-parsed rather than pulling in pflag, the
// same minimal style blame.go and languages.go already use for a command
// this small.
func runLog(ctx context.Context, args []string, stdout, stderr io.Writer) exitcode.Code {
	porcelain := false
	patch := false
	var positional string
	havePositional := false
	for _, a := range args {
		switch {
		case a == "--help" || a == "-h":
			fmt.Fprint(stdout, logHelp)
			return exitcode.Success
		case a == "--porcelain":
			porcelain = true
		case a == "-p" || a == "--patch":
			patch = true
		case havePositional:
			fmt.Fprintf(stderr, "rgit: log: unexpected extra argument %q\n", a)
			fmt.Fprint(stderr, logHelp)
			return exitcode.InvalidUsage
		default:
			positional = a
			havePositional = true
		}
	}
	if !havePositional {
		fmt.Fprintln(stderr, "rgit: log requires a FILE:SYMBOL anchor")
		fmt.Fprint(stderr, logHelp)
		return exitcode.InvalidUsage
	}
	if porcelain && patch {
		fmt.Fprintln(stderr, "rgit: --porcelain and --patch are mutually exclusive")
		fmt.Fprint(stderr, logHelp)
		return exitcode.InvalidUsage
	}

	root, prefix, repo, code := openRepo(ctx, stderr)
	if code != exitcode.Success {
		return code
	}

	// Reused rather than hand-parsed, the same reason blame.go gives: the
	// six-rule precedence table (internal/cli) is what already decides
	// pathspec vs. anchor for commit and diff, and a bare pathspec here must
	// be refused the same way rather than silently misread as an anchor
	// with an empty name.
	checker := cli.GitPathChecker{Root: root, Prefix: prefix, Repo: repo}
	classified, err := cli.ClassifyArgs(ctx, []string{positional}, false, checker, cli.GitRevisionResolver{Repo: repo})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}
	if len(classified) == 0 {
		// A bare "--" is consumed whole by rule 1 (everything after "--" is
		// a pathspec, always) and classifies to nothing -- the same refusal
		// as no positional at all, not a classified[0] panic.
		fmt.Fprintln(stderr, "rgit: log requires a FILE:SYMBOL anchor")
		fmt.Fprint(stderr, logHelp)
		return exitcode.InvalidUsage
	}
	c := classified[0]
	if c.Kind != cli.KindAnchor {
		fmt.Fprintln(stderr, "rgit: log requires a FILE:SYMBOL anchor, not a plain path")
		fmt.Fprint(stderr, logHelp)
		return exitcode.InvalidUsage
	}

	file, err := repoPath(root, prefix, c.Anchor.File)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.InvalidUsage
	}

	lang, ok := resolve.ForExtension(filepath.Ext(file))
	if !ok {
		// Same worktree-shebang fallback as blame.go's validateSym-adjacent
		// check: an extensionless script only resolves if its worktree copy
		// is there to peek a shebang line from. A file already deleted from
		// the worktree (TestRun_LogSurvivesWorktreeDeletion) degrades to the
		// plain "no grammar" refusal here, the same gap diff and commit
		// already accept for the identical reason.
		if line, peeked := resolve.PeekShebangLine(filepath.Join(root, file)); peeked {
			lang, ok = resolve.ForPath(file, line)
		}
	}
	if !ok {
		rerr := &resolve.ResolveError{Code: exitcode.UnsupportedLanguage, Anchor: c.Anchor.Name}
		fmt.Fprintf(stderr, "rgit: %s\n", rerr.Error()+unsupportedLanguageHint(filepath.Ext(file)))
		return rerr.Code
	}

	// History is a question about what HEAD (and its ancestors) already
	// committed, never about an uncommitted worktree edit -- resolving
	// against HEAD's own blob is what keeps the derived line range
	// meaningful to `git log -L`, which walks HEAD's own history and knows
	// nothing about the worktree at all (specs/design.md § Commands).
	head, exists, err := repo.CatFile(ctx, "HEAD", file)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	if !exists {
		rerr := &resolve.ResolveError{Code: exitcode.AnchorUnresolvable, Anchor: c.Anchor.Name}
		fmt.Fprintf(stderr, "rgit: %s (%q has no HEAD history)\n", rerr.Error(), file)
		return rerr.Code
	}

	res, err := resolve.Resolve(lang, head, c.Anchor.Name)
	if err != nil {
		var rerr *resolve.ResolveError
		if errors.As(err, &rerr) {
			fmt.Fprintf(stderr, "rgit: %s\n", rerr.Error())
			return rerr.Code
		}
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}

	start, end := lineRange(head, res.Extent)

	var extra []string
	switch {
	case patch:
		// Unmodified `git log -L`: showing the patch is exactly what -L
		// exists to do, so opting in means nothing more than not
		// suppressing it below.
	case porcelain:
		extra = []string{"--no-patch", "--format=%H%x09%s"}
	default:
		extra = []string{"--no-patch", "--format=%h %s"}
	}

	out, err := repo.LogLineRange(ctx, file, start, end, extra...)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	_, _ = stdout.Write(out)
	return exitcode.Success
}
