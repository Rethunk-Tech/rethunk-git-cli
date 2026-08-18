// No t.Parallel here: every case changes directory (app_test.go's own
// package comment explains why), which t.Chdir forbids combining with it.
package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

// assertAnchorUsageRefusals runs the usage-refusal matrix shared by
// blame_test.go and log_test.go: the six shapes
// every "one FILE:SYMBOL positional" command built on shared.go's
// parseAnchorCommandArgs must refuse the same way -- --help, no arguments,
// a bare pathspec, a bare "--", a second positional, and an unrecognized
// flag. One table instead of two hand-copies that could drift the moment
// only one of them is updated for a shared-loop change.
func assertAnchorUsageRefusals(t *testing.T, cmd string) {
	t.Helper()

	t.Chdir(t.TempDir())
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			stdout, stderr, code := runApp(t, cmd, flag)
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			qt.Assert(t, qt.Equals(stderr, ""))
			qt.Assert(t, qt.StringContains(stdout, "usage: rgit "+cmd))
		})
	}

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

func TestRun_BlameHelpEquality(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			stdout, stderr, code := runApp(t, "blame", flag)
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			qt.Assert(t, qt.Equals(stdout, blameHelp))
			qt.Assert(t, qt.Equals(stderr, ""))
		})
	}
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

// TestRun_BlameDeletedWorktreeUsesHEAD keeps blame and its line range tied to
// the same HEAD blob when the worktree copy is absent.
func TestRun_BlameDeletedWorktreeUsesHEAD(t *testing.T) {
	dir, _ := gittest.New(t)
	writeAppFile(t, dir, "gone.go", "package gone\n\nfunc Gone() int {\n\treturn 1\n}\n\nfunc Other() int {\n\treturn 2\n}\n")
	gittest.Commit(t, dir, "chore: add gone.go")
	t.Chdir(dir)

	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "blame", "gone.go:Gone")
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

// TestRun_BlameOrdinalAnchorWarns pins docs/ANCHORS.md's ordinal-anchor
// advisory beyond commit: an anchor resolved by position ("init#2") warns
// on stderr, but only when the anchor is actually ordinal-shaped -- a
// uniquely named anchor on the same file must stay silent.
func TestRun_BlameOrdinalAnchorWarns(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "dup.go", "package main\n\nfunc init() { println(1) }\n\nfunc init() { println(2) }\n")
	gitOut(t, dir, "add", "dup.go")

	stdout, stderr, code := runApp(t, "blame", "dup.go:init#2")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "println(2)"))
	qt.Assert(t, qt.StringContains(stderr, "dup.go:init#2"))
	qt.Assert(t, qt.StringContains(stderr, "positional"))

	_, stderr, code = runApp(t, "blame", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "positional")))
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

// TestRun_BlameFollowRenameCrossesARenameThatReordersTheSymbol pins the
// rename boundary: a reordered symbol needs a fresh extent in the old blob,
// so --follow-rename reaches the pre-rename path while the default form remains
// bounded to the current path.
func TestRun_BlameFollowRenameCrossesARenameThatReordersTheSymbol(t *testing.T) {
	dir := chdirTempRepo(t)
	t.Setenv("GIT_AUTHOR_DATE", "2020-01-01T00:00:00")
	t.Setenv("GIT_COMMITTER_DATE", "2020-01-01T00:00:00")
	writeAppFile(t, dir, "old.go", "package p\n\nfunc Foo() int {\n\treturn 1\n}\n\nfunc Bar() int {\n\treturn 100\n}\n")
	gitOut(t, dir, "add", "old.go")
	gitOut(t, dir, "commit", "-m", "feat: add old.go")

	gitOut(t, dir, "mv", "old.go", "new.go")
	t.Setenv("GIT_AUTHOR_DATE", "2025-01-01T00:00:00")
	t.Setenv("GIT_COMMITTER_DATE", "2025-01-01T00:00:00")
	writeAppFile(t, dir, "new.go", "package p\n\nfunc Bar() int {\n\treturn 100\n}\n\nfunc Foo() int {\n\treturn 2\n}\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "refactor: rename and reorder")

	t.Setenv("GIT_AUTHOR_DATE", "2030-01-01T00:00:00")
	t.Setenv("GIT_COMMITTER_DATE", "2030-01-01T00:00:00")
	writeAppFile(t, dir, "new.go", "package p\n\nfunc Bar() int {\n\treturn 100\n}\n\nfunc Foo() int {\n\treturn 3\n}\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "fix: bump Foo")

	t.Run("without the flag, blame stops at the current name", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "blame", "new.go:Foo")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "return 3"))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "return 1")))
	})

	t.Run("--follow-rename reaches the pre-rename path", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "blame", "new.go:Foo", "--follow-rename")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "return 3"))
		qt.Assert(t, qt.StringContains(stdout, "return 1"))
	})

	t.Run("--follow-rename preserves porcelain across segments", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "blame", "new.go:Foo", "--follow-rename", "--porcelain")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.Not(qt.Equals(stdout, "")))
		qt.Assert(t, qt.StringContains(stdout, "return 1"))
	})
}

// TestRun_BlameFollowRenameNoRenameMatchesDefault pins the no-rename path:
// the flag adds no second output segment when FindRename reports no boundary.
func TestRun_BlameFollowRenameNoRenameMatchesDefault(t *testing.T) {
	chdirTempRepo(t)

	withoutFlag, _, code := runApp(t, "blame", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	withFlag, _, code := runApp(t, "blame", "a.go:A", "--follow-rename")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	qt.Assert(t, qt.Equals(withFlag, withoutFlag))
}

// TestRun_BlameFollowRenameResolvesAgainstHEADNotWorktree pins the extent
// source independently from the blame output source: a dirty leading edit
// must not shift the range passed to git blame for the HEAD blob.
func TestRun_BlameFollowRenameResolvesAgainstHEADNotWorktree(t *testing.T) {
	dir := chdirTempRepo(t)

	clean, stderr, code := runApp(t, "blame", "a.go:A", "--follow-rename")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))

	src, err := os.ReadFile(filepath.Join(dir, "a.go"))
	qt.Assert(t, qt.IsNil(err))
	writeAppFile(t, dir, "a.go", "// dirty leading edit\n\n"+string(src))

	dirty, stderr, code := runApp(t, "blame", "a.go:A", "--follow-rename")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(dirty, clean))
}

func TestRun_BlameHonorsCoreIgnoreCaseForExtensions(t *testing.T) {
	root := t.TempDir()
	gittest.Git(t, root, "init", "--quiet")
	gittest.Git(t, root, "config", "core.ignorecase", "true")
	writeAppFile(t, root, "Foo.GO", "package demo\n\nfunc First() {}\n")
	gittest.Git(t, root, "add", "Foo.GO")
	gittest.Git(t, root, "-c", "user.name=rgit test", "-c", "user.email=rgit@example.invalid", "commit", "--quiet", "-m", "initial")
	t.Chdir(root)

	stdout, stderr, code := runApp(t, "blame", "Foo.GO:First")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.StringContains(stdout, "First"))

	gittest.Git(t, root, "config", "core.ignorecase", "false")
	_, _, code = runApp(t, "blame", "Foo.GO:First")
	qt.Assert(t, qt.Equals(code, exitcode.UnsupportedLanguage))
}
