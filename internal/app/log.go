// The `rgit log` command surface, two invocation shapes sharing one exit-
// code table and one patch-free-by-default rule (docs/USAGE.md § Log):
//
//   - `rgit log FILE:SYMBOL` -- patch-free history of one symbol, one
//     record per commit that touched its current extent, newest first. A
//     thin caller over anchor resolution and `git log -L`: git's own -L
//     implementation already re-derives the touched line range at each
//     ancestor commit itself, so there is no per-commit tree-sitter
//     re-parse here and no second attribution path (specs/design.md §
//     Commands).
//   - `rgit log --since=DATE [--until=DATE] [PATH...]` -- time- and
//     path-scoped history with no symbol at all, the shape the operator's
//     own tooling otherwise has to fall back to plain `git log --since=...
//     -- <paths>` for. Selected by the presence of --since/--until; every
//     positional is a pathspec, not an anchor.
//
// specs/design.md § Commands's own guardrail is non-negotiable for both:
// patches are opt-in (-p/--patch), never default -- the default stream is
// bounded by commit count, not by code size, which `git log -L` or a bare
// `git log` would not be on their own (both always show the patch).
package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/pflag"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

const logHelp = `usage: rgit log FILE:SYMBOL [--porcelain | -p|--patch]
       rgit log --since=DATE [--until=DATE] [--porcelain | -p|--patch] [PATH...]

History of one symbol: one record per commit whose own diff touched its
current extent, newest first. Patch-free by default -- plain
"git log -L" always prints the full patch body for every touching commit,
which is exactly the flood this command exists to avoid.

The anchor is resolved once, against HEAD -- never the worktree -- since
history is a question about what has already been committed, and git log
-L itself walks HEAD's own history with no notion of the worktree at all.
A file renamed since a commit loses its history under the old name; query
it under its current name instead (docs/LIMITATIONS.md).

--since=DATE switches to the second form: ordinary, non-anchored git
history bounded by date and, optionally, one or more paths -- no symbol
anchor at all. Forwarded to git's own --since unparsed, so anything git
accepts there ("2024-01-01", "2 weeks ago") works here too.

--until=DATE bounds the same form's other end, alone or combined with
--since. With no path and only one bound (or neither), it is the whole
repository's history in that window, matching plain "git log --since=DATE".

--porcelain lists stable tab-separated HASH<TAB>SUBJECT records instead of
the aligned "<abbrev-hash> <subject>" default.

-p, --patch opts into the real patch body for each commit -- git's own
log output, unmodified, not a second record shape rgit invents.
Mutually exclusive with --porcelain.

Full reference: docs/USAGE.md
`

// hasTimeRangeFlag reports whether args names --since or --until, in
// either of git's own two spellings for a long option taking a value
// ("--since VALUE" or "--since=VALUE"). This is what picks between rgit
// log's two invocation shapes (the package doc comment above): the
// anchor form's arity (exactly one required FILE:SYMBOL positional,
// parsed by shared.go's parseAnchorCommandArgs) and the path-scoped
// form's (zero or more path positionals, parsed by pflag below) are
// different enough that one loop cannot serve both, so this decides which
// one even runs.
func hasTimeRangeFlag(args []string) bool {
	for _, a := range args {
		if a == "--since" || a == "--until" || strings.HasPrefix(a, "--since=") || strings.HasPrefix(a, "--until=") {
			return true
		}
	}
	return false
}

func runLog(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	if hasTimeRangeFlag(args) {
		return runLogPathScoped(ctx, dir, args, stdout, stderr)
	}
	return runLogAnchor(ctx, dir, args, stdout, stderr)
}

