// No t.Parallel here: every case changes directory (app_test.go's own
// package comment explains why), which t.Chdir forbids combining with it.
package app

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

// assertAnchorUsageRefusals runs the usage-refusal matrix blame_test.go and
// log_test.go used to each pin in full separately (m31): the six shapes
// every "one FILE:SYMBOL positional" command built on shared.go's
// parseAnchorCommandArgs must refuse the same way -- --help, no arguments,
// a bare pathspec, a bare "--", a second positional, and an unrecognized
// flag. One table instead of two hand-copies that could drift the moment
// only one of them is updated for a shared-loop change.
func assertAnchorUsageRefusals(t *testing.T, cmd string) {
	t.Helper()

	t.Run("--help", func(t *testing.T) {
		t.Chdir(t.TempDir())
		stdout, _, code := runApp(t, cmd, "--help")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "usage: rgit "+cmd))
	})

	t.Run("no arguments", func(t *testing.T) {
		t.Chdir(t.TempDir())
		_, stderr, code := runApp(t, cmd)
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "requires a FILE:SYMBOL anchor"))
	})

	t.Run("bare pathspec is refused, not silently split", func(t *testing.T) {
		chdirTempRepo(t)
		_, stderr, code := runApp(t, cmd, "a.go")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "not a plain path"))
	})

	// "bare -- does not panic" pins the regression: a lone "--" is consumed
	// whole by cli.ClassifyArgs's rule 1 (everything after "--" is a
	// pathspec, always) and yields zero classifications, so indexing
	// classified[0] blindly panicked instead of refusing like any other
	// non-anchor positional.
	t.Run("bare -- does not panic", func(t *testing.T) {
		chdirTempRepo(t)
		_, stderr, code := runApp(t, cmd, "--")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "requires a FILE:SYMBOL anchor"))
	})

	t.Run("extra positional is refused", func(t *testing.T) {
		chdirTempRepo(t)
		_, stderr, code := runApp(t, cmd, "a.go:A", "a.go:B")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "unrecognized argument"))
	})

	// "unknown flag is refused before positional classification" pins that
	// a "-"-prefixed token which is none of the command's own flags is
	// rejected outright, rather than silently falling through and being
	// treated as the FILE:SYMBOL positional itself.
	t.Run("unknown flag is refused before positional classification", func(t *testing.T) {
		chdirTempRepo(t)
		_, stderr, code := runApp(t, cmd, "--nope")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, `unrecognized argument "--nope"`))
	})
}

func TestRun_BlameHelpAndUsage(t *testing.T) {
	assertAnchorUsageRefusals(t, "blame")
}

// TestRun_BlameUnresolvableAnchorNeverWidensToWholeFile pins the guardrail
// docs/USAGE.md § Blame states in full: an anchor that does not resolve is
// exit 3, never a silently widened whole-file blame. Asserting stdout is
// empty is the point of the test, not just the exit code -- a whole-file
// fallback would still exit non-zero-adjacent but would leak the file's
// blame anyway.
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

// TestRun_BlameBoundsToTheSymbolExtent is the happy path and the token case
// specs/design.md § Commands accepted this command against: the output
// must name the blamed symbol's own content and must NOT include the
// sibling function's, proving the -L bound actually narrowed git's own
// blame rather than covering the whole file.
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

// TestRun_BlameShortPorcelainFlagMatchesGit pins that "-p" is accepted as an
// alias for "--porcelain", exactly matching git blame's own flag (unlike
// "git log -p" or "git diff -p", git blame's "-p" already means porcelain,
// not patch -- blame has no patch mode to opt into).
func TestRun_BlameShortPorcelainFlagMatchesGit(t *testing.T) {
	chdirTempRepo(t)

	long, _, longCode := runApp(t, "blame", "a.go:A", "--porcelain")
	short, _, shortCode := runApp(t, "blame", "a.go:A", "-p")
	qt.Assert(t, qt.Equals(shortCode, exitcode.Success))
	qt.Assert(t, qt.Equals(shortCode, longCode))
	qt.Assert(t, qt.Equals(short, long))
}
