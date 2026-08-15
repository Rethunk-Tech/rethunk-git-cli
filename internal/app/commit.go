// The `rgit commit` command surface: flag parsing, validation, and dispatch
// into internal/synth. Flags and exit codes are specified in docs/USAGE.md.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/synth"
)

// commitFlags mirrors the `rgit commit` flag surface in docs/USAGE.md §
// Flags.
type commitFlags struct {
	messages     []string
	msgFile      string
	signoff      bool
	trailers     []string
	amend        bool
	allowEmpty   bool
	push         bool
	dryRun       bool
	noVerify     bool
	fixup        string
	squash       string
	reuseMessage string
	author       string
	date         string
	resetAuthor  bool
	gpgSignKey   string // "" = not given; gpgSignBare = bare --gpg-sign; else the key id
	noGPGSign    bool
	porcelain    bool
	quiet        bool
	only         bool
	pathspecFile string
	pathspecNUL  bool
	syms         []string
	files        []string
}

// expandGPGSignShorthand rewrites git's own -S spelling into the long form
// before pflag ever sees it, which is what lets rgit accept the flag every
// GPG user actually types.
//
// It cannot be a registered shorthand: pflag resolves an optional-value
// flag's NoOptDefVal before checking for an attached value, so -SDEADBEEF
// parses as a chain of nonexistent single-letter flags rather than as a key
// id (specs/design.md § CLI handling).
//
// The rewrite follows getopt's rule, which is git's: for a short option
// taking an optional argument, the rest of the token IS the argument. -Ss
// therefore means the key "s", not "-s -S", and that is exactly why this
// cannot expand general shorthand chains -- there is no way to tell a key
// id from a run of flags, and git does not try either. A token that packs
// S in behind other shorthands (-sS) is left alone and pflag rejects it by
// name, which is an honest refusal rather than a silent misread.
//
// Everything after "--" is a pathspec (docs/USAGE.md § Argument shape) and
// is never rewritten.
func expandGPGSignShorthand(args []string) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			return append(out, args[i:]...)
		}
		switch {
		case a == "-S":
			out = append(out, "--gpg-sign")
		case strings.HasPrefix(a, "-S") && len(a) > 2:
			out = append(out, "--gpg-sign="+a[2:])
		default:
			out = append(out, a)
		}
	}
	return out
}

// gpgSignBare is commitFlags.gpgSignKey's NoOptDefVal sentinel for a bare
// --gpg-sign (no key id): the empty string is already "flag not given" at
// all, so the sentinel is what lets bare-vs-absent be told apart after
// Parse.
//
// It has to be printable. pflag renders a string flag's NoOptDefVal
// straight into the usage line as [="<value>"], so a NUL-prefixed sentinel
// -- the obvious choice for "can never be a real key id" -- would put a raw
// control byte in `rgit commit --help` and break the column alignment for
// every flag after it. This reads correctly there instead, and the angle
// brackets keep it out of the space of real GPG key ids just as well.
const gpgSignBare = "<default-key>"

