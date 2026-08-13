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
//
// No t.Parallel here either, for the same reason as app_test.go: every case
// changes directory, which t.Chdir forbids combining with it.
package app

import (
	"os"
	"os/exec"
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

func TestRun_MergeWithNoMessageUsesMergeMessage(t *testing.T) {
	dir := chdirTempRepo(t)
	gitOut(t, dir, "checkout", "-q", "-b", "feature")
	writeAppFile(t, dir, "feature.txt", "feature\n")
	gitOut(t, dir, "add", "--", "feature.txt")
	gitOut(t, dir, "commit", "-q", "-m", "feat: add feature")
	gitOut(t, dir, "checkout", "-q", "main")
	gitOut(t, dir, "merge", "--no-ff", "--no-commit", "feature")

	_, stderr, code := runApp(t, "commit", "--no-verify", "feature.txt")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "requires a message")))
	parents := strings.Fields(gitOut(t, dir, "rev-list", "--parents", "-n", "1", "HEAD"))
	qt.Assert(t, qt.HasLen(parents, 3))
	qt.Assert(t, qt.StringContains(gitOut(t, dir, "log", "-1", "--format=%s"), "Merge branch 'feature'"))
}

func TestRun_CherryPickWithNoMessageUsesCherryPickMessage(t *testing.T) {
	dir := chdirTempRepo(t)
	gitOut(t, dir, "checkout", "-q", "-b", "cherry-pick-source")
	writeAppFile(t, dir, "a.go", "package a\n\nfunc A() int { return 2 }\n")
	gitOut(t, dir, "add", "--", "a.go")
	gitOut(t, dir, "commit", "-q", "-m", "feat: cherry-pick source")
	source := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	gitOut(t, dir, "checkout", "-q", "main")
	writeAppFile(t, dir, "a.go", "package a\n\nfunc A() int { return 3 }\n")
	gitOut(t, dir, "commit", "-qam", "feat: main divergence")
	expectGitFailure(t, dir, "cherry-pick", source)
	writeAppFile(t, dir, "a.go", "package a\n\nfunc A() int { return 2 }\n")

	_, stderr, code := runApp(t, "commit", "--no-verify", "a.go")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "requires a message")))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--format=%s"), "feat: cherry-pick source\n"))
}

func TestRun_CommitWithoutMessageOutsideSequencerIsUsageError(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\nfunc A() int { return 2 }\n")

	_, stderr, code := runApp(t, "commit", "--no-verify", "a.go")

	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.StringContains(stderr, "commit requires a message"))
}

func TestRun_RevertWithNoMessageUsesRevertMessage(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\nfunc A() int { return 2 }\n")
	gitOut(t, dir, "commit", "-qam", "feat: revert target")
	writeAppFile(t, dir, "a.go", "package a\n\nfunc A() int { return 3 }\n")
	gitOut(t, dir, "commit", "-qam", "feat: later change")
	expectGitFailure(t, dir, "revert", "HEAD~1")
	writeAppFile(t, dir, "a.go", "package a\n\nfunc A() int { return 1 }\n")

	_, stderr, code := runApp(t, "commit", "--no-verify", "a.go")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "requires a message")))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--format=%s"), "Revert \"feat: revert target\"\n"))
}

