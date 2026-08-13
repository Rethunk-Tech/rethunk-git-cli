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
//     -- <paths>` for. Selected by the presence of --since/--until when no
//     positional is a FILE:SYMBOL anchor; every positional is a pathspec.
//
// specs/design.md § Commands's own guardrail is non-negotiable for both:
// patches are opt-in (-p/--patch), never default -- the default stream is
// bounded by commit count, not by code size, which `git log -L` or a bare
// `git log` would not be on their own (both always show the patch).
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const logHelp = `usage: rgit log FILE:SYMBOL [--since=DATE] [--until=DATE] [-n N|--max-count=N] [--follow-rename] [--porcelain | -p|--patch]
       rgit log --since=DATE [--until=DATE] [-n N|--max-count=N] [--porcelain | -p|--patch] [PATH...]

History of one symbol: one record per commit whose own diff touched its
current extent, newest first. Patch-free by default -- plain
"git log -L" always prints the full patch body for every touching commit,
which is exactly the flood this command exists to avoid.

The anchor is resolved once, against HEAD -- never the worktree -- since
history is a question about what has already been committed, and git log
-L itself walks HEAD's own history with no notion of the worktree at all.
Plain git log -L already follows a rename when git's similarity heuristic
detects one, but a rename that also reshuffles the symbol can silently stop
short. --follow-rename re-resolves the anchor's extent at each rename
boundary and continues under the old name (docs/LIMITATIONS.md).

--since=DATE bounds either form. With a FILE:SYMBOL positional, the
anchor remains the symbol-scoped form; otherwise this is ordinary,
non-anchored git history with, optionally, one or more paths. Forwarded to
git's own --since unparsed, so anything git accepts there ("2024-01-01",
"2 weeks ago") works here too.

--until=DATE bounds the selected form's other end, alone or combined with
--since. With no path and only one bound (or neither), the unanchored form
is the whole repository's history in that window, matching plain
"git log --since=DATE".

-n, --max-count=N limits either form to at most N commits, forwarded to
git's own count limit. Under --follow-rename the limit applies per rename
segment, so the total can exceed N. Without -n, history is unbounded.

--follow-rename walks the file's rename history: at each commit that
renamed it, the anchor's extent is re-resolved against the old name's blob
just before the rename, and history continues under that name. Without it,
plain "git log -L" rename following still applies when similarity detects
the rename, but reordering across the boundary can still lose the thread.

--porcelain lists stable tab-separated HASH<TAB>SUBJECT records instead of
the aligned "<abbrev-hash> <subject>" default.

-p, --patch opts into the real patch body for each commit -- git's own
log output, unmodified, not a second record shape rgit invents.
Mutually exclusive with --porcelain.

Full reference: docs/USAGE.md
`

// hasTimeRangeFlag reports whether args names --since or --until, in
// either of git's own two spellings for a long option taking a value
// ("--since VALUE" or "--since=VALUE").
func hasTimeRangeFlag(args []string) bool {
	for _, a := range args {
		if a == "--since" || a == "--until" || strings.HasPrefix(a, "--since=") || strings.HasPrefix(a, "--until=") {
			return true
		}
	}
	return false
}

// hasLogAnchor reports whether a positional has FILE:SYMBOL shape. A leading
// colon is git pathspec magic, and -- makes every following token a pathspec,
// so neither can select the anchor form.
func hasLogAnchor(args []string) bool {
	afterSeparator := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			afterSeparator = true
			continue
		}
		if afterSeparator {
			continue
		}
		switch {
		case a == "--since" || a == "--until" || a == "-n" || a == "--max-count":
			i++
			continue
		case strings.HasPrefix(a, "--since="), strings.HasPrefix(a, "--until="), strings.HasPrefix(a, "-n"), strings.HasPrefix(a, "--max-count="):
			continue
		case a == "--porcelain", a == "-p", a == "--patch", a == "--follow-rename", a == "--help", a == "-h":
			continue
		case strings.Contains(a, ":") && !strings.HasPrefix(a, ":"):
			return true
		}
	}
	return false
}

func runLog(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	if hasTimeRangeFlag(args) && !hasLogAnchor(args) {
		return runLogPathScoped(ctx, dir, args, stdout, stderr)
	}
	return runLogAnchor(ctx, dir, args, stdout, stderr)
}

