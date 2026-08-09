// No t.Parallel here: every case changes directory (app_test.go's own
// package comment explains why), which t.Chdir forbids combining with it.
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
	assertAnchorUsageRefusals(t, "log") // shared with blame_test.go (m31)

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

// TestRun_LogOrdinalAnchorWarns mirrors blame's own case: log resolves
// against HEAD, so the dup file must be committed before the ordinal anchor
// warns.
func TestRun_LogOrdinalAnchorWarns(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "dup.go", "package main\n\nfunc init() { println(1) }\n\nfunc init() { println(2) }\n")
	gitOut(t, dir, "add", "dup.go")
	gitOut(t, dir, "commit", "-m", "chore: two inits")

	_, stderr, code := runApp(t, "log", "dup.go:init#2")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stderr, "dup.go:init#2"))
	qt.Assert(t, qt.StringContains(stderr, "positional"))

	_, stderr, code = runApp(t, "log", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "positional")))
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
// non-negotiable guardrail specs/design.md § Commands states in full:
// patches are opt-in, never default, and the stream is bounded by commit
// count, not code size.
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

// TestRun_LogPathScopedSinceAndUntil pins the date-bounded second shape
// against real commit timestamps -- --since alone, --until alone, and both
// together -- so the filtering is proven to be git's own, not a string
// match this command invents.
func TestRun_LogPathScopedSinceAndUntil(t *testing.T) {
	dir := chdirTempRepo(t) // "chore: initial", undated (today)

	t.Setenv("GIT_AUTHOR_DATE", "2020-01-01T00:00:00")
	t.Setenv("GIT_COMMITTER_DATE", "2020-01-01T00:00:00")
	writeAppFile(t, dir, "old.txt", "old\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "chore: old commit")

	t.Setenv("GIT_AUTHOR_DATE", "2030-01-01T00:00:00")
	t.Setenv("GIT_COMMITTER_DATE", "2030-01-01T00:00:00")
	writeAppFile(t, dir, "new.txt", "new\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "chore: new commit")

	t.Run("--since alone excludes the earlier commit", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "log", "--since=2025-01-01")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "new commit"))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "old commit")))
	})

	t.Run("--until alone excludes the later commit", func(t *testing.T) {
		stdout, _, code := runApp(t, "log", "--until=2025-01-01")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "old commit"))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "new commit")))
	})

	t.Run("--since and --until together bound both ends", func(t *testing.T) {
		stdout, _, code := runApp(t, "log", "--since=2019-01-01", "--until=2021-01-01")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "old commit"))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "new commit")))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "chore: initial")))
	})
}

// TestRun_LogPathScopedPaths pins the other half of the second shape: zero
// or more trailing positionals are pathspecs, not an anchor, narrowing
// history the same way plain `git log -- path` does.
func TestRun_LogPathScopedPaths(t *testing.T) {
	dir := chdirTempRepo(t) // "chore: initial" touches only a.go

	writeAppFile(t, dir, "x.txt", "x\n")
	writeAppFile(t, dir, "y.txt", "y\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "chore: add x and y")

	writeAppFile(t, dir, "x.txt", "x2\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "fix(x): bump x")

	writeAppFile(t, dir, "y.txt", "y2\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "fix(y): bump y")

	t.Run("one path narrows to only its own touching commits", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "log", "--since=2000-01-01", "x.txt")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "bump x"))
		qt.Assert(t, qt.StringContains(stdout, "add x and y"))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "bump y")))
	})

	t.Run("multiple paths union their own touching commits", func(t *testing.T) {
		stdout, _, code := runApp(t, "log", "--since=2000-01-01", "x.txt", "y.txt")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "bump x"))
		qt.Assert(t, qt.StringContains(stdout, "bump y"))
	})

	t.Run("zero paths with --since is the whole repository's history", func(t *testing.T) {
		stdout, _, code := runApp(t, "log", "--since=2000-01-01")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "bump x"))
		qt.Assert(t, qt.StringContains(stdout, "bump y"))
		qt.Assert(t, qt.StringContains(stdout, "chore: initial"))
	})

	t.Run("--since narrows the anchorless form the same as any other filter", func(t *testing.T) {
		stdout, _, code := runApp(t, "log", "--since=2000-01-01", "--until=2000-01-02")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stdout, ""))
	})
}

// TestRun_LogPathScopedMaxCount pins git's own count limit on the
// path-scoped form, including both spellings and the unbounded default.
func TestRun_LogPathScopedMaxCount(t *testing.T) {
	dir := chdirTempRepo(t)
	for i, subject := range []string{"fix: second", "fix: third", "fix: fourth"} {
		writeAppFile(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn "+string(rune('2'+i))+"\n}\n")
		gitOut(t, dir, "add", "a.go")
		gitOut(t, dir, "commit", "-m", subject)
	}

	t.Run("-n limits output", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "log", "--since=2000-01-01", "-n", "2", "a.go")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "fix: fourth"))
		qt.Assert(t, qt.StringContains(stdout, "fix: third"))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "fix: second")))
		qt.Assert(t, qt.Equals(len(strings.Split(strings.TrimRight(stdout, "\n"), "\n")), 2))
	})

	t.Run("--max-count limits output", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "log", "--since=2000-01-01", "--max-count=1", "a.go")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "fix: fourth"))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "fix: third")))
		qt.Assert(t, qt.Equals(len(strings.Split(strings.TrimRight(stdout, "\n"), "\n")), 1))
	})

	t.Run("without max-count output remains unbounded", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "log", "--since=2000-01-01", "a.go")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "fix: fourth"))
		qt.Assert(t, qt.StringContains(stdout, "fix: third"))
		qt.Assert(t, qt.StringContains(stdout, "fix: second"))
		qt.Assert(t, qt.StringContains(stdout, "chore: initial"))
		qt.Assert(t, qt.Equals(len(strings.Split(strings.TrimRight(stdout, "\n"), "\n")), 4))
	})
}