func expectGitFailure(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if err := cmd.Run(); err == nil {
		t.Fatalf("git %v succeeded; want failure", args)
	}
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

// TestRun_FixupWithMessageAppendsRatherThanConflicts is m24: --fixup plus
// -m is not the "-m and -F are mutually exclusive" shape of conflict --
// git appends -m's text as an extra body paragraph below the generated
// "fixup! <original subject>" subject, and that behaviour was only proven
// through the built binary (cmd/rgit/rgit_e2e_test.go's identically named
// case).
func TestRun_FixupWithMessageAppendsRatherThanConflicts(t *testing.T) {
	dir := chdirTempRepo(t)
	target := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	_, _, code := runApp(t, "commit", "--fixup="+target, "-m", "UNIQUE_BODY_MARKER", "a.go:A")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	body := gitOut(t, dir, "log", "-1", "--format=%B")
	qt.Assert(t, qt.StringContains(body, "fixup! chore: initial"))
	qt.Assert(t, qt.StringContains(body, "UNIQUE_BODY_MARKER"))
}

// TestRun_NoGPGSignOverridesConfiguredGPGSign is m25's --no-gpg-sign half:
// commit.gpgsign=true only proved it overrode a configured signing default
// through the built binary
// (cmd/rgit/rgit_e2e_test.go's TestCommit_GPGSignFlagsForwarded); -S itself
// already has a unit case (app_test.go's TestRun_GPGSignShorthandReachesGit).
// gpg.program pointed at a binary that always fails would turn an
// un-overridden commit.gpgsign=true into a deterministic failure, so a
// successful commit here is proof --no-gpg-sign actually reached git ahead
// of the config rather than the config never having fired at all.
func TestRun_NoGPGSignOverridesConfiguredGPGSign(t *testing.T) {
	dir := chdirTempRepo(t)
	gitOut(t, dir, "config", "gpg.program", "/bin/false")
	gitOut(t, dir, "config", "commit.gpgsign", "true")
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	_, _, code := runApp(t, "commit", "--no-gpg-sign", "-m", "fix(a): bump", "a.go:A")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
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

// TestRun_DiffUnbornBranchListsEverythingCommittable is m28: a fresh repo
// with no HEAD cannot run `git diff HEAD` for the default scope, so it
// compares against the empty tree instead (committableBase,
// internal/diff/scope.go) -- both the staged and untracked halves of that
// listing were only proven through the built binary
// (cmd/rgit/rgit_e2e_test.go's identically named case); the unit lane
// covered only the empty-tree base itself (internal/diff/scope_test.go),
// not a real listing through it.
func TestRun_DiffUnbornBranchListsEverythingCommittable(t *testing.T) {
	dir, _ := gittest.New(t)
	t.Chdir(dir)

	writeAppFile(t, dir, "staged.go", "package auth\n\nfunc Staged() int { return 3 }\n")
	gitOut(t, dir, "add", "--", "staged.go")
	writeAppFile(t, dir, "untracked.go", "package auth\n\nfunc Untracked() int { return 4 }\n")

	stdout, _, code := runApp(t, "diff", "--porcelain")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	// staged.go and untracked.go are both brand-new files, so both attribute
	// per symbol (each one's @header preamble plus its own function) rather
	// than one aggregate row -- the point here is that both appear at all
	// against the empty-tree base, not their own attribution shape.
	qt.Assert(t, qt.StringContains(stdout, "staged.go\tStaged\tMOD\t"))
	qt.Assert(t, qt.StringContains(stdout, "untracked.go\tUntracked\tMOD\t"))
}

// TestRun_PushAfterSuccessfulCommit is m29: a successful --push (as
// opposed to the failure path TestRun_PushFailureReportsUpstreamHint,
// app_test.go, already covers) was only proven through the built binary
// (cmd/rgit/rgit_e2e_test.go's identically named case) -- a real bare
// remote, upstream already configured, and repo.Push actually reaching it
// successfully.
func TestRun_PushAfterSuccessfulCommit(t *testing.T) {
	dir := chdirTempRepo(t)
	remote := t.TempDir()
	gittest.Git(t, remote, "init", "-q", "--bare")
	gitOut(t, dir, "remote", "add", "origin", remote)
	branch := strings.TrimSpace(gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD"))
	gitOut(t, dir, "push", "-q", "-u", "origin", branch)

	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	_, _, code := runApp(t, "commit", "--push", "-m", "fix(a): bump", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	local := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	qt.Assert(t, qt.Equals(strings.TrimSpace(gittest.Git(t, remote, "rev-parse", branch)), local))
}

// TestRun_DiffUntrackedFileAndModeChange holds `rgit diff`'s untracked and
// mode-only paths at the unit level: an untracked file attributes per
// symbol (internal/diff's buildUntrackedReport) exactly like a brand-new
// tracked file, and a mode-only change reads from either the worktree or
// the index (formatModeNote, and contentSide.mode on indexSide
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
	qt.Assert(t, qt.StringContains(stdout, "untracked.go\tU\tMOD\t"))
	qt.Assert(t, qt.StringContains(stdout, "a.go\t\tMODE\t"))

	// --staged: New is indexSide(), so staging the mode change routes its
	// own mode() through repo.LsFilesStage rather than a worktree os.Stat.
	gitOut(t, dir, "add", "a.go")
	staged, _, code := runApp(t, "diff", "--staged", "--porcelain")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(staged, "a.go\t\tMODE\t"))
}

// TestRun_CommitExtensionlessShebangResolvesShellSymbol is M11: the commit
// path's own use of the worktree-shebang fallback (resolveAnchorExtent and
// synth's own resolution share it) was only proven by building and execing
// the binary (cmd/rgit/rgit_e2e_test.go's identically named case) -- the
// unit lane covered the shebang fallback on the diff path
// (internal/diff/run_test.go) but never through a real commit, so a
// regression specific to the commit-path resolution would not fail
// `-short`. gittest.New is used directly, not chdirTempRepo, so the only
// file in the repository is the extensionless script itself.
func TestRun_CommitExtensionlessShebangResolvesShellSymbol(t *testing.T) {
	dir, _ := gittest.New(t)
	t.Chdir(dir)

	writeAppFile(t, dir, "pre-commit", "#!/usr/bin/env bash\n\nfoo() {\n  echo v1\n}\n\nbar() {\n  echo bar\n}\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", "init")

	writeAppFile(t, dir, "pre-commit", "#!/usr/bin/env bash\n\nfoo() {\n  echo v2\n}\n\nbar() {\n  echo changed too\n}\n")

	_, _, code := runApp(t, "commit", "-m", "fix: bump foo only", "pre-commit:foo")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	head := gitOut(t, dir, "show", "HEAD:pre-commit")
	qt.Assert(t, qt.StringContains(head, "echo v2"))
	qt.Assert(t, qt.StringContains(head, "echo bar")) // bar's edit stayed uncommitted
}

// TestRun_CommitExtensionlessNodeShebangResolvesTypeScriptSymbol pins the
// Node/TypeScript ecosystem's own shebang routing (resolve.shebangExtension)
// through a real commit, the same way
// TestRun_CommitExtensionlessShebangResolvesShellSymbol above does for
// shell: an extensionless script naming "npx tsx" via env resolves symbols
// through the TypeScript adapter, and one named function stages while its
// sibling's edit stays uncommitted.
func TestRun_CommitExtensionlessNodeShebangResolvesTypeScriptSymbol(t *testing.T) {
	dir, _ := gittest.New(t)
	t.Chdir(dir)

	writeAppFile(t, dir, "run", "#!/usr/bin/env npx tsx\n\nfunction foo(): void {\n  console.log('v1')\n}\n\nfunction bar(): void {\n  console.log('bar')\n}\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", "init")

	writeAppFile(t, dir, "run", "#!/usr/bin/env npx tsx\n\nfunction foo(): void {\n  console.log('v2')\n}\n\nfunction bar(): void {\n  console.log('changed too')\n}\n")

	_, _, code := runApp(t, "commit", "-m", "fix: bump foo only", "run:foo")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	head := gitOut(t, dir, "show", "HEAD:run")
	qt.Assert(t, qt.StringContains(head, "v2"))
	qt.Assert(t, qt.StringContains(head, "bar")) // bar's edit stayed uncommitted
}

// TestRun_CommitGoTSPythonSymbolGranularityInOneInvocation is M12: the
// cross-grammar single-invocation guarantee (one `commit` naming a symbol
// in each of three languages, each file's other symbol staying
// uncommitted) was only proven through the built binary
// (cmd/rgit/rgit_e2e_test.go's identically named case). Per-grammar
// staging is already covered elsewhere at the unit level; what only the
// e2e case proved was that naming all three together in one invocation
// does not let one grammar's plan step over another's.
func TestRun_CommitGoTSPythonSymbolGranularityInOneInvocation(t *testing.T) {
	dir, _ := gittest.New(t)
	t.Chdir(dir)

	writeAppFile(t, dir, "auth.go", "package auth\n\nfunc GoA() int { return 1 }\n\nfunc GoB() int { return 1 }\n")
	writeAppFile(t, dir, "app.ts", "export function TsA(): number { return 1 }\n\nexport function TsB(): number { return 1 }\n")
	writeAppFile(t, dir, "svc.py", "def py_a():\n    return 1\n\n\ndef py_b():\n    return 1\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", "init")

	writeAppFile(t, dir, "auth.go", "package auth\n\nfunc GoA() int { return 2 }\n\nfunc GoB() int { return 2 }\n")
	writeAppFile(t, dir, "app.ts", "export function TsA(): number { return 2 }\n\nexport function TsB(): number { return 2 }\n")
	writeAppFile(t, dir, "svc.py", "def py_a():\n    return 2\n\n\ndef py_b():\n    return 2\n")

	_, _, code := runApp(t, "commit", "-m", "fix: bump the first of each",
		"auth.go:GoA", "app.ts:TsA", "svc.py:py_a")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	for _, c := range []struct{ path, committed, withheld string }{
		{"auth.go", "func GoA() int { return 2 }", "func GoB() int { return 1 }"},
		{"app.ts", "export function TsA(): number { return 2 }", "export function TsB(): number { return 1 }"},
		{"svc.py", "def py_a():\n    return 2", "def py_b():\n    return 1"},
	} {
		head := gitOut(t, dir, "show", "HEAD:"+c.path)
		qt.Assert(t, qt.StringContains(head, c.committed))
		qt.Assert(t, qt.StringContains(head, c.withheld))
	}
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

func TestRun_AmendWithNoTargetsReusesHead(t *testing.T) {
	dir := chdirTempRepo(t)
	beforeTree := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD^{tree}"))
	beforeSubject := gitOut(t, dir, "log", "-1", "--format=%s")

	_, stderr, code := runApp(t, "commit", "--amend")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD^{tree}")), beforeTree))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--format=%s"), beforeSubject))
}