// runLogAnchor is rgit log's original FILE:SYMBOL form, unchanged: its
// flag surface is hand-parsed via shared.go's parseAnchorCommandArgs
// (m10: the same loop blame.go shares, rather than each command keeping
// its own copy a future flag could land on and miss), the same minimal
// style blame.go and languages.go already use for a command this small.
// hasTimeRangeFlag above is what keeps this reachable only when neither
// --since nor --until was given, so its behavior -- including its exact
// error wording -- is identical to before the path-scoped form existed.
func runLogAnchor(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	porcelain := false
	patch := false
	positional, code, done := parseAnchorCommandArgs("log", args,
		[]anchorCommandFlag{
			{tokens: []string{"--porcelain"}, set: &porcelain},
			{tokens: []string{"-p", "--patch"}, set: &patch},
		},
		logHelp, stdout, stderr)
	if done {
		return code
	}
	if porcelain && patch {
		fmt.Fprintln(stderr, "rgit: --porcelain and --patch are mutually exclusive")
		fmt.Fprint(stderr, logHelp)
		return exitcode.InvalidUsage
	}

	// History is a question about what HEAD (and its ancestors) already
	// committed, never about an uncommitted worktree edit -- resolving
	// against HEAD's own blob is what keeps the derived line range
	// meaningful to `git log -L`, which walks HEAD's own history and knows
	// nothing about the worktree at all (specs/design.md § Commands).
	repo, file, head, res, anchorName, code := resolveAnchorExtent(ctx, dir, stderr, positional, "log", logHelp,
		func(ctx context.Context, repo *gitx.Repo, _, file string) ([]byte, string, bool, error) {
			head, exists, err := repo.CatFile(ctx, "HEAD", file)
			if err != nil {
				return nil, "", false, err
			}
			if !exists {
				return nil, "has no HEAD history", false, nil
			}
			return head, "", true, nil
		})
	if code != exitcode.Success {
		return code
	}
	warnIfOrdinalAnchor(stderr, file, anchorName)

	start, end := lineRange(head, res.Extent)

	extra := logFormatArgs(patch, porcelain)

	out, err := repo.LogLineRange(ctx, file, start, end, extra...)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	_, _ = stdout.Write(out)
	return exitcode.Success
}

// runLogPathScoped is rgit log --since/--until's form: ordinary,
// non-anchored git history bounded by date and, optionally, one or more
// paths. Flags are parsed with pflag, matching commit.go and diff.go's own
// style for a surface with value-taking flags -- shared.go's
// parseAnchorCommandArgs only ever supported booleans, and this form's
// arity (zero or more path positionals, none of them a required anchor)
// does not fit its "exactly one positional" rule regardless.
func runLogPathScoped(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	var since, until string
	var porcelain, patch bool

	fs := pflag.NewFlagSet("log", pflag.ContinueOnError)
	fs.SetInterspersed(true)
	fs.SetOutput(io.Discard)
	fs.StringVar(&since, "since", "", "only commits at or after this date (forwarded to git)")
	fs.StringVar(&until, "until", "", "only commits at or before this date (forwarded to git)")
	fs.BoolVar(&porcelain, "porcelain", false, "stable tab-separated HASH<TAB>SUBJECT records")
	fs.BoolVarP(&patch, "patch", "p", false, "include the real patch body")

	if code, done := parseFlagsOrHelp(fs, args, stdout, stderr, logHelp); done {
		return code
	}
	if porcelain && patch {
		fmt.Fprintln(stderr, "rgit: --porcelain and --patch are mutually exclusive")
		fmt.Fprint(stderr, logHelp)
		return exitcode.InvalidUsage
	}

	root, prefix, repo, code := openRepo(ctx, dir, stderr)
	if code != exitcode.Success {
		return code
	}

	paths := make([]string, 0, len(fs.Args()))
	for _, p := range fs.Args() {
		resolved, err := repoPath(root, prefix, p)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.InvalidUsage
		}
		paths = append(paths, resolved)
	}

	out, err := repo.Log(ctx, since, until, paths, logFormatArgs(patch, porcelain)...)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	_, _ = stdout.Write(out)
	return exitcode.Success
}

// logFormatArgs is the extra git-log arguments both of rgit log's forms
// derive identically from patch/porcelain: real patch when opted in
// (unmodified git output, not a second format rgit invents), stable
// tab-separated records for scripting, or the aligned human default.
func logFormatArgs(patch, porcelain bool) []string {
	switch {
	case patch:
		// "-p" is a no-op alongside the anchor form's own "-L", which
		// already implies a patch body on its own (measured directly: `git
		// log -L1,1:f` and `git log -L1,1:f -p` produce byte-identical
		// output) -- but the path-scoped form runs plain `git log`, which
		// shows no diff at all without it. One shared arg list has to work
		// for both callers, so it is always passed explicitly rather than
		// relying on the anchor form's own implicit behavior.
		return []string{"-p"}
	case porcelain:
		return []string{"--no-patch", "--format=%H%x09%s"}
	default:
		return []string{"--no-patch", "--format=%h %s"}
	}
}
