// Package app implements the `rgit commit` command surface: flag parsing, validation, and dispatch
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

	"github.com/spf13/pflag"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/resolve"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/synth"
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
	asJSON       bool
	quiet        bool
	include      bool
	only         bool
	stageOnly    bool
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
// id.
//
// The rewrite follows getopt's rule, which is git's: for a short option
// taking an optional argument, the rest of the token IS the argument. -Ss
// therefore means the key "s", not "-s -S", and that is exactly why this
// cannot expand general shorthand chains -- there is no way to tell a key
// id from a run of flags, and git does not try either. A token that packs
// S in behind other shorthands (-sS) is left alone and pflag rejects it by
// name, which is an honest refusal rather than a silent misread.
//
// A token that is already some flag's value is copied verbatim: `-m -S`
// commits the subject "-S", exactly as plain git does, rather than turning
// the message into a signing request. fs is consulted for which flags take a
// separate value, so the decision is pflag's own flag table rather than a
// second list that could drift from it.
//
// Everything after "--" is a pathspec (docs/USAGE.md § Argument shape) and
// is never rewritten.
func expandGPGSignShorthand(fs *pflag.FlagSet, args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(out, args[i:]...)
		}
		switch {
		case a == "-S":
			out = append(out, "--gpg-sign")
		case strings.HasPrefix(a, "-S"):
			out = append(out, "--gpg-sign="+a[2:])
		default:
			out = append(out, a)
			if takesSeparateValue(fs, a) && i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		}
	}
	return out
}