func runCommit(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	var f commitFlags
	fs := newTargetFlagSet("commit", &f.syms, &f.files)
	// -m's long spelling matches git commit's own --message, and -F's long
	// spelling is --message-file (to avoid collision with rgit's --file
	// pathspec flag).
	fs.StringArrayVarP(&f.messages, "message", "m", nil, "commit message (repeatable)")
	fs.StringVarP(&f.msgFile, "message-file", "F", "", "read the message from a file, or - for stdin")
	fs.BoolVarP(&f.signoff, "signoff", "s", false, "append Signed-off-by")
	fs.StringArrayVar(&f.trailers, "trailer", nil, "append a trailer TOKEN:VALUE (repeatable)")
	fs.BoolVar(&f.amend, "amend", false, "amend the previous commit")
	fs.BoolVar(&f.allowEmpty, "allow-empty", false, "permit a commit with no changes")
	fs.BoolVar(&f.push, "push", false, "push upstream after a successful commit")
	fs.BoolVar(&f.dryRun, "dry-run", false, "preview only; writes and stages nothing")
	fs.BoolVar(&f.noVerify, "no-verify", false, "skip git hooks")
	fs.StringVar(&f.fixup, "fixup", "", "autosquash fixup for <commit> (or amend:<commit>/reword:<commit>)")
	fs.StringVar(&f.squash, "squash", "", "autosquash squash for <commit>")
	fs.StringVar(&f.reuseMessage, "reuse-message", "", "reuse the message and authorship from <commit>")
	reeditMessage := fs.Bool("reedit-message", false, "unsupported; use --reuse-message")
	fs.StringVar(&f.author, "author", "", "override the commit author")
	fs.StringVar(&f.date, "date", "", "override the commit date")
	fs.BoolVar(&f.resetAuthor, "reset-author", false, "take the author identity from the committer (with --amend)")
	fs.BoolVar(&f.porcelain, "porcelain", false, "list staged targets as stable tab-separated records")
	fs.BoolVarP(&f.quiet, "quiet", "q", false, "suppress the commit summary and target listing")
	fs.BoolVarP(&f.only, "only", "o", false, "commit only named targets")
	// -S is not registered as a shorthand here; expandGPGSignShorthand
	// rewrites it before Parse, for the pflag reason documented there.
	fs.StringVar(&f.gpgSignKey, "gpg-sign", "", "GPG-sign the commit; -S/-S<key-id>/--gpg-sign=<key-id>")
	fs.Lookup("gpg-sign").NoOptDefVal = gpgSignBare
	fs.BoolVar(&f.noGPGSign, "no-gpg-sign", false, "do not GPG-sign, overriding commit.gpgsign")
	fs.StringVar(&f.pathspecFile, "pathspec-from-file", "", "read targets from a file, or - for stdin")
	fs.BoolVar(&f.pathspecNUL, "pathspec-file-nul", false, "separate pathspec-file targets with NUL")

	help := "usage: rgit commit [flags] [target...]\n\n" +
		"Stage named targets -- pathspecs and/or FILE:NAME symbol anchors\n" +
		"(e.g. auth.go:ValidateToken) -- and commit them.\n\n" +
		fs.FlagUsages() +
		"\nFull reference: docs/USAGE.md\n"
	if code, done := parseFlagsOrHelp(fs, expandGPGSignShorthand(args), stdout, stderr, help); done {
		return code
	}
	if *reeditMessage {
		fmt.Fprintln(stderr, "rgit: --reedit-message is unsupported; use --reuse-message")
		return exitcode.InvalidUsage
	}

	if len(f.messages) > 0 && f.msgFile != "" {
		fmt.Fprintln(stderr, "rgit: -m and -F are mutually exclusive")
		return exitcode.InvalidUsage
	}
	if f.dryRun && f.push {
		fmt.Fprintln(stderr, "rgit: --dry-run and --push are mutually exclusive")
		return exitcode.InvalidUsage
	}
	// Asking for machine-readable output and for no output is a
	// contradiction, and silently letting one win would leave a script
	// parsing an empty stream that it cannot tell from "nothing staged".
	if f.porcelain && f.quiet {
		fmt.Fprintln(stderr, "rgit: --porcelain and --quiet are mutually exclusive")
		return exitcode.InvalidUsage
	}
	if f.pathspecNUL && f.pathspecFile == "" {
		fmt.Fprintln(stderr, "rgit: --pathspec-file-nul requires --pathspec-from-file")
		return exitcode.InvalidUsage
	}
	if f.msgFile == "-" && f.pathspecFile == "-" {
		fmt.Fprintln(stderr, "rgit: -F - and --pathspec-from-file=- are mutually exclusive")
		return exitcode.InvalidUsage
	}
	// --amend, --fixup, and --squash each generate their own message when
	// neither -m nor -F is given, and all three need --no-edit forwarded to
	// keep that message-free (docs/USAGE.md: rgit never opens an editor).
	// Plain `--fixup=<commit>` happens to skip git's own editor without it,
	// but `--squash=<commit>` and the `--fixup=amend:`/`--fixup=reword:`
	// subtypes do not -- git opens one to let the subject be edited, which
	// hangs or fails outright ("Terminal is dumb, but EDITOR unset") in any
	// non-interactive caller. An in-progress merge, cherry-pick, or revert
	// may also supply git's generated message; that probe happens after the
	// repository opens. -m/-F given alongside --fixup or --squash is not a
	// conflict: git appends it as an extra body paragraph rather than
	// rejecting or silently dropping it.
	autoMessage := f.amend || f.fixup != "" || f.squash != "" || f.reuseMessage != ""
	noEdit := (f.amend || f.fixup != "" || f.squash != "") && len(f.messages) == 0 && f.msgFile == ""

	positionalsGiven := restoreDoubleDash(fs)
	if f.pathspecFile != "" {
		pathspecs, err := readPathspecFile(f.pathspecFile, f.pathspecNUL)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: reading pathspec file: %v\n", err)
			return exitcode.GitFailure
		}
		positionalsGiven = append(positionalsGiven, pathspecs...)
	}
	targetCount := len(positionalsGiven) + len(f.syms) + len(f.files)
	if f.only && targetCount == 0 && !f.amend {
		fmt.Fprintln(stderr, "rgit: --only requires at least one target")
		return exitcode.InvalidUsage
	}
	if targetCount == 0 && !f.amend && !f.allowEmpty && f.fixup == "" && f.squash == "" && f.reuseMessage == "" {
		fmt.Fprintln(stderr, "rgit: commit requires at least one target")
		return exitcode.InvalidUsage
	}

	if f.reuseMessage == "" && !hasConventionalShape(f.messages) {
		// [warning], not "rgit: warning:" -- every other advisory in this
		// command (below) and in diff.go already uses the bracketed form;
		// one spelling for "advisory, not a refusal" across the surface.
		fmt.Fprintln(stderr, `[warning] message does not look like "type(scope): subject"`)
	}

	root, prefix, repo, code := openRepo(ctx, dir, stderr)
	if code != exitcode.Success {
		return code
	}

	if len(f.messages) == 0 && f.msgFile == "" && !autoMessage {
		op, ok, err := repo.SequencerOp(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.GitFailure
		}
		if ok && (op == "merge" || op == "cherry-pick" || op == "revert") {
			noEdit = true
		} else {
			fmt.Fprintln(stderr, "rgit: commit requires a message (-m or -F)")
			return exitcode.InvalidUsage
		}
	}

	var targetResults []synth.TargetResult
	var onlyPaths []string
	if targetCount > 0 {
		checker := &cli.GitPathChecker{Root: root, Prefix: prefix, Repo: repo}
		classified, err := cli.ClassifyArgs(ctx, positionalsGiven, false, checker, cli.GitRevisionResolver{Repo: repo})
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.InvalidUsage
		}

		targets, err := commitTargets(root, prefix, classified, f.files, f.syms)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return exitcode.InvalidUsage
		}

		// Checked on the built targets, not the raw arguments: by this point
		// positionals and flags have collapsed into one list and every path has
		// been resolved against the invocation prefix, so "src/a.go" named from
		// a subdirectory and "a.go" named from the root compare equal.
		paths, anchorFiles := targetPaths(targets)
		if code := pathAnchorContradiction(paths, anchorFiles, stderr); code != exitcode.Success {
			return code
		}
		if code := refuseStructuredDataAnchors(ctx, repo, root, targets, stderr); code != exitcode.Success {
			return code
		}

		plan, err := synth.PlanStage(ctx, repo, root, targets)
		if err != nil {
			code, msg := mapStageError(err)
			fmt.Fprintf(stderr, "rgit: %s\n", msg)
			return code
		}

		if plan.TSOnly() {
			fmt.Fprintln(stderr, tsOnlyNotice)
		}
		for _, path := range plan.Preamble() {
			fmt.Fprintf(stderr, "[notice] %s is new; staging its @header and @imports so the file compiles\n", path)
		}
		for _, e := range plan.Escalated() {
			fmt.Fprintf(stderr, "[notice] %s: container is new, so the whole container is staged\n", e)
		}
		for _, anchor := range plan.Ordinals() {
			fmt.Fprintf(stderr, "[warning] anchor '%s' is positional; inserting a symbol above it repoints it -- qualify it where the language allows\n", anchor)
		}
		for _, w := range plan.CountingWarnings() {
			fmt.Fprintf(stderr, "[warning] %s\n", w)
		}

		targetResults = plan.Results()
		allUnchanged := len(targetResults) > 0
		for _, r := range targetResults {
			if r.Outcome != synth.Unchanged {
				allUnchanged = false
				continue
			}
			fmt.Fprintf(stderr, "[warning] target '%s' has no uncommitted changes; skipping\n", targetLabel(r.Target))
		}

		// docs/USAGE.md § Targets with nothing to commit: exit 11 only when
		// EVERY named target turned out unchanged, and only then -- a mix of
		// changed and unchanged targets is a warning plus a commit of the rest,
		// not a failure. --allow-empty suppresses it.
		if allUnchanged && !f.allowEmpty {
			return exitcode.NothingToCommit
		}

		if f.dryRun {
			// docs/USAGE.md: dry-run "writes no objects, stages nothing, runs
			// no hooks" -- resolution (including the cross-check above) already
			// happened as a pure read; nothing past this point may execute.
			//
			// It still has to say what it resolved. A preview that prints
			// nothing and exits 0 is indistinguishable from one that found
			// nothing, which is the opposite of what a preview is for.
			if f.porcelain {
				// No preamble: the records are the whole output, so a caller
				// can read them without stripping a human sentence first.
				writeTargetRecords(stdout, targetResults)
				return exitcode.Success
			}
			if !f.quiet {
				fmt.Fprintln(stdout, "dry run: nothing written, nothing staged. Would commit:")
				writeTargetListing(stdout, targetResults)
			}
			return exitcode.Success
		}

		if err := plan.Apply(ctx, repo, root); err != nil {
			code, msg := mapStageError(err)
			fmt.Fprintf(stderr, "rgit: %s\n", msg)
			return code
		}
		if f.only {
			onlyPaths = targetResultPaths(targetResults)
		}
	} else if f.dryRun {
		if f.porcelain {
			return exitcode.Success
		}
		if !f.quiet {
			fmt.Fprintln(stdout, "dry run: nothing written, nothing staged. Would commit:")
		}
		return exitcode.Success
	}

	opts := gitx.CommitOptions{
		Messages:     f.messages,
		Signoff:      f.signoff,
		Trailers:     f.trailers,
		ReuseMessage: f.reuseMessage,
		Amend:        f.amend,
		AllowEmpty:   f.allowEmpty,
		NoVerify:     f.noVerify,
		NoEdit:       noEdit,
		Fixup:        f.fixup,
		Squash:       f.squash,
		Author:       f.author,
		Date:         f.date,
		ResetAuthor:  f.resetAuthor,
		GPGSign:      f.gpgSignKey != "",
		GPGSignKeyID: gpgSignKeyID(f.gpgSignKey),
		NoGPGSign:    f.noGPGSign,
		Only:         f.only,
		OnlyPaths:    onlyPaths,
	}
	if f.msgFile == "-" {
		data, rerr := io.ReadAll(os.Stdin)
		if rerr != nil {
			fmt.Fprintf(stderr, "rgit: reading commit message from stdin: %v\n", rerr)
			return exitcode.GitFailure
		}
		opts.MessageFile = "-"
		opts.StdinMessage = data
	} else if f.msgFile != "" {
		opts.MessageFile = f.msgFile
	}

	// AGENTS.md: a hook rejecting the commit leaves staging in place, and
	// rgit does not roll it back -- Commit's own error is simply reported.
	res, err := repo.Commit(ctx, opts)
	// Hook output goes to the user either way: on success it is the
	// formatter or codegen telling them what it did, and on failure it is
	// usually the reason.
	if len(res.Stderr) > 0 {
		_, _ = stderr.Write(res.Stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	switch {
	case f.porcelain:
		// git's own --porcelain replaces its human summary rather than
		// adding to it, and the same rule applies here: relaying the
		// summary would leave a caller parsing records interleaved with
		// prose.
		sha, ok, revErr := repo.RevParseVerify(ctx, "HEAD")
		if revErr != nil {
			fmt.Fprintf(stderr, "rgit: resolving committed SHA: %v\n", revErr)
			return exitcode.GitFailure
		}
		if !ok {
			fmt.Fprintln(stderr, "rgit: resolving committed SHA: HEAD is unavailable")
			return exitcode.GitFailure
		}
		fmt.Fprintf(stdout, "H\t%s\n", sha)
		writeTargetRecords(stdout, targetResults)
	case f.quiet:
		// Nothing on stdout. Hook output and every warning above still
		// went to stderr -- git's own -q suppresses the summary, not
		// diagnostics.
	default:
		// git's own summary -- branch, new SHA, and the changed/insertion/
		// deletion counts. Relaying it verbatim is what stops a caller
		// having to run `git show` afterwards just to find out what landed.
		_, _ = stdout.Write(res.Stdout)

		// Then the part git cannot report: which symbols went in, and by
		// how much. Same listing and same order as --dry-run, so a preview
		// and the commit it previews are comparable line for line.
		writeTargetListing(stdout, targetResults)
	}

	if f.push {
		// A push failure does not roll back the commit that preceded it
		// (docs/USAGE.md § Flags, AGENTS.md's delegation boundary).
		if err := repo.Push(ctx); err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			// gitx.Push's own doc comment explains why this never becomes
			// an implicit --set-upstream: some push.default settings push
			// a branch with no upstream configured just fine, so guessing
			// -u here could fail a push plain `git push` would have
			// completed. This only adds a concrete, named fix once the
			// push has already failed for its own reason, and only when
			// HasUpstream independently confirms there genuinely is none.
			if hasUpstream, uerr := repo.HasUpstream(ctx); uerr == nil && !hasUpstream {
				if branch, berr := repo.CurrentBranch(ctx); berr == nil && branch != "" && branch != "HEAD" {
					fmt.Fprintf(stderr, "rgit: %s has no upstream tracking branch -- try `git push -u origin %s`, or set push.autoSetupRemote to do this for every push\n", branch, branch)
				}
			}
			return exitcode.PushFailed
		}
	}

	return exitcode.Success
}