// TestRun_LogPathScopedOutputModes covers --porcelain and -p/--patch on the
// path-scoped shape, and that the two remain mutually exclusive there the
// same as on the anchor shape (TestRun_LogHelpAndUsage covers that one).
func TestRun_LogPathScopedOutputModes(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "fix(a): bump A")

	t.Run("--porcelain emits tab-separated records", func(t *testing.T) {
		stdout, _, code := runApp(t, "log", "--since=2000-01-01", "--porcelain")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		for line := range strings.SplitSeq(strings.TrimRight(stdout, "\n"), "\n") {
			fields := strings.Split(line, "\t")
			qt.Assert(t, qt.Equals(len(fields), 2))
			qt.Assert(t, qt.IsTrue(len(fields[0]) >= 7))
		}
	})

	t.Run("-p/--patch includes the real patch body", func(t *testing.T) {
		stdout, _, code := runApp(t, "log", "--since=2000-01-01", "-p", "a.go")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "diff --git"))
		qt.Assert(t, qt.StringContains(stdout, "return 111"))
	})

	t.Run("--porcelain and --patch are mutually exclusive", func(t *testing.T) {
		_, stderr, code := runApp(t, "log", "--since=2000-01-01", "--porcelain", "--patch")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "mutually exclusive"))
	})
}

// TestRun_LogPathScopedPathEscapeIsRefused pins that the path-scoped shape
// goes through the same repoPath safety check as every other pathspec-
// accepting command, rather than forwarding a caller-supplied path to git
// unchecked because this shape parses with pflag instead of
// parseAnchorCommandArgs.
func TestRun_LogPathScopedPathEscapeIsRefused(t *testing.T) {
	dir := chdirTempRepo(t)
	outside := filepath.Join(filepath.Dir(dir), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runApp(t, "log", "--since=2000-01-01", "../outside.go")
	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.StringContains(stderr, "escapes the repository root"))
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

// TestRun_LogFollowRenameCrossesARenameThatReordersTheSymbol pins
// --follow-rename's whole reason to exist: a rename landing in the same
// commit as a reorder of the symbol within the file breaks git log -L's own
// line-range tracking (it follows text, not the decl), so the unflagged
// form stops at the rename commit while --follow-rename, re-resolving with
// tree-sitter at the boundary, reaches the pre-rename commit under the old
// name.
func TestRun_LogFollowRenameCrossesARenameThatReordersTheSymbol(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "old.go", "package p\n\nfunc Foo() int {\n\treturn 1\n}\n\nfunc Bar() int {\n\treturn 100\n}\n")
	gitOut(t, dir, "add", "old.go")
	gitOut(t, dir, "commit", "-m", "feat: add old.go")

	gitOut(t, dir, "mv", "old.go", "new.go")
	writeAppFile(t, dir, "new.go", "package p\n\nfunc Bar() int {\n\treturn 100\n}\n\nfunc Foo() int {\n\treturn 2\n}\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "refactor: rename and reorder")

	writeAppFile(t, dir, "new.go", "package p\n\nfunc Bar() int {\n\treturn 100\n}\n\nfunc Foo() int {\n\treturn 3\n}\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "fix: bump Foo")

	t.Run("without the flag, history stops at the rename", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "log", "new.go:Foo")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "fix: bump Foo"))
		qt.Assert(t, qt.StringContains(stdout, "refactor: rename and reorder"))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "feat: add old.go")))
	})

	t.Run("--follow-rename reaches the pre-rename commit", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "log", "new.go:Foo", "--follow-rename")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "fix: bump Foo"))
		qt.Assert(t, qt.StringContains(stdout, "refactor: rename and reorder"))
		qt.Assert(t, qt.StringContains(stdout, "feat: add old.go"))

		lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
		qt.Assert(t, qt.Equals(len(lines), 3))
	})

	t.Run("--follow-rename --porcelain keeps the two-field record shape across segments", func(t *testing.T) {
		stdout, _, code := runApp(t, "log", "new.go:Foo", "--follow-rename", "--porcelain")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
		qt.Assert(t, qt.Equals(len(lines), 3))
		for _, line := range lines {
			qt.Assert(t, qt.Equals(len(strings.Split(line, "\t")), 2))
		}
	})
}

// TestRun_LogFollowRenameNoRenameMatchesDefault pins that --follow-rename
// changes nothing for a symbol whose file was never renamed -- the loop
// runs exactly once, gitx.FindRename reports found=false, and output is
// byte-identical to the unflagged form.
func TestRun_LogFollowRenameNoRenameMatchesDefault(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "fix(a): bump A")

	withoutFlag, _, code := runApp(t, "log", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	withFlag, _, code := runApp(t, "log", "a.go:A", "--follow-rename")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	qt.Assert(t, qt.Equals(withFlag, withoutFlag))
}
