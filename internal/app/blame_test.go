package app

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

func TestRun_BlameHelpAndUsage(t *testing.T) {
	t.Run("--help", func(t *testing.T) {
		t.Chdir(t.TempDir())
		stdout, _, code := runApp(t, "blame", "--help")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "usage: rgit blame"))
	})

	t.Run("no arguments", func(t *testing.T) {
		t.Chdir(t.TempDir())
		_, stderr, code := runApp(t, "blame")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "requires a FILE:SYMBOL anchor"))
	})

	t.Run("bare pathspec is refused, not silently split", func(t *testing.T) {
		chdirTempRepo(t)
		_, stderr, code := runApp(t, "blame", "a.go")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "not a plain path"))
	})

	// TestRun_BlameHelpAndUsage/"bare -- does not panic" pins the regression:
	// a lone "--" is consumed whole by cli.ClassifyArgs's rule 1 (everything
	// after "--" is a pathspec, always) and yields zero classifications, so
	// indexing classified[0] blindly panicked instead of refusing like any
	// other non-anchor positional.
	t.Run("bare -- does not panic", func(t *testing.T) {
		chdirTempRepo(t)
		_, stderr, code := runApp(t, "blame", "--")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "requires a FILE:SYMBOL anchor"))
	})

	t.Run("extra positional is refused", func(t *testing.T) {
		chdirTempRepo(t)
		_, stderr, code := runApp(t, "blame", "a.go:A", "a.go:B")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "unrecognized argument"))
	})

	// TestRun_BlameHelpAndUsage/"unknown flag is refused before positional
	// classification" pins that a "-"-prefixed token which is none of
	// blame's own flags is rejected outright, rather than silently falling
	// through to the default case and being treated as the FILE:SYMBOL
	// positional itself.
	t.Run("unknown flag is refused before positional classification", func(t *testing.T) {
		chdirTempRepo(t)
		_, stderr, code := runApp(t, "blame", "--nope")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, `unrecognized argument "--nope"`))
	})
}

// TestRun_BlameUnresolvableAnchorNeverWidensToWholeFile pins the guardrail
// TODO.md states in full: an anchor that does not resolve is exit 3, never
// a silently widened whole-file blame. Asserting stdout is empty is the
// point of the test, not just the exit code -- a whole-file fallback would
// still exit non-zero-adjacent but would leak the file's blame anyway.
func TestRun_BlameUnresolvableAnchorNeverWidensToWholeFile(t *testing.T) {
	chdirTempRepo(t)

	stdout, stderr, code := runApp(t, "blame", "a.go:NoSuchFunc")
	qt.Assert(t, qt.Equals(code, exitcode.AnchorUnresolvable))
	qt.Assert(t, qt.Equals(stdout, ""))
	qt.Assert(t, qt.StringContains(stderr, `"NoSuchFunc"`))
}

// TestRun_BlameAmbiguousAnchor pins exit 4 through the same ResolveError
// path the unresolvable case uses, for a bare name that names two
// container-qualified members.
func TestRun_BlameAmbiguousAnchor(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "b.go", "package a\n\ntype X struct{}\n\nfunc (x X) Get() int { return 1 }\n\n"+
		"type Y struct{}\n\nfunc (y Y) Get() int { return 2 }\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "chore: two Gets")

	_, stderr, code := runApp(t, "blame", "b.go:Get")
	qt.Assert(t, qt.Equals(code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.StringContains(stderr, "ambiguous"))
}

// TestRun_BlameUnsupportedLanguage pins exit 9 for a file with no grammar
// registered at all, the same code rgit commit gives an identical anchor.
func TestRun_BlameUnsupportedLanguage(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "note.txt", "hello\n")
	gitOut(t, dir, "add", "note.txt")

	_, stderr, code := runApp(t, "blame", "note.txt:Anything")
	qt.Assert(t, qt.Equals(code, exitcode.UnsupportedLanguage))
	qt.Assert(t, qt.StringContains(stderr, "unsupported language"))
}

// TestRun_BlameBoundsToTheSymbolExtent is the happy path and the token-case
// TODO.md accepted this command against: the output must name the blamed
// symbol's own content and must NOT include the sibling function's, proving
// the -L bound actually narrowed git's own blame rather than covering the
// whole file.
func TestRun_BlameBoundsToTheSymbolExtent(t *testing.T) {
	chdirTempRepo(t)

	stdout, stderr, code := runApp(t, "blame", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.StringContains(stdout, "return 1"))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "return 2")))
}

// TestRun_BlamePorcelainReachesGit pins that --porcelain is not swallowed:
// git's own porcelain blame format names the author on its own line, which
// the default human-readable format does not.
func TestRun_BlamePorcelainReachesGit(t *testing.T) {
	chdirTempRepo(t)

	stdout, _, code := runApp(t, "blame", "a.go:A", "--porcelain")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "\nauthor "))
}
