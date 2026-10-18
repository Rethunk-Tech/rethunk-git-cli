// Package app implements the `rgit log` command surface, two invocation shapes sharing one exit-
// code table and one patch-free-by-default rule (docs/USAGE.md § Log):
//
//   - `rgit log FILE:SYMBOL` -- patch-free history of one symbol, one
//     record per commit that touched the extent resolved at HEAD, or at
//     --source's revision. A thin caller over anchor resolution and
//     `git log -L`: git's own -L
//     implementation already re-derives the touched line range at each
//     ancestor commit itself, so there is no per-commit tree-sitter
//     re-parse here and no second attribution path.
//   - `rgit log [PATH...]` -- path-scoped history with no symbol at all,
//     with or without --since/--until. Selected when every positional
//     classifies as a pathspec (the six-rule table, not a colon heuristic:
//     an existing path like src/notes:draft.md is still a pathspec) so a
//     lockfile, a JSON file, or a directory does not fall through to the
//     anchor form. Zero positionals and no date stays the anchor form's
//     exit 129, unless --follow is set: that flag selects this form and
//     then requires exactly one path.
//
// One guardrail is non-negotiable for both:
// patches are opt-in (-p/--patch), never default -- the default stream is
// bounded by commit count, not by code size, which `git log -L` or a bare
// `git log` would not be on their own (both always show the patch).
package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const logHelp = `usage: rgit log FILE:SYMBOL [--source <rev>] [--since=DATE] [--until=DATE] [-n N|--max-count=N] [--follow-rename] [--porcelain | --json | -p|--patch]
       rgit log [--since=DATE] [--until=DATE] [-n N|--max-count=N] [--follow] [--porcelain | --json | -p|--patch] PATH...
       rgit log --since=DATE [--until=DATE] [-n N|--max-count=N] [--porcelain | --json | -p|--patch]

History of one symbol: one record per commit whose own diff touched its
current extent, newest first. Patch-free by default -- plain
"git log -L" always prints the full patch body for every touching commit,
which is exactly the flood this command exists to avoid.

The anchor is resolved once, against HEAD -- never the worktree -- since
history is a question about what has already been committed, and git log
-L itself walks HEAD's own history with no notion of the worktree at all.
--source <rev> resolves against <rev>:FILE instead, the same read show and
blame --source use, then runs git log -L on that extent starting at <rev>.
Both spellings (--source REV and --source=REV). FILE is the path's current
name; this does not follow renames. Mutually exclusive with --follow-rename
(exit 129). An unknown revision is exit 128; a path absent at a known one
is exit 3.
Plain git log -L already follows a rename when git's similarity heuristic
detects one, but a rename that also reshuffles the symbol can silently stop
short. --follow-rename re-resolves the anchor's extent at each rename
boundary and continues under the pre-rename path (docs/LIMITATIONS.md).

PATH... with no FILE:SYMBOL positional is ordinary, non-anchored git
history, with or without a date. Every positional is classified with the
same six-rule table as commit and diff, so an existing path that contains
a colon stays a pathspec. With neither a path, a date, nor --follow, log
requires a FILE:SYMBOL anchor.

--follow continues one path's history past renames, forwarded to git's
own --follow. It requires exactly one path; zero paths, or two or more,
is exit 129. A FILE:SYMBOL anchor does not take --follow; that form's
rename walk is --follow-rename.

--since=DATE bounds either form. Forwarded to git's own --since unparsed,
so anything git accepts there ("2024-01-01", "2 weeks ago") works here too.

--until=DATE bounds the selected form's other end, alone or combined with
--since. With no path and a date bound, the unanchored form is the whole
repository's history in that window, matching plain "git log --since=DATE".

-n, --max-count=N limits either form to at most N commits, forwarded to
git's own count limit. Under --follow-rename the limit applies per rename
segment, so the total can exceed N. Without -n, history is unbounded.

--follow-rename walks the file's rename history: at each commit that
renamed it, the anchor's extent is re-resolved against the pre-rename blob
just before the rename, and history continues under that path. Without it,
plain "git log -L" rename following still applies when similarity detects
the rename, but reordering across the boundary can still lose the thread.

--porcelain lists stable tab-separated HASH<TAB>SUBJECT records instead of
the aligned "<abbrev-hash> <subject>" default.

--json lists those same commits as one JSON object: see
docs/CODES.md#rgit-log---json. Mutually exclusive with --porcelain and
with -p/--patch.

-p, --patch opts into the real patch body for each commit -- git's own
log output, unmodified, not a second record shape rgit invents.
Mutually exclusive with --porcelain and with --json.

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

// hasFollowFlag reports whether args names the path form's --follow.
// --follow-rename is a different flag and must not select this form.
func hasFollowFlag(args []string) bool {
	for _, a := range args {
		if a == "--follow" || strings.HasPrefix(a, "--follow=") {
			return true
		}
	}
	return false
}

// logInteriorColonPositionals collects positionals that look like they
// might be FILE:SYMBOL anchors: an interior colon, not leading-colon
// pathspec magic, and not after "--". A colon is not enough on its own --
// src/notes:draft.md is a legal path (docs/USAGE.md rule 4) -- so callers
// that pick between log's two forms still have to classify each token.
func logInteriorColonPositionals(args []string) []string {
	var out []string
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
		case a == "--since" || a == "--until" || a == "-n" || a == "--max-count" || a == "--source":
			i++
			continue
		case strings.HasPrefix(a, "--since="), strings.HasPrefix(a, "--until="), strings.HasPrefix(a, "-n"), strings.HasPrefix(a, "--max-count="), strings.HasPrefix(a, "--source="):
			continue
		case a == "--porcelain", a == "--json", a == "-p", a == "--patch", a == "--follow-rename", a == "--help", a == "-h":
			continue
		case strings.Contains(a, ":") && !strings.HasPrefix(a, ":"):
			out = append(out, a)
		}
	}
	return out
}

func runLog(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	// No path and no date is still the anchor form, which refuses with
	// exit 129. A path selects path-scoped history once classification
	// says every positional is a pathspec, date or not. --follow selects
	// the same path form, then requires exactly one path.
	if hasTimeRangeFlag(args) || hasFollowFlag(args) || logHasPositionals(args) {
		anchor, code := logSelectsAnchor(ctx, dir, args, stderr)
		if code != exitcode.Success {
			return code
		}
		if !anchor {
			return runLogPathScoped(ctx, dir, args, stdout, stderr)
		}
	}
	return runLogAnchor(ctx, dir, args, stdout, stderr)
}

// logHasPositionals reports whether args names a path or anchor, including
// a token after "--". Flags and their values are not positionals, so
// "rgit log --porcelain" stays the no-argument refusal.
func logHasPositionals(args []string) bool {
	afterSeparator := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			afterSeparator = true
			continue
		}
		if afterSeparator {
			return true
		}
		switch {
		case a == "--since" || a == "--until" || a == "-n" || a == "--max-count" || a == "--source":
			i++
		case strings.HasPrefix(a, "-"):
			continue
		default:
			return true
		}
	}
	return false
}

// logSelectsAnchor reports whether the invocation should keep the
// FILE:SYMBOL form. The cheap colon scan is only a filter: an existing
// path that itself contains a colon is a pathspec, the same rule 4 commit
// and diff already apply, and that form is path-scoped history. A token
// that classifies as KindAnchor, or that fails classification entirely
// (so runLogAnchor can print its own message), keeps the symbol-scoped form.
func logSelectsAnchor(ctx context.Context, dir string, args []string, stderr io.Writer) (bool, exitcode.Code) {
	candidates := logInteriorColonPositionals(args)
	if len(candidates) == 0 {
		return false, exitcode.Success
	}

	root, prefix, repo, code := openRepo(ctx, dir, stderr)
	if code != exitcode.Success {
		return false, code
	}
	checker := &cli.GitPathChecker{Root: root, Prefix: prefix, Repo: repo}
	revs := cli.GitRevisionResolver{Repo: repo}
	for _, a := range candidates {
		classified, err := cli.ClassifyArgs(ctx, []string{a}, false, checker, revs)
		if err != nil {
			if _, ok := errors.AsType[*cli.UnresolvedArgError](err); ok {
				return true, exitcode.Success
			}
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return false, exitcode.GitFailure
		}
		if len(classified) > 0 && classified[0].Kind == cli.KindAnchor {
			return true, exitcode.Success
		}
	}
	return false, exitcode.Success
}

// runLogAnchor is rgit log's FILE:SYMBOL form. Flags go through the shared
// anchor parser; setLogMaxCount still checks -n/--max-count.
func runLogAnchor(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	var opts logAnchorOptions
	opts.asJSON = jsonFlag(ctx)
	positionals, code, done := parseAnchorCommandArgs("log", args, []anchorCommandFlag{
		{tokens: []string{"--porcelain"}, set: &opts.porcelain},
		{tokens: []string{"-p", "--patch"}, set: &opts.patch},
		{tokens: []string{"--follow-rename"}, set: &opts.followRename},
		{tokens: []string{"--source"}, val: &opts.source},
		{tokens: []string{"--since"}, val: &opts.since, quoteMissing: true},
		{tokens: []string{"--until"}, val: &opts.until, quoteMissing: true},
		{tokens: []string{"-n", "--max-count"}, quoteMissing: true, glued: true, accept: func(flag, value string) bool {
			return setLogMaxCount(&opts, flag, value, stderr)
		}},
	}, 1, logHelp, stdout, stderr, true)
	if done {
		return code
	}
	opts.positional = positionals[0]
	if code := refuseLogFormatConflict(opts.porcelain, opts.asJSON, opts.patch, stderr); code != exitcode.Success {
		return code
	}
	if opts.source != "" && opts.followRename {
		fmt.Fprintln(stderr, "rgit: --source and --follow-rename are mutually exclusive")
		fmt.Fprint(stderr, logHelp)
		return exitcode.InvalidUsage
	}

	// History is a question about what has already been committed, never
	// about an uncommitted worktree edit. The default resolves against
	// HEAD's own blob so the derived line range matches `git log -L`, which
	// walks HEAD's history and knows nothing about the worktree. --source
	// resolves against that revision instead, the same read show and blame
	// use, and the walk starts there.
	fetch := showHeadSource()
	if opts.source != "" {
		fetch = showSource(opts.source)
	}
	repo, file, blob, res, anchorName, code := resolveAnchorExtent(ctx, dir, stderr, opts.positional, "log", logHelp, fetch)
	if code != exitcode.Success {
		return code
	}
	warnIfOrdinalAnchor(stderr, file, anchorName)

	extra := logDateArgs(opts.since, opts.until, opts.maxCount, opts.maxCountSet, logFormatArgs(opts.patch, opts.porcelain || opts.asJSON))

	if opts.followRename {
		out := stdout
		var buf bytes.Buffer
		if opts.asJSON {
			out = &buf
		}
		code := runLogFollowRename(ctx, repo, file, blob, res, anchorName, extra, out, stderr)
		if code != exitcode.Success {
			return code
		}
		if opts.asJSON {
			return writeLogOutput(stdout, stderr, buf.Bytes(), true)
		}
		return exitcode.Success
	}

	start, end := lineRange(blob, res.Extent)
	logArgs := extra
	if opts.source != "" {
		// The line numbers were taken from this revision's blob, so the
		// walk has to start there. git log -L without a revision reads
		// those numbers against HEAD.
		logArgs = append([]string{opts.source}, extra...)
	}
	out, err := repo.LogLineRange(ctx, file, start, end, logArgs...)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	return writeLogOutput(stdout, stderr, out, opts.asJSON)
}

type logAnchorOptions struct {
	since        string
	until        string
	maxCount     int
	maxCountSet  bool
	porcelain    bool
	asJSON       bool
	patch        bool
	followRename bool
	source       string
	positional   string
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
// re-resolves the anchor against the pre-rename blob one commit before
// that boundary and repeats, via gitx.FindRename. This is deliberately not
// a per-commit tree-sitter re-parse (TODO.md's own tradeoff): git log -L
// already re-derives how a fixed line range moves within one file's own
// history, so only a rename crossing to a new path name -- found once per
// segment, not once per commit -- needs a fresh resolve.
func runLogFollowRename(ctx context.Context, repo *gitx.Repo, file string, src []byte, res *resolve.Resolution, anchorName string, extra []string, stdout, stderr io.Writer) exitcode.Code {
	return walkRenameSegments(ctx, repo, "log", file, anchorName, src, res, stderr, func(rev, file string, src []byte, res *resolve.Resolution, renameCommit string, found bool) exitcode.Code {
		start, end := lineRange(src, res.Extent)
		// Bounded to the rename boundary when one was found -- otherwise
		// this segment's own git log -L would walk straight through it and
		// duplicate the history the next segment (under the pre-rename path) is
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
		return exitcode.Success
	})
}

// runLogPathScoped is rgit log's path form: ordinary, non-anchored git
// history of one or more paths, optionally bounded by date. Flags are
// parsed with pflag, matching commit.go and diff.go's own
// style for a surface with value-taking flags -- this form's arity (zero or
// more path positionals, none of them a required anchor) does not fit
// shared.go's parseAnchorCommandArgs, which always requires at least one
// anchor.
func runLogPathScoped(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	var since, until string
	var maxCount int
	var porcelain, patch, follow bool
	asJSON := jsonFlag(ctx)

	fs := pflag.NewFlagSet("log", pflag.ContinueOnError)
	fs.SetInterspersed(true)
	fs.SetOutput(io.Discard)
	fs.StringVar(&since, "since", "", "only commits at or after this date (forwarded to git)")
	fs.StringVar(&until, "until", "", "only commits at or before this date (forwarded to git)")
	fs.IntVarP(&maxCount, "max-count", "n", 0, "maximum number of commits (forwarded to git)")
	fs.BoolVar(&porcelain, "porcelain", false, "stable tab-separated HASH<TAB>SUBJECT records")
	fs.BoolVarP(&patch, "patch", "p", false, "include the real patch body")
	fs.BoolVar(&follow, "follow", false, "continue one path's history past renames")

	if code, done := parseFlagsOrHelp(fs, args, stdout, stderr, logHelp); done {
		return code
	}
	if code := refuseLogFormatConflict(porcelain, asJSON, patch, stderr); code != exitcode.Success {
		return code
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

	// git log --follow walks rename history for one pathspec. A usage error
	// is exit 129, so any other count is refused before git runs.
	if follow && len(paths) != 1 {
		fmt.Fprintln(stderr, "rgit: log: --follow requires exactly one path")
		fmt.Fprint(stderr, logHelp)
		return exitcode.InvalidUsage
	}

	extra := logFormatArgs(patch, porcelain || asJSON)
	if fs.Changed("max-count") {
		extra = append([]string{"--max-count=" + strconv.Itoa(maxCount)}, extra...)
	}
	if follow {
		extra = append([]string{"--follow"}, extra...)
	}

	out, err := repo.Log(ctx, since, until, paths, extra...)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	return writeLogOutput(stdout, stderr, out, asJSON)
}

func refuseLogFormatConflict(porcelain, asJSON, patch bool, stderr io.Writer) exitcode.Code {
	switch {
	case porcelain && patch:
		fmt.Fprintln(stderr, "rgit: --porcelain and --patch are mutually exclusive")
	case porcelain && asJSON:
		fmt.Fprintln(stderr, "rgit: --porcelain and --json are mutually exclusive")
	case asJSON && patch:
		fmt.Fprintln(stderr, "rgit: --json and --patch are mutually exclusive")
	default:
		return exitcode.Success
	}
	fmt.Fprint(stderr, logHelp)
	return exitcode.InvalidUsage
}

func writeLogOutput(stdout, stderr io.Writer, out []byte, asJSON bool) exitcode.Code {
	if !asJSON {
		_, _ = stdout.Write(out)
		return exitcode.Success
	}
	if err := writeLogJSON(stdout, out); err != nil {
		fmt.Fprintf(stderr, "rgit: log: %v\n", err)
		return exitcode.GitFailure
	}
	return exitcode.Success
}

type logCommitRecord struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}

type logReport struct {
	Schema  int               `json:"schema"`
	Commits []logCommitRecord `json:"commits"`
}

func writeLogJSON(w io.Writer, out []byte) error {
	records := make([]logCommitRecord, 0)
	body := strings.TrimRight(string(out), "\n")
	if body != "" {
		for line := range strings.SplitSeq(body, "\n") {
			hash, subject, ok := strings.Cut(line, "\t")
			if !ok {
				return fmt.Errorf("malformed log record %q", line)
			}
			records = append(records, logCommitRecord{Hash: hash, Subject: subject})
		}
	}
	return encodeJSON(w, logReport{Schema: jsonSchema, Commits: records})
}

// logFormatArgs is the extra git-log arguments both of rgit log's forms
// derive identically from patch versus record output: real patch when opted
// in, or the HASH<TAB>SUBJECT shape --porcelain and --json both consume.
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
