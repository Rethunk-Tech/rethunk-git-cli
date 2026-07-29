// Unit-lane coverage for guarantees that otherwise need the real binary
// built and exec'd (cmd/rgit/rgit_e2e_test.go).
// app.Run already shells out to the real git binary for everything git
// itself does (openRepo, synth.Stage, and commit's own gitx.Repo.Commit
// call), so a real hook fires, a real index updates, and a real git
// identity resolves the same way here as it does through the built
// binary -- the only thing this lane genuinely cannot prove is an
// argv[0]-level exit code as an external process observes it, which is
// exactly what stays in rgit_e2e_test.go.
//
// This file is separate from app_test.go so a concurrent editor of that
// file never collides with it; it shares that file's runApp, chdirTempRepo,
// writeAppFile, and gitOut helpers, since Go compiles every _test.go file
// in a package together.
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

// TestRun_InterspersedFlagAfterPositional pins the pflag behaviour stdlib
// flag and ff/ffcli do not have: a flag arriving after a positional target
// must still parse, reading one message and one target rather than stopping
// at the first positional and misreading "-m"/"msg" as two more targets.
func TestRun_InterspersedFlagAfterPositional(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	stdout, stderr, code := runApp(t, "commit", "a.go:A", "-m", "fix(a): interspersed flag")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "requires a message")))
	qt.Assert(t, qt.StringContains(stdout, "a.go:A"))
}

// TestRun_PreStagedSiblingFileComesAlong pins AGENTS.md's inherited-from-git
// behaviour: work staged before invoking rgit comes along with the commit,
// exactly as a bare `git commit` would carry it.
func TestRun_PreStagedSiblingFileComesAlong(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	writeAppFile(t, dir, "sibling.txt", "never named to rgit\n")
	gitOut(t, dir, "add", "--", "sibling.txt")

	_, _, code := runApp(t, "commit", "-m", "feat(a): update A", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	show := gitOut(t, dir, "show", "--stat", "HEAD")
	qt.Assert(t, qt.StringContains(show, "sibling.txt"))
}

// TestRun_HookRejectionLeavesStagingIntact pins AGENTS.md's other inherited
// behaviour: a hook that rejects the commit must never roll staging back.
// This is deliberately covered in both lanes (rgit_e2e_test.go's own
// TestCommit_HookRejectionLeavesStagingIntact): a real hook firing is
// process-level enough that losing either lane would leave a gap the other
// cannot see -- the unit lane exercises app.Run's own gitx.Repo.Commit call,
// the e2e lane exercises the same guarantee through the built binary's own
// exit code as a caller observes it.
func TestRun_HookRejectionLeavesStagingIntact(t *testing.T) {
	dir := chdirTempRepo(t)
	// Both A and B change in the worktree; only A is named, so B's own edit
	// staying unstaged is what makes the index-vs-worktree difference below
	// mean something.
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 222\n}\n")
	gittest.InstallHook(t, dir, "pre-commit", "#!/bin/sh\nexit 1\n")

	_, _, code := runApp(t, "commit", "-m", "feat(a): update A", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.GitFailure))

	// Nothing rolled back: A's synthesized edit is still staged.
	indexed := gitOut(t, dir, "show", ":a.go")
	qt.Assert(t, qt.StringContains(indexed, "return 111"))
	qt.Assert(t, qt.Not(qt.StringContains(indexed, "return 222")))
	// Staged (index differs from HEAD) AND unstaged (B's own edit, worktree
	// differs from index) both hold: git's porcelain reports "MM".
	status := gitOut(t, dir, "status", "--porcelain")
	qt.Assert(t, qt.StringContains(status, "MM a.go"))
}

// TestRun_AmendWithNoMessageReusesHeadSubject pins docs/USAGE.md: rgit never
// opens an editor, so --amend with neither -m nor -F has exactly one
// sensible meaning, `git commit --amend --no-edit`.
func TestRun_AmendWithNoMessageReusesHeadSubject(t *testing.T) {
	dir := chdirTempRepo(t)
	before := gitOut(t, dir, "log", "-1", "--format=%s")

	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	_, _, code := runApp(t, "commit", "--amend", "a.go:A")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--format=%s"), before))
	qt.Assert(t, qt.StringContains(gitOut(t, dir, "cat-file", "-p", "HEAD:a.go"), "return 111"))
}

