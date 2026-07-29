package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

func TestRun_LogHelpAndUsage(t *testing.T) {
	t.Run("--help", func(t *testing.T) {
		t.Chdir(t.TempDir())
		stdout, _, code := runApp(t, "log", "--help")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "usage: rgit log"))
	})

	t.Run("no arguments", func(t *testing.T) {
		t.Chdir(t.TempDir())
		_, stderr, code := runApp(t, "log")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "requires a FILE:SYMBOL anchor"))
	})

	t.Run("bare pathspec is refused, not silently split", func(t *testing.T) {
		chdirTempRepo(t)
		_, stderr, code := runApp(t, "log", "a.go")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "not a plain path"))
	})

	// TestRun_LogHelpAndUsage/"bare -- does not panic" mirrors blame's own
	// regression: a lone "--" is consumed whole by cli.ClassifyArgs's rule 1
	// and yields zero classifications, so indexing classified[0] blindly
	// panicked instead of refusing like any other non-anchor positional.
	t.Run("bare -- does not panic", func(t *testing.T) {
		chdirTempRepo(t)
		_, stderr, code := runApp(t, "log", "--")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "requires a FILE:SYMBOL anchor"))
	})

	t.Run("extra positional is refused", func(t *testing.T) {
		chdirTempRepo(t)
		_, stderr, code := runApp(t, "log", "a.go:A", "a.go:B")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "unexpected extra argument"))
	})

	t.Run("--porcelain and --patch are mutually exclusive", func(t *testing.T) {
		chdirTempRepo(t)
		_, stderr, code := runApp(t, "log", "--porcelain", "-p", "a.go:A")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "mutually exclusive"))
	})
}

// TestRun_LogUnresolvableAnchor pins the same exit-3 refusal blame.go
// already gives an anchor absent from the resolved source -- here, HEAD's
// blob rather than the worktree file, since history is a question about
// what has already been committed.
func TestRun_LogUnresolvableAnchor(t *testing.T) {
	chdirTempRepo(t)

	stdout, stderr, code := runApp(t, "log", "a.go:NoSuchFunc")
	qt.Assert(t, qt.Equals(code, exitcode.AnchorUnresolvable))
	qt.Assert(t, qt.Equals(stdout, ""))
	qt.Assert(t, qt.StringContains(stderr, `"NoSuchFunc"`))
}

// TestRun_LogAmbiguousAnchor mirrors blame's own case: a bare name matching
// two container-qualified members is exit 4, not a silent pick of either.
func TestRun_LogAmbiguousAnchor(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "b.go", "package a\n\ntype X struct{}\n\nfunc (x X) Get() int { return 1 }\n\n"+
		"type Y struct{}\n\nfunc (y Y) Get() int { return 2 }\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "chore: two Gets")

	_, stderr, code := runApp(t, "log", "b.go:Get")
	qt.Assert(t, qt.Equals(code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.StringContains(stderr, "ambiguous"))
}

// TestRun_LogUnsupportedLanguage pins exit 9 for a file with no grammar
// registered at all, the same code blame and commit give an identical
// anchor.
func TestRun_LogUnsupportedLanguage(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "note.txt", "hello\n")
	gitOut(t, dir, "add", "note.txt")

	_, stderr, code := runApp(t, "log", "note.txt:Anything")
	qt.Assert(t, qt.Equals(code, exitcode.UnsupportedLanguage))
	qt.Assert(t, qt.StringContains(stderr, "unsupported language"))
}

// TestRun_LogDefaultIsPatchFreeAndListsOnlyTouchingCommits pins the
// non-negotiable guardrail TODO.md states in full: patches are opt-in,
// never default, and the stream is bounded by commit count, not code size.
// A commit that only touched B must never appear in A's own history, and
// no patch marker may leak into the default output no matter how many
// commits touched the symbol.
func TestRun_LogDefaultIsPatchFreeAndListsOnlyTouchingCommits(t *testing.T) {
	dir := chdirTempRepo(t) // "chore: initial" already touches A and B

	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "fix(a): bump A")

	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 222\n}\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "fix(b): bump B")

	stdout, stderr, code := runApp(t, "log", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "diff --git")))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "@@")))
	qt.Assert(t, qt.StringContains(stdout, "fix(a): bump A"))
	qt.Assert(t, qt.StringContains(stdout, "chore: initial"))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "fix(b): bump B")))

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	qt.Assert(t, qt.Equals(len(lines), 2))
}

// TestRun_LogPorcelainEmitsTabSeparatedRecords pins docs/CODES.md's
// HASH<TAB>SUBJECT record shape: exactly two fields per line, no header.
func TestRun_LogPorcelainEmitsTabSeparatedRecords(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "fix(a): bump A")

	stdout, stderr, code := runApp(t, "log", "--porcelain", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	qt.Assert(t, qt.Equals(len(lines), 2))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		qt.Assert(t, qt.Equals(len(fields), 2))
		qt.Assert(t, qt.IsTrue(len(fields[0]) >= 7)) // a real commit hash, not truncated to nothing
	}
}

// TestRun_LogPatchFlagIncludesPatch pins that -p/--patch actually reaches
// git: unlike the default, its output carries the real patch body.
func TestRun_LogPatchFlagIncludesPatch(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "fix(a): bump A")

	for _, flag := range []string{"-p", "--patch"} {
		t.Run(flag, func(t *testing.T) {
			stdout, _, code := runApp(t, "log", flag, "a.go:A")
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			qt.Assert(t, qt.StringContains(stdout, "diff --git"))
			qt.Assert(t, qt.StringContains(stdout, "return 111"))
		})
	}
}

// TestRun_LogResolvesAgainstHEADNotWorktree is the regression test for the
// design decision itself: the anchor is resolved against HEAD's own blob,
// never the worktree copy. An uncommitted edit that shifts A's line numbers
// in the worktree (without touching HEAD at all) must not change what `git
// log -L` is asked to bound -- git log -L walks HEAD's own history and has
// no notion of the worktree, so a line range derived from the shifted
// worktree copy would silently point at the wrong lines of every historical
// blob. Resolving against HEAD, as this command does, is immune to that by
// construction.
func TestRun_LogResolvesAgainstHEADNotWorktree(t *testing.T) {
	dir := chdirTempRepo(t) // "chore: initial" touches A

	writeAppFile(t, dir, "a.go", "package a\n\n// leading comment shifting everything below\n\n"+
		"// A returns one.\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	stdout, stderr, code := runApp(t, "log", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.StringContains(stdout, "chore: initial"))
}

// TestRun_LogSurvivesWorktreeDeletion is the same design decision from the
// other side: a symbol's history is reachable even with nothing left on
// disk to open, because HEAD's blob is read directly (gitx.CatFile) rather
// than a worktree file (blame.go's os.ReadFile, which this command does not
// share). cli.GitPathChecker's own existence rule already allows a
// HEAD-only path through classification.
func TestRun_LogSurvivesWorktreeDeletion(t *testing.T) {
	dir := chdirTempRepo(t)
	if err := os.Remove(filepath.Join(dir, "a.go")); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "log", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.StringContains(stdout, "chore: initial"))
}
