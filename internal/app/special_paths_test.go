package app

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

func TestRun_CommitRefusesSkipWorktreeSymbol(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "tracked.go", "package p\n\nfunc Keep() {}\n")
	gitOut(t, dir, "add", "--", "tracked.go")
	gitOut(t, dir, "commit", "-q", "-m", "chore: add tracked fixture")
	gitOut(t, dir, "update-index", "--skip-worktree", "tracked.go")

	_, stderr, code := runApp(t, "commit", "-m", "fix(p): keep", "tracked.go:Keep")

	assertSpecialPathRefusal(t, stderr, code, "skip-worktree")
}

func TestRun_CommitRefusesAssumeUnchangedSymbol(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "tracked.go", "package p\n\nfunc Keep() {}\n")
	gitOut(t, dir, "add", "--", "tracked.go")
	gitOut(t, dir, "commit", "-q", "-m", "chore: add tracked fixture")
	gitOut(t, dir, "update-index", "--assume-unchanged", "tracked.go")

	_, stderr, code := runApp(t, "commit", "-m", "fix(p): keep", "tracked.go:Keep")

	assertSpecialPathRefusal(t, stderr, code, "assume-unchanged")
}

func TestRun_CommitRefusesUnmergedSymbol(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "conflict.go", "package p\n\nfunc Keep() {}\n")
	gitOut(t, dir, "add", "--", "conflict.go")
	gitOut(t, dir, "commit", "-q", "-m", "chore: add conflict fixture")
	blob := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD:conflict.go"))
	gittest.Unmerged(t, dir, blob, "conflict.go")

	_, stderr, code := runApp(t, "commit", "-m", "fix(p): keep", "conflict.go:Keep")

	assertSpecialPathRefusal(t, stderr, code, "unmerged")
}

func TestRun_CommitPathspecOnSkipWorktreeDelegatesToGit(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "tracked.go", "package p\n\nfunc Keep() {}\n")
	gitOut(t, dir, "add", "--", "tracked.go")
	gitOut(t, dir, "commit", "-q", "-m", "chore: add tracked fixture")
	gitOut(t, dir, "update-index", "--skip-worktree", "tracked.go")
	writeAppFile(t, dir, "tracked.go", "package p\n\nfunc Keep() { println(\"updated\") }\n")

	_, stderr, code := runApp(t, "commit", "-m", "chore: path", "tracked.go")

	qt.Assert(t, qt.Equals(code, exitcode.GitFailure))
	qt.Assert(t, qt.StringContains(stderr, "git add -- tracked.go"))
}

func assertSpecialPathRefusal(t *testing.T, stderr string, code exitcode.Code, reason string) {
	t.Helper()
	qt.Assert(t, qt.Equals(code, exitcode.SpecialPathRefused))
	qt.Assert(t, qt.StringContains(stderr, "synth:"))
	qt.Assert(t, qt.StringContains(stderr, reason))
}
