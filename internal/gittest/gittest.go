// Package gittest builds real temporary git repositories for tests.
//
// It exists so internal/app, internal/cli, internal/diff, and
// internal/synth can share one implementation of init, force a known
// branch name, set an identity, run a command and fail the test on its
// output, instead of each carrying its own copy.
//
// It is deliberately a plain package rather than a _test.go helper: Go
// scopes test files to their own package, so those four packages cannot
// share one otherwise. Nothing in the production build imports it.
//
// What it does NOT do is stand in for git. Every function here shells out
// to the real binary, because these tests exist to prove rgit agrees with
// git, and a stand-in would only prove rgit agrees with our belief about
// git (CONTRIBUTING.md § Tests).
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

// New initialises an empty repository in a temp directory and returns it
// alongside a gitx.Repo rooted there.
//
// The branch is forced to "main" and the identity is set locally rather
// than inherited: a developer's own init.defaultBranch or global
// user.email would otherwise decide what these tests assert against, and
// a repository with no identity cannot commit at all.
func New(t *testing.T) (dir string, repo *gitx.Repo) {
	t.Helper()
	dir = t.TempDir()

	Git(t, dir, "init", "-q", "-b", "main")
	Git(t, dir, "config", "user.email", "rgit-test@example.com")
	Git(t, dir, "config", "user.name", "rgit Test")

	return dir, gitx.New(dir)
}

// Git runs one git command in dir and returns its combined output, failing
// the test if git does. The output is returned rather than discarded so a
// caller can assert on it without a second spelling of this function.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// Write creates a file under dir, making any parent directories it needs.
func Write(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Commit stages everything and commits it, for a fixture whose starting
// state matters but whose history does not.
func Commit(t *testing.T, dir, message string) {
	t.Helper()
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-q", "-m", message)
}

// InstallHook writes an executable git hook into dir's real .git/hooks --
// e.g. a pre-commit hook that exits non-zero, to exercise AGENTS.md's "a
// rejected commit leaves staging in place" rule against a real hook rather
// than something rgit only believes git does with one.
func InstallHook(t *testing.T, dir, name, script string) {
	t.Helper()
	path := filepath.Join(dir, ".git", "hooks", name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
