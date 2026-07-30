package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/pflag"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// splitAnchor splits a FILE:NAME anchor the way cli.ClassifyArgs's rule 5
// does: at the LAST colon, since a path may itself contain one. ok is
// false when the value has no colon, nothing before it, or nothing after
// it -- none of which names a symbol. Both subcommands and the
// contradiction check share this so a value one accepts is never one
// another silently drops.
func splitAnchor(s string) (file, name string, ok bool) {
	idx := strings.LastIndexByte(s, ':')
	if idx <= 0 || idx == len(s)-1 {
		return "", "", false
	}
	return s[:idx], s[idx+1:], true
}

// checkPathEscape refuses a pathspec or anchor file whose path climbs
// above root via "..". A leading-colon pathspec is magic passed through
// verbatim (docs/USAGE.md § Argument shape), not a literal path, so it is
// exempt -- there is nothing here to resolve against root at all.
//
// This is the one piece of path safety rgit owns rather than delegating,
// so both subcommands apply it: docs/USAGE.md's exit table lists a path
// escape as 129 without qualifying it to commit.
func checkPathEscape(root, path string) error {
	if strings.HasPrefix(path, ":") {
		return nil
	}
	rel, err := filepath.Rel(root, filepath.Join(root, path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes the repository root", path)
	}
	return nil
}

// repoPath resolves one caller-supplied path to its root-relative form and
// refuses it if it climbs above root. Every pathspec and every anchor file
// in both subcommands goes through here, so the one piece of path safety
// rgit owns rather than delegating to git cannot end up applied on one
// command and skipped on the other.
func repoPath(root, prefix, path string) (string, error) {
	p := cli.PrefixPath(prefix, path)
	if err := checkPathEscape(root, p); err != nil {
		return "", err
	}
	return p, nil
}

// newTargetFlagSet builds the flag set both subcommands start from: git's
// own interspersed parsing (git accepts flags after positionals, and stdlib
// flag does not), errors reported by us rather than by pflag's usage
// printer, and the --sym/--file pair, which names targets identically on
// each command. Callers add their own flags to the returned set.
func newTargetFlagSet(name string, syms, files *[]string) *pflag.FlagSet {
	fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
	fs.SetInterspersed(true)
	fs.SetOutput(io.Discard)
	fs.StringArrayVar(syms, "sym", nil, "explicit FILE:NAME anchor (repeatable)")
	fs.StringArrayVar(files, "file", nil, "explicit pathspec (repeatable)")
	return fs
}

// parseFlagsOrHelp runs fs.Parse for both subcommands, reporting a parse
// failure as docs/USAGE.md's exit 129 with the same message shape on
// either, and handling -h/--help. pflag's ContinueOnError returns
// pflag.ErrHelp from Parse rather than printing anything itself -- the
// FlagSet's own output is discarded (see newTargetFlagSet) precisely so
// nothing is written by accident -- so this is the one place that turns
// ErrHelp into the caller-supplied help text on stdout at exit 0, instead
// of the exit-129 usage error every other Parse failure gets.
//
// done is true whenever the caller must return code immediately: on any
// Parse failure, ErrHelp included. A successful help print is also a stop
// -- it must not fall through into validation that assumes flags were
// meant to be acted on (e.g. reporting a missing commit message after just
// printing how to supply one).
func parseFlagsOrHelp(fs *pflag.FlagSet, args []string, stdout, stderr io.Writer, help string) (code exitcode.Code, done bool) {
	err := fs.Parse(args)
	if err == nil {
		return exitcode.Success, false
	}
	if errors.Is(err, pflag.ErrHelp) {
		fmt.Fprint(stdout, help)
		return exitcode.Success, true
	}
	fmt.Fprintf(stderr, "rgit: %v\n", err)
	return exitcode.InvalidUsage, true
}

// pathAnchorContradiction implements docs/USAGE.md's "--sym and --file on
// the same path → exit 5": naming a path both ways is a contradiction the
// caller must resolve, not a case rgit could silently pick a side on.
//
// It runs on the merged, prefix-resolved targets rather than on the flag
// values alone, so the positional form is held to the identical rule. The
// two spellings have to agree: a whole-path target stages every byte of the
// file, while an anchor target stages a blob synthesized from HEAD plus one
// extent, and the synthesized blob is written to the index second. Letting
// both through means the anchor silently discards the rest of the file's
// worktree changes -- the caller asked for the whole path and would get one
// symbol, with nothing on stderr to say so.
func pathAnchorContradiction(paths, anchorFiles []string, stderr io.Writer) exitcode.Code {
	pathSet := make(map[string]bool, len(paths))
	for _, path := range paths {
		pathSet[path] = true
	}
	for _, file := range anchorFiles {
		if pathSet[file] {
			fmt.Fprintf(stderr, "rgit: %q is named both as a path and as a symbol anchor\n", file)
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
		// -F was used (the message lives in a file the execution step
		// reads, not something this parse-only phase inspects), or
		// --amend/--fixup/--squash is generating its own message ("reuse
		// HEAD's" via --no-edit, or "fixup!"/"squash! <subject>") --
		// either way there is no in-process string here to judge, so none
		// of these warn.
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

// openRepo resolves dir's git toplevel and returns a Repo rooted there.
// Every subcommand needs this before classification, since rules 3-5 all
// query git or the filesystem.
//
// dir is the global -C option's directory (app.go's parseChdir), or "" for
// the process working directory -- the only two things "where was rgit
// started" can mean, resolved here rather than at each of the five call
// sites, so no subcommand can end up reading one of them and its neighbour
// the other.
func openRepo(ctx context.Context, dir string, stderr io.Writer) (root, prefix string, repo *gitx.Repo, code exitcode.Code) {
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return "", "", nil, exitcode.GitFailure
		}
		dir = cwd
	}

	probe := gitx.New(dir)
	top, err := probe.Toplevel(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return "", "", nil, exitcode.GitFailure
	}

	// Read the prefix from the invocation directory, not the toplevel --
	// it is precisely the difference between the two.
	pfx, err := probe.ShowPrefix(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return "", "", nil, exitcode.GitFailure
	}

	return top, pfx, probe.Reroot(top), exitcode.Success
}

// anchorSourceFunc fetches the blob resolveAnchorExtent resolves an
// anchor's extent against. blame's reads the worktree copy at root/file;
// log's reads HEAD's own blob via git cat-file -- the one place the two
// commands genuinely differ (docs/USAGE.md: blame resolves the worktree,
// log resolves HEAD). ok is false when there is nothing to resolve against
// (a deleted worktree file, a file with no HEAD history); notFoundDetail is
// then the parenthetical resolveAnchorExtent appends to the
// AnchorUnresolvable message it emits itself ("no longer exists in the
// worktree", "has no HEAD history").
type anchorSourceFunc func(ctx context.Context, repo *gitx.Repo, root, file string) (src []byte, notFoundDetail string, ok bool, err error)

// resolveAnchorExtent is the wiring blame.go and log.go otherwise duplicate
// in full: open the repo, classify positional through the six-rule table,
// require it land on a FILE:SYMBOL anchor (cli.KindAnchor), resolve the
// file's language (with the same worktree-shebang fallback internal/diff's
// own validateSym uses), fetch source via fetchSource, and resolve the
// anchor's extent against it.
//
// cmdName and help supply each command's own "<cmdName> requires a
// FILE:SYMBOL anchor[, not a plain path]" wording and usage text -- kept
// distinct per caller on purpose; homogenising user-facing strings across
// commands is a separate call this helper does not make.
//
// On success code is exitcode.Success and repo, file, src, res are all
// populated; otherwise every refusal has already been written to stderr
// and the caller must return code immediately.
func resolveAnchorExtent(ctx context.Context, dir string, stderr io.Writer, positional, cmdName, help string, fetchSource anchorSourceFunc) (repo *gitx.Repo, file string, src []byte, res *resolve.Resolution, code exitcode.Code) {
	root, prefix, repo, code := openRepo(ctx, dir, stderr)
	if code != exitcode.Success {
		return nil, "", nil, nil, code
	}

	// Reused rather than hand-parsed: the six-rule precedence table
	// (internal/cli) is what already decides pathspec vs. anchor for
	// commit and diff, and a bare pathspec here (an existing path with no
	// name after it) must be refused the same way rather than silently
	// misread as an anchor with an empty name.
	checker := cli.GitPathChecker{Root: root, Prefix: prefix, Repo: repo}
	classified, err := cli.ClassifyArgs(ctx, []string{positional}, false, checker, cli.GitRevisionResolver{Repo: repo})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return nil, "", nil, nil, exitcode.InvalidUsage
	}
	if len(classified) == 0 {
		// A bare "--" is consumed whole by rule 1 (everything after "--" is
		// a pathspec, always) and classifies to nothing -- the same refusal
		// as no positional at all, not a classified[0] panic.
		fmt.Fprintf(stderr, "rgit: %s requires a FILE:SYMBOL anchor\n", cmdName)
		fmt.Fprint(stderr, help)
		return nil, "", nil, nil, exitcode.InvalidUsage
	}
	c := classified[0]
	if c.Kind != cli.KindAnchor {
		fmt.Fprintf(stderr, "rgit: %s requires a FILE:SYMBOL anchor, not a plain path\n", cmdName)
		fmt.Fprint(stderr, help)
		return nil, "", nil, nil, exitcode.InvalidUsage
	}

	file, err = repoPath(root, prefix, c.Anchor.File)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return nil, "", nil, nil, exitcode.InvalidUsage
	}

	lang, ok := resolve.ForExtension(filepath.Ext(file))
	if !ok {
		// Same worktree-shebang fallback as internal/diff's validateSym: an
		// extensionless script only resolves if its worktree copy is there
		// to peek a shebang line from.
		if line, peeked := resolve.PeekShebangLine(filepath.Join(root, file)); peeked {
			lang, ok = resolve.ForPath(file, line)
		}
	}
	if !ok {
		rerr := &resolve.ResolveError{Code: exitcode.UnsupportedLanguage, Anchor: c.Anchor.Name}
		fmt.Fprintf(stderr, "rgit: %s\n", rerr.Error()+unsupportedLanguageHint(filepath.Ext(file)))
		return nil, "", nil, nil, rerr.Code
	}

	src, notFoundDetail, ok, err := fetchSource(ctx, repo, root, file)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return nil, "", nil, nil, exitcode.GitFailure
	}
	if !ok {
		rerr := &resolve.ResolveError{Code: exitcode.AnchorUnresolvable, Anchor: c.Anchor.Name}
		fmt.Fprintf(stderr, "rgit: %s (%q %s)\n", rerr.Error(), file, notFoundDetail)
		return nil, "", nil, nil, rerr.Code
	}

	res, err = resolve.Resolve(lang, src, c.Anchor.Name)
	if err != nil {
		var rerr *resolve.ResolveError
		if errors.As(err, &rerr) {
			fmt.Fprintf(stderr, "rgit: %s\n", rerr.Error())
			return nil, "", nil, nil, rerr.Code
		}
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return nil, "", nil, nil, exitcode.GitFailure
	}

	return repo, file, src, res, exitcode.Success
}

// unsupportedLanguageHint returns a rebuild suggestion for ext when it is a
// known gated extension this exact build was not compiled with, or "" when
// ext is genuinely unsupported (no grammar exists at all, gated or not) or
// is in fact already compiled in. resolve.GatedTag (internal/resolve/
// gated.go) owns which extensions are gated and by what tag, and already
// checks the "already registered" case against its own registry -- this
// function owns only the user-facing sentence.
//
// Both mapStageError (commit.go) and runDiff's own resolve.ResolveError
// handling (diff.go) call this on an exitcode.UnsupportedLanguage failure,
// so a .sql anchor gets the same rebuild hint whichever subcommand hit it.
func unsupportedLanguageHint(ext string) string {
	tag, ok := resolve.GatedTag(ext)
	if !ok {
		return ""
	}
	return fmt.Sprintf(" (this build was compiled without -tags %s; installing the tree-sitter CLI and rebuilding would enable it -- see docs/INSTALL.md § SQL support, or run rgit doctor)", tag)
}