// takesSeparateValue reports whether pflag will read the following argument
// as arg's value: a known flag (long, or a lone short one) that carries no
// NoOptDefVal, and no "=" supplying the value inline.
func takesSeparateValue(fs *pflag.FlagSet, arg string) bool {
	name, hasValue := strings.CutPrefix(arg, "--")
	if hasValue {
		if name == "" || strings.ContainsRune(name, '=') {
			return false
		}
		f := fs.Lookup(name)
		return f != nil && f.NoOptDefVal == ""
	}
	if len(arg) == 2 && arg[0] == '-' {
		f := fs.ShorthandLookup(arg[1:])
		return f != nil && f.NoOptDefVal == ""
	}
	return false
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
	reeditMessage := registerCommitFlags(fs, &f)

	help := "usage: rgit commit [flags] [target...]\n\n" +
		"Stage named targets -- pathspecs and/or FILE:NAME symbol anchors\n" +
		"(e.g. auth.go:ValidateToken) -- and commit them, or with --stage-only\n" +
		"stage them and stop. Un-staging is plain git: \"git restore --staged FILE\"\n" +
		"is per file, so a half-staged symbol comes out with its whole file.\n\n" +
		fs.FlagUsages() +
		"\nFull reference: docs/USAGE.md\n"
	if code, done := parseFlagsOrHelp(fs, expandGPGSignShorthand(fs, args), stdout, stderr, help); done {
		return code
	}
	if *reeditMessage {
		fmt.Fprintln(stderr, "rgit: --reedit-message is unsupported; use --reuse-message")
		return exitcode.InvalidUsage
	}

	r := &commitRun{f: &f, asJSON: jsonFlag(ctx) || f.asJSON, stdout: stdout, stderr: stderr, invocationDir: dir}
	if code := r.validateFlags(); code != exitcode.Success {
		return code
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

	positionalsGiven, code := r.positionals(fs)
	if code != exitcode.Success {
		return code
	}
	targetCount := len(positionalsGiven) + len(f.syms) + len(f.files)
	if code := r.validateTargetCount(targetCount); code != exitcode.Success {
		return code
	}

	r.warnUnconventionalMessage()

	root, prefix, repo, code := openRepo(ctx, dir, stderr)
	if code != exitcode.Success {
		return code
	}

	if !f.stageOnly && len(f.messages) == 0 && f.msgFile == "" && !autoMessage {
		var code exitcode.Code
		if noEdit, code = r.requireSequencerMessage(ctx, repo); code != exitcode.Success {
			return code
		}
	}

	if code := r.refuseOnlyDuringSequencer(ctx, repo); code != exitcode.Success {
		return code
	}

	// --only commits HEAD plus the targets from a private index, so the
	// shared index is never staged into and a concurrent rgit in the same
	// checkout cannot undo this one's staging. The lock comes before
	// planning because anchors are synthesized against HEAD, which must not
	// move until the commit lands.
	only, release, code := r.acquireOnlyIndex(ctx, repo)
	defer release()
	if code != exitcode.Success {
		return code
	}

	var targetResults []synth.TargetResult
	var indexSnap *synth.IndexSnapshot
	if targetCount > 0 {
		var done bool
		targetResults, indexSnap, code, done = r.stageTargets(ctx, root, prefix, repo, positionalsGiven, only)
		if done {
			return code
		}
	} else if f.dryRun {
		return r.dryRunOutput(nil, false)
	}

	opts, code := r.commitOptions(noEdit, only)
	if code != exitcode.Success {
		return code
	}

	// A hook rejecting the commit rolls staging back to the pre-staging
	// snapshot: Apply stages atomically, but the commit (and any hook it
	// runs) still sees the real index, so without this a rejection would
	// strand the just-staged blobs there. Restore covers every commit
	// failure, not just hook rejections -- git reports both the same way,
	// and the snapshot already holds any pre-existing staged work, so
	// restoring it destroys nothing either way. Exit code is unchanged.
	res, err := repo.Commit(ctx, opts)
	// Hook output goes to the user either way: on success it is the
	// formatter or codegen telling them what it did, and on failure it is
	// usually the reason.
	if len(res.Stderr) > 0 {
		_, _ = stderr.Write(res.Stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		rollbackIndex(ctx, stderr, indexSnap, targetResults)
		return exitcode.GitFailure
	}
	jsonSHA, code := r.summarize(ctx, repo, res.Stdout, targetResults)
	if code != exitcode.Success {
		return code
	}

	if f.push {
		if err := repo.Push(ctx); err != nil {
			r.reportPushFailure(ctx, repo, err)
			if code := emitLeft(ctx, repo, stdout, stderr, r.leftMode(), jsonSHA, targetResults); code != exitcode.Success {
				return code
			}
			return exitcode.PushFailed
		}
	}

	return emitLeft(ctx, repo, stdout, stderr, r.leftMode(), jsonSHA, targetResults)
}

// leftMode is which of emitLeft's three renderings this invocation's own
// output flags ask for. -q promises an empty stdout and --porcelain promises
// records a parser can classify, so neither may receive the human "left:"
// line merely because a --push failed: which flags were given decides the
// framing, and a failure is not a licence to change it.
type leftMode int

const (
	leftText leftMode = iota
	leftPorcelain
	leftJSON
)

func (r *commitRun) leftMode() leftMode {
	switch {
	case r.asJSON:
		return leftJSON
	case r.f.porcelain, r.f.quiet:
		return leftPorcelain
	}
	return leftText
}

// commitRun carries the parsed flags and output streams shared by the
// phases of runCommit.
type commitRun struct {
	f      *commitFlags
	asJSON bool

	// invocationDir is the global -C directory, or "" for the process working
	// directory. -F's path resolves against it, because that is the directory
	// the caller named the file from -- see messageFile.
	invocationDir  string
	stdout, stderr io.Writer
}

func (r *commitRun) usage(format string, a ...any) exitcode.Code {
	fmt.Fprintf(r.stderr, "rgit: "+format+"\n", a...)
	return exitcode.InvalidUsage
}

func registerCommitFlags(fs *pflag.FlagSet, f *commitFlags) (reeditMessage *bool) {
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
	reeditMessage = fs.Bool("reedit-message", false, "unsupported; use --reuse-message")
	fs.StringVar(&f.author, "author", "", "override the commit author")
	fs.StringVar(&f.date, "date", "", "override the commit date")
	fs.BoolVar(&f.resetAuthor, "reset-author", false, "take the author identity from the committer (with --amend)")
	fs.BoolVar(&f.porcelain, "porcelain", false, "list staged targets as stable tab-separated records")
	fs.BoolVar(&f.asJSON, "json", false, "one JSON object of the porcelain records")
	fs.BoolVarP(&f.quiet, "quiet", "q", false, "suppress the commit summary and target listing")
	fs.BoolVarP(&f.include, "include", "i", false, "stage named targets and commit them with other staged work")
	fs.BoolVarP(&f.only, "only", "o", false, "commit only named targets")
	fs.BoolVar(&f.stageOnly, "stage-only", false, "stage the named targets into the index without committing")
	// -S is not registered as a shorthand here; expandGPGSignShorthand
	// rewrites it before Parse, for the pflag reason documented there.
	fs.StringVar(&f.gpgSignKey, "gpg-sign", "", "GPG-sign the commit; -S/-S<key-id>/--gpg-sign=<key-id>")
	fs.Lookup("gpg-sign").NoOptDefVal = gpgSignBare
	fs.BoolVar(&f.noGPGSign, "no-gpg-sign", false, "do not GPG-sign, overriding commit.gpgsign")
	fs.StringVar(&f.pathspecFile, "pathspec-from-file", "", "read targets from a file, or - for stdin")
	fs.BoolVar(&f.pathspecNUL, "pathspec-file-nul", false, "separate pathspec-file targets with NUL")
	return reeditMessage
}

// stageOnlyConflict names the first flag that cannot combine with
// --stage-only, or "" when none does.
func (f *commitFlags) stageOnlyConflict() string {
	switch {
	case f.include:
		return "--include"
	case f.only:
		return "--only"
	case f.push:
		return "--push"
	case f.amend:
		return "--amend"
	case len(f.messages) > 0 || f.msgFile != "":
		return "-m/-F"
	case f.fixup != "" || f.squash != "" || f.reuseMessage != "":
		return "--fixup/--squash/--reuse-message"
	case f.allowEmpty:
		return "--allow-empty"
	}
	return ""
}

// positionals gathers the command-line targets plus any from
// --pathspec-from-file.
func (r *commitRun) positionals(fs *pflag.FlagSet) ([]string, exitcode.Code) {
	given := restoreDoubleDash(fs)
	if r.f.pathspecFile == "" {
		return given, exitcode.Success
	}
	pathspecs, err := readPathspecFile(r.f.pathspecFile, r.f.pathspecNUL)
	if err != nil {
		fmt.Fprintf(r.stderr, "rgit: reading pathspec file: %v\n", err)
		return nil, exitcode.GitFailure
	}
	return append(given, pathspecs...), exitcode.Success
}

// requireSequencerMessage covers a commit with no message of its own: an
// in-progress merge, cherry-pick, or revert supplies git's generated one
// (noEdit true); anything else is a usage error.
func (r *commitRun) requireSequencerMessage(ctx context.Context, repo *gitx.Repo) (noEdit bool, code exitcode.Code) {
	op, ok, err := repo.SequencerOp(ctx)
	if err != nil {
		fmt.Fprintf(r.stderr, "rgit: %v\n", err)
		return false, exitcode.GitFailure
	}
	if ok && (op == "merge" || op == "cherry-pick" || op == "revert") {
		return true, exitcode.Success
	}
	return false, r.usage("commit requires a message (-m or -F)")
}

// refuseOnlyDuringSequencer: the private index holds HEAD's tree, so
// committing it mid-merge would record the merge without the merged content;
// git refuses the same way.
func (r *commitRun) refuseOnlyDuringSequencer(ctx context.Context, repo *gitx.Repo) exitcode.Code {
	if !r.f.only {
		return exitcode.Success
	}
	op, ok, err := repo.SequencerOp(ctx)
	if err != nil {
		fmt.Fprintf(r.stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	if ok && (op == "merge" || op == "cherry-pick" || op == "revert") {
		return r.usage("cannot do a partial commit during a " + op + ".")
	}
	return exitcode.Success
}

// acquireOnlyIndex takes the commit lock and a private index for --only.
// release is always safe to call, including after a failure.
func (r *commitRun) acquireOnlyIndex(ctx context.Context, repo *gitx.Repo) (only *gitx.OnlyIndex, release func(), code exitcode.Code) {
	release = func() {}
	if !r.f.only || r.f.dryRun {
		return nil, release, exitcode.Success
	}
	unlock, err := repo.LockCommits(ctx)
	if err != nil {
		fmt.Fprintf(r.stderr, "rgit: %v\n", err)
		return nil, release, exitcode.GitFailure
	}
	release = unlock
	only, err = repo.NewOnlyIndex(ctx)
	if err != nil {
		fmt.Fprintf(r.stderr, "rgit: %v\n", err)
		return nil, release, exitcode.GitFailure
	}
	release = func() {
		only.Close()
		unlock()
	}
	return only, release, exitcode.Success
}

func (r *commitRun) commitOptions(noEdit bool, only *gitx.OnlyIndex) (gitx.CommitOptions, exitcode.Code) {
	f := r.f
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
		Only:         only,
	}
	if f.msgFile == "-" {
		data, rerr := io.ReadAll(os.Stdin)
		if rerr != nil {
			fmt.Fprintf(r.stderr, "rgit: reading commit message from stdin: %v\n", rerr)
			return opts, exitcode.GitFailure
		}
		opts.MessageFile = "-"
		opts.StdinMessage = data
	} else if f.msgFile != "" {
		path, err := r.messageFile(f.msgFile)
		if err != nil {
			fmt.Fprintf(r.stderr, "rgit: reading commit message file: %v\n", err)
			return opts, exitcode.GitFailure
		}
		opts.MessageFile = path
	}
	return opts, exitcode.Success
}

// messageFile resolves -F's path the way git does: relative to the directory
// rgit was invoked from, not to the repository root. git itself runs every
// commit from the toplevel (-C root), so a bare relative name would otherwise
// read a different file than `git commit -F` in the same directory -- and
// silently commit the wrong message when a same-named file exists at the root.
func (r *commitRun) messageFile(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	dir := r.invocationDir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	return filepath.Join(dir, path), nil
}

// stdinMessageHint names the one spelling that carries a subject and body
// without the shell ever parsing the text. -m is still supported for a
// single line; the refusals below point at the form that always works.
const stdinMessageHint = "use rgit commit --only -F - PATHS <<'EOF' with a subject line, a blank line, then the body"

func (r *commitRun) validateFlags() exitcode.Code {
	f := r.f
	if len(f.messages) > 0 && f.msgFile != "" {
		return r.usage("-m and -F are mutually exclusive; %s", stdinMessageHint)
	}
	// git accepts newlines inside -m, but reaching rgit that way means the
	// caller quoted a multi-line message at the shell, where quoting is
	// what breaks; --only -F - has no quoting to get wrong.
	for _, m := range f.messages {
		if strings.Contains(m, "\n") {
			return r.usage("-m cannot carry a multi-line message; %s", stdinMessageHint)
		}
	}
	if f.dryRun && f.push {
		return r.usage("--dry-run and --push are mutually exclusive")
	}
	// Asking for machine-readable output and for no output is a
	// contradiction, and silently letting one win would leave a script
	// parsing an empty stream that it cannot tell from "nothing staged".
	switch {
	case f.porcelain && f.quiet:
		return r.usage("--porcelain and --quiet are mutually exclusive")
	case r.asJSON && f.quiet:
		return r.usage("--json and --quiet are mutually exclusive")
	case f.porcelain && r.asJSON:
		return r.usage("--porcelain and --json are mutually exclusive")
	}
	// git commit -i and -o are alternate ways to build the commit from
	// paths (builtin/commit.c die_for_incompatible_opt4). rgit's default
	// already follows -i; the flag is that spelling made explicit.
	if f.include && f.only {
		return r.usage("--include and --only are mutually exclusive")
	}
	if f.stageOnly {
		if conflict := f.stageOnlyConflict(); conflict != "" {
			return r.usage("--stage-only and %s are mutually exclusive", conflict)
		}
	}
	if f.pathspecNUL && f.pathspecFile == "" {
		return r.usage("--pathspec-file-nul requires --pathspec-from-file")
	}
	if f.msgFile == "-" && f.pathspecFile == "-" {
		return r.usage("-F - and --pathspec-from-file=- are mutually exclusive")
	}
	return exitcode.Success
}

func (r *commitRun) validateTargetCount(targetCount int) exitcode.Code {
	f := r.f
	if targetCount > 0 {
		return exitcode.Success
	}
	// git refuses --include with an empty pathspec even alongside --amend
	// or --allow-empty ("No paths with --include/--only does not make
	// sense."). --only keeps its amend exception.
	switch {
	case f.include:
		return r.usage("--include requires at least one target")
	case f.only && !f.amend:
		return r.usage("--only requires at least one target")
	case f.stageOnly:
		return r.usage("--stage-only requires at least one target")
	case !f.amend && !f.allowEmpty && f.fixup == "" && f.squash == "" && f.reuseMessage == "":
		return r.usage("commit requires at least one target")
	}
	return exitcode.Success
}

func (r *commitRun) writeJSON(results []synth.TargetResult) exitcode.Code {
	if err := writeCommitJSON(r.stdout, "", results, nil); err != nil {
		fmt.Fprintf(r.stderr, "rgit: commit: %v\n", err)
		return exitcode.GitFailure
	}
	return exitcode.Success
}

// dryRunOutput says what a preview resolved: a preview that prints nothing
// and exits 0 is indistinguishable from one that found nothing.
func (r *commitRun) dryRunOutput(results []synth.TargetResult, withTargets bool) exitcode.Code {
	switch {
	case r.f.porcelain:
		// No preamble: the records are the whole output, so a caller can
		// read them without stripping a human sentence first.
		if withTargets {
			writeTargetRecords(r.stdout, results)
		}
	case r.asJSON:
		return r.writeJSON(results)
	case !r.f.quiet:
		fmt.Fprintln(r.stdout, "dry run: nothing written, nothing staged. Would commit:")
		if withTargets {
			writeTargetListing(r.stdout, results)
		}
	}
	return exitcode.Success
}

func (r *commitRun) stageOnlyOutput(results []synth.TargetResult) exitcode.Code {
	switch {
	case r.f.porcelain:
		writeTargetRecords(r.stdout, results)
	case r.asJSON:
		return r.writeJSON(results)
	case !r.f.quiet:
		fmt.Fprintln(r.stdout, "staged, not committed:")
		writeTargetListing(r.stdout, results)
	}
	return exitcode.Success
}

// planTargets resolves the named targets into a stage plan and prints its
// notices; a non-Success code means the command must exit with it.
func (r *commitRun) planTargets(ctx context.Context, root, prefix string, repo *gitx.Repo, positionals []string) (*synth.Plan, exitcode.Code) {
	checker := &cli.GitPathChecker{Root: root, Prefix: prefix, Repo: repo}
	classified, err := cli.ClassifyArgs(ctx, positionals, false, checker, cli.GitRevisionResolver{Repo: repo})
	if err != nil {
		fmt.Fprintf(r.stderr, "rgit: %v\n", err)
		return nil, exitcode.InvalidUsage
	}

	targets, err := commitTargets(ctx, root, prefix, classified, r.f.files, r.f.syms, checker)
	if err != nil {
		fmt.Fprintf(r.stderr, "rgit: %v\n", err)
		return nil, exitcode.InvalidUsage
	}

	// Checked on the built targets, not the raw arguments: by this point
	// positionals and flags have collapsed into one list and every path has
	// been resolved against the invocation prefix, so "src/a.go" named from
	// a subdirectory and "a.go" named from the root compare equal.
	paths, anchorFiles := targetPaths(targets)
	if code := pathAnchorContradiction(paths, anchorFiles, r.stderr); code != exitcode.Success {
		return nil, code
	}
	if code := refuseStructuredDataAnchors(ctx, repo, root, targets, r.stderr); code != exitcode.Success {
		return nil, code
	}

	plan, err := synth.PlanStage(ctx, repo, root, targets)
	if err != nil {
		code, msg := mapStageError(err)
		fmt.Fprintf(r.stderr, "rgit: %s\n", msg)
		return nil, code
	}
	r.printPlanNotices(plan)
	return plan, exitcode.Success
}

func (r *commitRun) printPlanNotices(plan *synth.Plan) {
	if plan.TSOnly {
		fmt.Fprintln(r.stderr, tsOnlyNotice)
	}
	for _, path := range plan.Preamble {
		fmt.Fprintf(r.stderr, "[notice] %s is new; staging its @header and @imports so the file compiles\n", path)
	}
	for _, e := range plan.Escalated {
		fmt.Fprintf(r.stderr, "[notice] %s: container is new, so the whole container is staged\n", e)
	}
	for _, anchor := range plan.Ordinals {
		fmt.Fprintf(r.stderr, "[warning] anchor '%s' is positional; inserting a symbol above it repoints it -- qualify it where the language allows\n", anchor)
	}
	for _, w := range plan.CountingWarnings {
		fmt.Fprintf(r.stderr, "[warning] %s\n", w)
	}
}

// stageTargets plans and stages the named targets. done reports that the
// command is finished with exit code code (dry run, stage-only, or a
// failure); otherwise the commit proceeds with the returned results and
// pre-staging index snapshot.
func (r *commitRun) stageTargets(ctx context.Context, root, prefix string, repo *gitx.Repo, positionals []string, only *gitx.OnlyIndex) (results []synth.TargetResult, snap *synth.IndexSnapshot, code exitcode.Code, done bool) {
	f := r.f
	plan, code := r.planTargets(ctx, root, prefix, repo, positionals)
	if code != exitcode.Success {
		return nil, nil, code, true
	}

	results = plan.Results
	allUnchanged := len(results) > 0
	for _, res := range results {
		if res.Outcome != synth.Unchanged {
			allUnchanged = false
			continue
		}
		fmt.Fprintf(r.stderr, "[warning] target '%s' has no uncommitted changes; skipping\n", targetLabel(res.Target))
	}

	// docs/USAGE.md § Targets with nothing to commit: exit 11 only when
	// EVERY named target turned out unchanged, and only then -- a mix of
	// changed and unchanged targets is a warning plus a commit of the rest,
	// not a failure. --allow-empty suppresses it.
	if allUnchanged && !f.allowEmpty {
		return results, nil, exitcode.NothingToCommit, true
	}

	// docs/USAGE.md: dry-run "writes no objects, stages nothing, runs no
	// hooks" -- resolution already happened as a pure read.
	if f.dryRun {
		return results, nil, r.dryRunOutput(results, true), true
	}

	// Staging without committing: no hooks run and nothing is committed, so
	// there is no failure left to roll back. Apply is already atomic through
	// its temp index.
	if f.stageOnly {
		if err := plan.Apply(ctx, repo, root); err != nil {
			code, msg := mapStageError(err)
			fmt.Fprintf(r.stderr, "rgit: %s\n", msg)
			return results, nil, code, true
		}
		return results, nil, r.stageOnlyOutput(results), true
	}

	var stageErr error
	if only != nil {
		stageErr = plan.StageInto(ctx, repo, root, only.Repo)
	} else {
		// Snapshot the index before staging anything: Apply stages
		// atomically through a temp index, but the commit itself -- hooks
		// included -- still runs against the real one, so a failure there
		// needs this snapshot to roll the index back to. Refusing to
		// proceed when the snapshot itself fails is the non-destructive
		// answer: staging nothing beats staging what cannot be restored.
		var serr error
		snap, serr = synth.SnapshotIndex(ctx, root)
		if serr != nil {
			fmt.Fprintf(r.stderr, "rgit: %v\n", serr)
			return results, nil, exitcode.GitFailure, true
		}
		stageErr = plan.ApplyWithSnapshot(ctx, repo, root, snap)
	}
	if stageErr != nil {
		code, msg := mapStageError(stageErr)
		fmt.Fprintf(r.stderr, "rgit: %s\n", msg)
		return results, snap, code, true
	}
	return results, snap, exitcode.Success, false
}

// summarize prints the post-commit report and returns the committed SHA
// when --json defers it to the final "left" object.
func (r *commitRun) summarize(ctx context.Context, repo *gitx.Repo, gitStdout []byte, results []synth.TargetResult) (jsonSHA string, code exitcode.Code) {
	switch {
	case r.f.porcelain, r.asJSON:
		// git's own --porcelain replaces its human summary rather than
		// adding to it, and the same rule applies here: relaying the
		// summary would leave a caller parsing records interleaved with
		// prose. --json is that same H + target list as one object.
		sha, ok, revErr := repo.RevParseVerify(ctx, "HEAD")
		if revErr != nil {
			fmt.Fprintf(r.stderr, "rgit: resolving committed SHA: %v\n", revErr)
			return "", exitcode.GitFailure
		}
		if !ok {
			fmt.Fprintln(r.stderr, "rgit: resolving committed SHA: HEAD is unavailable")
			return "", exitcode.GitFailure
		}
		if r.asJSON {
			// Written after the optional push so "left" is the final state.
			return sha, exitcode.Success
		}
		fmt.Fprintf(r.stdout, "H\t%s\n", sha)
		writeTargetRecords(r.stdout, results)
	case r.f.quiet:
		// Nothing on stdout. Hook output and every warning above still
		// went to stderr -- git's own -q suppresses the summary, not
		// diagnostics.
	default:
		// git's own summary -- branch, new SHA, and the changed/insertion/
		// deletion counts. Relaying it verbatim is what stops a caller
		// having to run `git show` afterwards just to find out what landed.
		_, _ = r.stdout.Write(gitStdout)

		// Then the part git cannot report: which symbols went in, and by
		// how much. Same listing and same order as --dry-run, so a preview
		// and the commit it previews are comparable line for line.
		writeTargetListing(r.stdout, results)
	}
	return "", exitcode.Success
}

// reportPushFailure prints a failed push. A push failure does not roll back
// the commit that preceded it (docs/USAGE.md § Flags, AGENTS.md's
// delegation boundary).
func (r *commitRun) reportPushFailure(ctx context.Context, repo *gitx.Repo, pushErr error) {
	fmt.Fprintf(r.stderr, "rgit: %v\n", pushErr)
	// gitx.Push's own doc comment explains why this never becomes an
	// implicit --set-upstream: some push.default settings push a branch
	// with no upstream configured just fine, so guessing -u here could fail
	// a push plain `git push` would have completed. This only adds a
	// concrete, named fix once the push has already failed for its own
	// reason, and only when Upstream independently confirms there genuinely
	// is none.
	if _, hasUpstream, uerr := repo.Upstream(ctx); uerr != nil || hasUpstream {
		return
	}
	if branch, berr := repo.CurrentBranch(ctx); berr == nil && branch != "" && branch != "HEAD" {
		fmt.Fprintf(r.stderr, "rgit: %s has no upstream tracking branch -- try `git push -u origin %s`, or set push.autoSetupRemote to do this for every push\n", branch, branch)
	}
}

// leftState is what remains after a commit, so the caller needs no
// follow-up `git status` / `git log`.
type leftState struct {
	Dirty    []string
	Upstream string
	Ahead    int
}

func (l leftState) String() string {
	s := "left: clean"
	if n := len(l.Dirty); n > 0 {
		names := l.Dirty
		if n > 3 {
			names = append(append([]string{}, names[:3]...), "…")
		}
		s = fmt.Sprintf("left: %d dirty (%s)", n, strings.Join(names, ", "))
	}
	if l.Upstream != "" {
		s += fmt.Sprintf(" · %d ahead of %s", l.Ahead, l.Upstream)
	}
	return s
}

func readLeft(ctx context.Context, repo *gitx.Repo) (leftState, error) {
	var l leftState
	var err error
	if l.Dirty, err = repo.DirtyPaths(ctx); err != nil {
		return l, err
	}
	up, ok, err := repo.Upstream(ctx)
	if err != nil || !ok {
		return l, err
	}
	l.Upstream = up
	l.Ahead, _, err = repo.AheadBehind(ctx)
	return l, err
}

// emitLeft prints the invocation's own final records: the whole JSON object,
// nothing at all under --porcelain (whose H and target rows summarize wrote
// before any push), or the human "left:" line. A failed lookup never fails
// an already-landed commit.
func emitLeft(ctx context.Context, repo *gitx.Repo, stdout, stderr io.Writer, mode leftMode, sha string, results []synth.TargetResult) exitcode.Code {
	l, err := readLeft(ctx, repo)
	if err != nil {
		fmt.Fprintf(stderr, "[warning] reading remaining state: %v\n", err)
	}
	switch mode {
	case leftJSON:
		if err := writeCommitJSON(stdout, sha, results, &l); err != nil {
			fmt.Fprintf(stderr, "rgit: commit: %v\n", err)
			return exitcode.GitFailure
		}
	case leftText:
		if err == nil {
			fmt.Fprintln(stdout, l)
		}
	case leftPorcelain:
		// The H and target rows already carried the result.
	}
	return exitcode.Success
}

// warnUnconventionalMessage flags a -m subject that is not "type(scope):
// subject". [warning], not "rgit: warning:" -- every other advisory in this
// command and in diff.go already uses the bracketed form; one spelling for
// "advisory, not a refusal" across the surface.
func (r *commitRun) warnUnconventionalMessage() {
	if !r.f.stageOnly && r.f.reuseMessage == "" && !hasConventionalShape(r.f.messages) {
		fmt.Fprintln(r.stderr, `[warning] message does not look like "type(scope): subject"`)
	}
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
// FILE:SYMBOL anchor into JSON, JSONC, JSON5, YAML, or TOML is refused before synth ever
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
		lang, ok, _, _ := resolve.LanguageForPathFolding(root, t.Symbol.Path, fold, nil)
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

// rollbackIndex restores the pre-staging index snapshot after a failed
// commit and says what it did: the exact paths Apply had staged, plus the
// reminder that rollback is index-only and no worktree byte moved. A nil
// snapshot means nothing was staged by this invocation (an amend or
// allow-empty over the index as it stands), so there is nothing to undo.
// A restore failure is reported, not hidden: the commit already failed,
// and silence would leave the caller believing staging was rolled back
// when it was not.
func rollbackIndex(ctx context.Context, stderr io.Writer, snap *synth.IndexSnapshot, results []synth.TargetResult) {
	if snap == nil {
		return
	}
	if rerr := snap.Restore(ctx); rerr != nil {
		fmt.Fprintf(stderr, "rgit: restoring pre-commit index: %v\n", rerr)
		return
	}
	fmt.Fprintf(stderr, "rgit: commit failed; staging rolled back to its pre-commit state")
	if touched := targetResultPaths(results); len(touched) > 0 {
		fmt.Fprintf(stderr, ": %s", strings.Join(touched, ", "))
	}
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "rgit: no worktree files were changed")
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
func commitTargets(ctx context.Context, root, prefix string, classified []cli.Classification, files, syms []string, paths *cli.GitPathChecker) ([]synth.Target, error) {
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
		case cli.KindRevision, cli.KindRevPath:
			// classified is built with allowRevisions=false (above), so
			// these cases never reach here today. An internal error is
			// honest if that invariant changes; silently staging only part
			// of what was requested is not.
			return nil, fmt.Errorf("internal error: commitTargets: unhandled classification kind %v", c.Kind)
		}
	}
	for _, file := range files {
		if err := addPathspec(file); err != nil {
			return nil, err
		}
	}
	for _, sym := range syms {
		file, name, ok, err := splitAnchor(ctx, sym, paths)
		if err != nil {
			return nil, err
		}
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
type commitJSONTarget struct {
	File    string `json:"file"`
	Symbol  string `json:"symbol"`
	Added   string `json:"added"`
	Deleted string `json:"deleted"`
}

type commitJSONReport struct {
	Schema  int                `json:"schema"`
	SHA     string             `json:"sha,omitempty"`
	Targets []commitJSONTarget `json:"targets"`
	Left    *commitJSONLeft    `json:"left,omitempty"`
}

type commitJSONLeft struct {
	Dirty    []string `json:"dirty"`
	Upstream string   `json:"upstream,omitempty"`
	Ahead    int      `json:"ahead"`
}

func writeCommitJSON(w io.Writer, sha string, results []synth.TargetResult, l *leftState) error {
	targets := make([]commitJSONTarget, 0)
	for _, r := range results {
		if r.Outcome == synth.Unchanged {
			continue
		}
		symbol := ""
		if r.Target.Pathspec == "" {
			symbol = r.Target.Symbol.Anchor
		}
		targets = append(targets, commitJSONTarget{
			File:    r.Path,
			Symbol:  symbol,
			Added:   fmt.Sprintf("%d", r.Added),
			Deleted: fmt.Sprintf("%d", r.Deleted),
		})
	}
	rep := commitJSONReport{Schema: jsonSchema, SHA: sha, Targets: targets}
	if l != nil {
		rep.Left = &commitJSONLeft{Dirty: append([]string{}, l.Dirty...), Upstream: l.Upstream, Ahead: l.Ahead}
	}
	return encodeJSON(w, rep)
}

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