func TestRun_AllowEmptyWithNoTargets(t *testing.T) {
	dir := chdirTempRepo(t)
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	_, stderr, code := runApp(t, "commit", "--allow-empty", "-m", "chore: ping")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	after := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	qt.Assert(t, qt.Not(qt.Equals(after, before)))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--format=%s"), "chore: ping\n"))
}

func TestRun_FixupAndSquashWithNoTargetsUseIndex(t *testing.T) {
	dir := chdirTempRepo(t)
	target := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	writeAppFile(t, dir, "a.go", "package a\n\nfunc A() int { return 2 }\n")
	gitOut(t, dir, "add", "--", "a.go")
	_, _, code := runApp(t, "commit", "--fixup="+target)
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--format=%s"), "fixup! chore: initial\n"))

	writeAppFile(t, dir, "a.go", "package a\n\nfunc A() int { return 3 }\n")
	gitOut(t, dir, "add", "--", "a.go")
	_, _, code = runApp(t, "commit", "--squash="+target)
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--format=%s"), "squash! chore: initial\n"))
}

func TestRun_ReuseMessageWithChdir(t *testing.T) {
	dir := chdirTempRepo(t)
	t.Chdir(t.TempDir())

	_, stderr, code := runApp(t, "-C", dir, "commit", "--reuse-message=HEAD", "--allow-empty")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "log", "-1", "--format=%s"), "chore: initial\n"))
}

func TestRun_ReeditMessageRefused(t *testing.T) {
	chdirTempRepo(t)

	_, stderr, code := runApp(t, "commit", "--reedit-message")

	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.StringContains(stderr, "--reedit-message"))
	qt.Assert(t, qt.StringContains(stderr, "--reuse-message"))
}