// runLogAnchor is rgit log's FILE:SYMBOL form. Its small flag surface is
// hand-parsed here because date and count flags take values while the shared
// anchor parser only accepts booleans.
func runLogAnchor(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	opts, code, done := parseLogAnchorArgs(args, stdout, stderr)
	if done {
		return code
	}
	if opts.porcelain && opts.patch {
		fmt.Fprintln(stderr, "rgit: --porcelain and --patch are mutually exclusive")
		fmt.Fprint(stderr, logHelp)
		return exitcode.InvalidUsage
	}

	// History is a question about what HEAD (and its ancestors) already
	// committed, never about an uncommitted worktree edit -- resolving
	// against HEAD's own blob is what keeps the derived line range
	// meaningful to `git log -L`, which walks HEAD's own history and knows
	// nothing about the worktree at all (specs/design.md § Commands).
	repo, file, head, res, anchorName, code := resolveAnchorExtent(ctx, dir, stderr, opts.positional, "log", logHelp,
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

	extra := logDateArgs(opts.since, opts.until, opts.maxCount, opts.maxCountSet, logFormatArgs(opts.patch, opts.porcelain))

	if opts.followRename {
		return runLogFollowRename(ctx, repo, file, head, res, anchorName, extra, stdout, stderr)
	}

	start, end := lineRange(head, res.Extent)
	out, err := repo.LogLineRange(ctx, file, start, end, extra...)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	_, _ = stdout.Write(out)
	return exitcode.Success
}

type logAnchorOptions struct {
	since        string
	until        string
	maxCount     int
	maxCountSet  bool
	porcelain    bool
	patch        bool
	followRename bool
	positional   string
}

func parseLogAnchorArgs(args []string, stdout, stderr io.Writer) (logAnchorOptions, exitcode.Code, bool) {
	var opts logAnchorOptions
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--help" || a == "-h":
			fmt.Fprint(stdout, logHelp)
			return logAnchorOptions{}, exitcode.Success, true
		case a == "--porcelain":
			opts.porcelain = true
		case a == "-p" || a == "--patch":
			opts.patch = true
		case a == "--follow-rename":
			opts.followRename = true
		case a == "--since" || a == "--until":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "rgit: log: option %q requires a value\n", a)
				fmt.Fprint(stderr, logHelp)
				return logAnchorOptions{}, exitcode.InvalidUsage, true
			}
			i++
			if a == "--since" {
				opts.since = args[i]
			} else {
				opts.until = args[i]
			}
		case strings.HasPrefix(a, "--since="):
			opts.since = strings.TrimPrefix(a, "--since=")
		case strings.HasPrefix(a, "--until="):
			opts.until = strings.TrimPrefix(a, "--until=")
		case a == "-n" || a == "--max-count":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "rgit: log: option %q requires a value\n", a)
				fmt.Fprint(stderr, logHelp)
				return logAnchorOptions{}, exitcode.InvalidUsage, true
			}
			i++
			if !setLogMaxCount(&opts, a, args[i], stderr) {
				fmt.Fprint(stderr, logHelp)
				return logAnchorOptions{}, exitcode.InvalidUsage, true
			}
		case strings.HasPrefix(a, "-n") && len(a) > 2:
			if !setLogMaxCount(&opts, "-n", a[2:], stderr) {
				fmt.Fprint(stderr, logHelp)
				return logAnchorOptions{}, exitcode.InvalidUsage, true
			}
		case strings.HasPrefix(a, "--max-count="):
			if !setLogMaxCount(&opts, "--max-count", strings.TrimPrefix(a, "--max-count="), stderr) {
				fmt.Fprint(stderr, logHelp)
				return logAnchorOptions{}, exitcode.InvalidUsage, true
			}
		case a == "--":
			if opts.positional != "" {
				fmt.Fprintf(stderr, "rgit: log: unrecognized argument %q\n", a)
				fmt.Fprint(stderr, logHelp)
				return logAnchorOptions{}, exitcode.InvalidUsage, true
			}
			fmt.Fprintln(stderr, `rgit: log: "--" cannot be used as a FILE:SYMBOL anchor; log requires a FILE:SYMBOL anchor`)
			fmt.Fprint(stderr, logHelp)
			return logAnchorOptions{}, exitcode.InvalidUsage, true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "rgit: log: unrecognized argument %q\n", a)
			fmt.Fprint(stderr, logHelp)
			return logAnchorOptions{}, exitcode.InvalidUsage, true
		case opts.positional != "":
			fmt.Fprintf(stderr, "rgit: log: unrecognized argument %q\n", a)
			fmt.Fprint(stderr, logHelp)
			return logAnchorOptions{}, exitcode.InvalidUsage, true
		default:
			opts.positional = a
		}
	}
	if opts.positional == "" {
		fmt.Fprintln(stderr, "rgit: log requires a FILE:SYMBOL anchor")
		fmt.Fprint(stderr, logHelp)
		return logAnchorOptions{}, exitcode.InvalidUsage, true
	}
	return opts, exitcode.Success, false
}

