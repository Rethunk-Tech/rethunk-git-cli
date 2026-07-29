package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// openRepo resolves the current working directory's git toplevel and
// returns a Repo rooted there. Every subcommand needs this before
// classification, since rules 3-5 all query git or the filesystem.
func openRepo(ctx context.Context, stderr io.Writer) (root, prefix string, repo *gitx.Repo, code exitcode.Code) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return "", "", nil, exitcode.GitFailure
	}

	probe := gitx.New(cwd)
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

	return top, pfx, gitx.New(top), exitcode.Success
}

// gatedExtensionHint maps an extension whose grammar exists upstream but
// may be compiled out because it sits behind a build tag, to the tag that
// would enable it -- currently SQL alone (internal/resolve/lang_sql.go's
// own rgit_sql tag).
//
// This duplicates a fact internal/resolve already knows, and does so only
// because there is nowhere else to ask it from once the tag is off:
// resolve.LanguageInfo.Gated (lang.go) can only describe an adapter that IS
// registered, by that struct's own doc comment -- a build without the tag
// never registers "sql" at all, so resolve.Languages() has no entry to read
// Gated off of. The clean fix is a small always-compiled file in
// internal/resolve, beside lang_sql.go's own gate, that can answer "what
// might a different build tag enable" even when this one lacks it; that
// package is outside this worker's fence, so this map is the seam-side
// stand-in -- see the worker report.
var gatedExtensionHint = map[string]string{
	".sql": "rgit_sql",
}

// unsupportedLanguageHint returns a rebuild suggestion for ext when it is a
// known gated extension this exact build was not compiled with, or "" when
// ext is genuinely unsupported (no grammar exists at all, gated or not) or
// is in fact already compiled in -- the latter check against
// resolve.Languages() so this can never contradict `rgit languages`, even
// if gatedExtensionHint above ever drifted from lang_sql.go's own claim.
//
// Both mapStageError (commit.go) and runDiff's own resolve.ResolveError
// handling (diff.go) call this on an exitcode.UnsupportedLanguage failure,
// so a .sql anchor gets the same rebuild hint whichever subcommand hit it.
func unsupportedLanguageHint(ext string) string {
	tag, known := gatedExtensionHint[ext]
	if !known {
		return ""
	}
	for _, l := range resolve.Languages() {
		if slices.Contains(l.Extensions, ext) {
			return "" // actually compiled in; the failure is something else
		}
	}
	return fmt.Sprintf(" (this build was compiled without -tags %s; installing the tree-sitter CLI and rebuilding would enable it -- see docs/INSTALL.md § SQL support, or run rgit doctor)", tag)
}