// gpgSignKeyID strips commitFlags.gpgSignKey's NoOptDefVal sentinel,
// turning a bare --gpg-sign back into "" (git signs with the configured
// default key) while leaving an explicit --gpg-sign=<key-id> untouched.
func gpgSignKeyID(v string) string {
	if v == gpgSignBare {
		return ""
	}
	return v
}

// targetPaths splits built targets into the plain pathspecs and the files
// named by a symbol anchor, the two sides pathAnchorContradiction compares.
func targetPaths(targets []synth.Target) (paths, anchorFiles []string) {
	for _, t := range targets {
		if t.Pathspec != "" {
			paths = append(paths, t.Pathspec)
			continue
		}
		anchorFiles = append(anchorFiles, t.Symbol.Path)
	}
	return paths, anchorFiles
}

// refuseStructuredDataAnchors implements docs/CODES.md's exit 12: a
// FILE:SYMBOL anchor into JSON, YAML, or TOML is refused before synth ever
// reads the file, because a spliced extent is not guaranteed to agree with
// one of these formats' own grammar and nothing downstream would catch the
// resulting malformed blob before it reached HEAD (AGENTS.md's invariant
// table).
//
// This lives here, at rgit commit's own command surface, rather than
// inside internal/synth: it is commit's own policy on what it accepts to
// stage, not a claim that blob synthesis itself is unreliable for these
// languages -- internal/synth's own tests still stage them directly to
// prove the machinery correct, and rgit diff, blame, and log all resolve
// the identical anchor fine, since none of them writes a blob.
func refuseStructuredDataAnchors(ctx context.Context, repo *gitx.Repo, root string, targets []synth.Target, stderr io.Writer) exitcode.Code {
	fold, err := repo.IgnoreCase(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	for _, t := range targets {
		if t.Pathspec != "" {
			continue
		}
		lang, ok, _ := resolve.LanguageForWorktreePathFolding(root, t.Symbol.Path, fold)
		if !ok || !resolve.IsStructuredData(lang) {
			continue
		}
		fmt.Fprintf(stderr, "rgit: %s: structured-data file; commit it by path instead of a symbol anchor (e.g. rgit commit -m ... %s)\n", t.Symbol.Path, t.Symbol.Path)
		return exitcode.StructuredDataAnchorRefused
	}
	return exitcode.Success
}

// resultLabel renders one staged row: "FILE:NAME" for a symbol anchor, and
// for a pathspec the individual file that row reports on -- a directory or
// glob produces one row per file it stages, so the label has to name the
// file rather than repeat the pathspec. Both match exact copy-paste syntax,
// the same as rgit diff's own labels.
func resultLabel(r synth.TargetResult) string {
	if r.Target.Pathspec != "" {
		return r.Path
	}
	return targetLabel(r.Target)
}

// targetLabel renders a synth.Target the way docs/USAGE.md's warning
// example does: "FILE:NAME" for a symbol anchor, the bare pathspec
// otherwise.
func targetLabel(t synth.Target) string {
	if t.Pathspec != "" {
		return t.Pathspec
	}
	return t.Symbol.Path + ":" + t.Symbol.Anchor
}

func targetResultPaths(results []synth.TargetResult) []string {
	seen := make(map[string]struct{}, len(results))
	paths := make([]string, 0, len(results))
	for _, result := range results {
		if result.Outcome == synth.Unchanged || result.Path == "" {
			continue
		}
		if _, ok := seen[result.Path]; ok {
			continue
		}
		seen[result.Path] = struct{}{}
		paths = append(paths, result.Path)
	}
	return paths
}

// mapStageError turns a synth/resolve error into the exit code
// docs/USAGE.md's table assigns it. Both error types already carry their
// own Code field and format their own message via Error(), so this is a
// pure dispatch, not a second source of truth about what each code means.
func mapStageError(err error) (exitcode.Code, string) {
	if perr, ok := errors.AsType[*synth.PathError](err); ok {
		msg := perr.Error()
		if perr.Code == exitcode.UnsupportedLanguage {
			// synth/stage.go's own error names the extension in its Path,
			// unlike resolve.ResolveError below -- see diff.go's
			// counterpart for why that side needs a different lookup.
			msg += unsupportedLanguageHint(filepath.Ext(perr.Path))
		}
		return perr.Code, msg
	}
	if rerr, ok := errors.AsType[*resolve.ResolveError](err); ok {
		return rerr.Code, rerr.Error()
	}
	// Anything else reaching here ran through gitx (hash-object,
	// update-index, git add, check-ignore, ls-tree) and failed at the git
	// or system level -- docs/USAGE.md's exit 128, mirroring git's own
	// convention for a fatal failure that is not a usage error.
	return exitcode.GitFailure, err.Error()
}

// commitTargets turns rule-classified positionals plus explicit --file/
// --sym flags into synth targets, in the order docs/USAGE.md documents
// pathspecs and anchors mixing freely. It also enforces the one piece of
// path safety rgit owns rather than delegating to git: a pathspec or
// anchor file that resolves outside root is an invalid-usage error (exit
// 129) caught before anything runs, not a fatal git failure discovered
// only after `git add` itself refuses it.
func commitTargets(root, prefix string, classified []cli.Classification, files, syms []string) ([]synth.Target, error) {
	targets := make([]synth.Target, 0, len(classified)+len(files)+len(syms))

	addPathspec := func(p string) error {
		resolved, err := repoPath(root, prefix, p)
		if err != nil {
			return err
		}
		targets = append(targets, synth.PathTarget(resolved))
		return nil
	}
	addAnchor := func(file, name string) error {
		resolved, err := repoPath(root, prefix, file)
		if err != nil {
			return err
		}
		targets = append(targets, synth.AnchorTarget(resolved, name))
		return nil
	}

	for _, c := range classified {
		switch c.Kind {
		case cli.KindPathspec:
			if err := addPathspec(c.Pathspec); err != nil {
				return nil, err
			}
		case cli.KindAnchor:
			if err := addAnchor(c.Anchor.File, c.Anchor.Name); err != nil {
				return nil, err
			}
		default:
			// classified is built with allowRevisions=false (above), so
			// KindRevision/KindRevPath never reach here today -- but a
			// silent skip would stage nothing for a positional the caller
			// named, with no line on stderr to say why, if that ever
			// changes. An internal error is honest; committing part of what
			// was asked for without saying so is not.
			return nil, fmt.Errorf("internal error: commitTargets: unhandled classification kind %v", c.Kind)
		}
	}
	for _, file := range files {
		if err := addPathspec(file); err != nil {
			return nil, err
		}
	}
	for _, sym := range syms {
		file, name, ok := splitAnchor(sym)
		if !ok {
			return nil, fmt.Errorf("malformed --sym value %q", sym)
		}
		if err := addAnchor(file, name); err != nil {
			return nil, err
		}
	}
	return targets, nil
}

// writeTargetRecords is writeTargetListing's machine-readable form:
// FILE<TAB>SYMBOL<TAB>ADDED<TAB>DELETED, one record per staged target, no
// header and no alignment padding. SYMBOL is empty for a pathspec target,
// matching how `rgit diff --porcelain` leaves the column empty for a row
// that owns no anchor.
//
// There is no STATUS column, unlike diff's records: an unchanged target is
// already omitted here (it got its own stderr warning and nothing was
// staged for it), so every record this writes would carry the same value.
func writeTargetRecords(stdout io.Writer, results []synth.TargetResult) {
	for _, r := range results {
		if r.Outcome == synth.Unchanged {
			continue
		}
		symbol := ""
		if r.Target.Pathspec == "" {
			symbol = r.Target.Symbol.Anchor
		}
		fmt.Fprintf(stdout, "%s\t%s\t%d\t%d\n", r.Path, symbol, r.Added, r.Deleted)
	}
}

// writeTargetListing prints one aligned line per staged target with its
// +N/-M, in the plan's order: alphabetical by path, then ascending by
// position within each file. Shared by --dry-run and a successful commit so
// the preview and the real thing are comparable line for line, and so
// neither has to be re-derived by running `rgit diff` again afterwards.
//
// Unchanged targets are omitted; they already got their own warning on
// stderr and nothing was staged for them.
func writeTargetListing(stdout io.Writer, results []synth.TargetResult) {
	width := 0
	for _, r := range results {
		if r.Outcome == synth.Unchanged {
			continue
		}
		if n := len(resultLabel(r)); n > width {
			width = n
		}
	}
	for _, r := range results {
		if r.Outcome == synth.Unchanged {
			continue
		}
		fmt.Fprintf(stdout, "  %-*s  +%d/-%d\n", width, resultLabel(r), r.Added, r.Deleted)
	}
}