func setLogMaxCount(opts *logAnchorOptions, flag, value string, stderr io.Writer) bool {
	maxCount, err := strconv.Atoi(value)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: log: invalid value %q for %s\n", value, flag)
		return false
	}
	opts.maxCount = maxCount
	opts.maxCountSet = true
	return true
}

func logDateArgs(since, until string, maxCount int, maxCountSet bool, extra []string) []string {
	if maxCountSet {
		extra = append([]string{"--max-count=" + strconv.Itoa(maxCount)}, extra...)
	}
	if until != "" {
		extra = append([]string{"--until=" + until}, extra...)
	}
	if since != "" {
		extra = append([]string{"--since=" + since}, extra...)
	}
	return extra
}

// runLogFollowRename walks file's rename history one segment at a time,
// newest first: HEAD's own extent (already resolved by the caller as head/
// res) covers the segment from HEAD back to the nearest rename boundary
// (or, absent one, the file's whole history); each further segment
// re-resolves the anchor against the old name's blob one commit before
// that boundary and repeats, via gitx.FindRename. This is deliberately not
// a per-commit tree-sitter re-parse (TODO.md's own tradeoff): git log -L
// already re-derives how a fixed line range moves within one file's own
// history, so only a rename crossing to a new path name -- found once per
// segment, not once per commit -- needs a fresh resolve.
func runLogFollowRename(ctx context.Context, repo *gitx.Repo, file string, src []byte, res *resolve.Resolution, anchorName string, extra []string, stdout, stderr io.Writer) exitcode.Code {
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

		// Bounded to the rename boundary when one was found -- otherwise
		// this segment's own git log -L would walk straight through it and
		// duplicate the history the next segment (under the old name) is
		// about to report on its own.
		segRev := rev
		if found {
			segRev = renameCommit + "~1.." + rev
		}
		segArgs := append([]string{segRev}, extra...)
		out, err := repo.LogLineRange(ctx, file, start, end, segArgs...)
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
			fmt.Fprintf(stderr, "rgit: log: %q: unsupported language before the rename to its current name\n", file)
			return exitcode.UnsupportedLanguage
		}
		var exists bool
		src, exists, err = repo.CatFile(ctx, rev, file)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.GitFailure
		}
		if !exists {
			fmt.Fprintf(stderr, "rgit: log: %q does not exist at %s\n", file, rev)
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

// runLogPathScoped is rgit log --since/--until's form: ordinary,
// non-anchored git history bounded by date and, optionally, one or more
// paths. Flags are parsed with pflag, matching commit.go and diff.go's own
// style for a surface with value-taking flags -- shared.go's
// parseAnchorCommandArgs only ever supported booleans, and this form's
// arity (zero or more path positionals, none of them a required anchor)
// does not fit its "exactly one positional" rule regardless.
func runLogPathScoped(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	var since, until string
	var maxCount int
	var porcelain, patch bool

	fs := pflag.NewFlagSet("log", pflag.ContinueOnError)
	fs.SetInterspersed(true)
	fs.SetOutput(io.Discard)
	fs.StringVar(&since, "since", "", "only commits at or after this date (forwarded to git)")
	fs.StringVar(&until, "until", "", "only commits at or before this date (forwarded to git)")
	fs.IntVarP(&maxCount, "max-count", "n", 0, "maximum number of commits (forwarded to git)")
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

	extra := logFormatArgs(patch, porcelain)
	if fs.Changed("max-count") {
		extra = append([]string{"--max-count=" + strconv.Itoa(maxCount)}, extra...)
	}

	out, err := repo.Log(ctx, since, until, paths, extra...)
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