// TestRun_FixupAndSquashGenerateAutosquashMessages pins docs/USAGE.md:
// --fixup/--squash need neither -m nor -F, since git generates
// "fixup!"/"squash! <subject>" itself -- the message-required validation
// must not fire for either.
func TestRun_FixupAndSquashGenerateAutosquashMessages(t *testing.T) {
	dir := chdirTempRepo(t)
	target := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	for i, tc := range []struct{ flag, wantPrefix string }{
		{"--fixup", "fixup! "},
		{"--squash", "squash! "},
	} {
		writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn "+strings.Repeat("1", i+3)+"\n}\n\nfunc B() int {\n\treturn 2\n}\n")
		_, _, code := runApp(t, "commit", tc.flag+"="+target, "a.go:A")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--format=%s"), tc.wantPrefix+"chore: initial\n"))
	}
}

// TestRun_AuthorAndDateForwarded pins plain forwarding of both flags to git.
func TestRun_AuthorAndDateForwarded(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	_, _, code := runApp(t, "commit",
		"--author", "Ada Lovelace <ada@example.com>",
		"--date", "2005-04-07T22:13:13",
		"-m", "fix(a): bump", "a.go:A")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--format=%an <%ae>"), "Ada Lovelace <ada@example.com>\n"))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--date=format:%Y-%m-%d", "--format=%ad"), "2005-04-07\n"))
}

// TestRun_ResetAuthorForwarded pins --reset-author's own git semantics:
// paired with --amend, it takes the author identity from the committer
// instead of carrying the original forward.
func TestRun_ResetAuthorForwarded(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	_, _, code := runApp(t, "commit",
		"--author", "Ada Lovelace <ada@example.com>",
		"-m", "fix(a): bump", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--format=%an"), "Ada Lovelace\n"))

	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 222\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	_, _, code = runApp(t, "commit", "--amend", "--reset-author", "a.go:A")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	// gittest.New's own committer identity, not Ada's.
	qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--format=%an"),
		gitOut(t, dir, "log", "-1", "--format=%cn")))
}

// TestRun_DiffUntrackedFileAndModeChange holds `rgit diff`'s untracked and
// mode-only paths at the unit level: an untracked file (internal/diff's
// buildUntrackedReport) and a mode-only change read from either the
// worktree or the index (formatModeNote, and contentSide.mode on indexSide
// specifically, which only --staged/--unstaged ever select).
func TestRun_DiffUntrackedFileAndModeChange(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "untracked.go", "package a\n\nfunc U() int { return 1 }\n")

	if err := os.Chmod(filepath.Join(dir, "a.go"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Default scope: worktree mode against HEAD's, plus the untracked file.
	stdout, _, code := runApp(t, "diff", "--porcelain")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "untracked.go\t\tUNTRACKED\t"))
	qt.Assert(t, qt.StringContains(stdout, "a.go\t\tMODE\t"))

	// --staged: New is indexSide(), so staging the mode change routes its
	// own mode() through repo.LsFilesStage rather than a worktree os.Stat.
	gitOut(t, dir, "add", "a.go")
	staged, _, code := runApp(t, "diff", "--staged", "--porcelain")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(staged, "a.go\t\tMODE\t"))
}

// TestRun_DiffUnsupportedLanguageSymReachesExtLookup closes internal/app's
// own gap: diff.go's extForFailedSym (the file-extension lookup
// unsupportedLanguageHint needs, read off resolve.ResolveError's own Path
// field) was reachable only by building and execing the binary. main.rs is
// genuinely unsupported, gated or otherwise, so no hint is expected here --
// TestRun_UnsupportedLanguageGetsNoRebuildHint in app_test.go already pins
// that half on the commit path; this pins that runDiff's own error handling
// reaches the same lookup without one.
func TestRun_DiffUnsupportedLanguageSymReachesExtLookup(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "main.rs", "fn main() {}\n")

	_, stderr, code := runApp(t, "diff", "--sym", "main.rs:main")

	qt.Assert(t, qt.Equals(code, exitcode.UnsupportedLanguage))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "rgit_sql")))
}
