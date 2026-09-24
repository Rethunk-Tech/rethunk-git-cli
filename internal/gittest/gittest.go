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
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
func New(ctx context.Context, t testing.TB) (dir string, repo *gitx.Repo) {
	t.Helper()
	dir = t.TempDir()

	Git(ctx, t, dir, "init", "-q", "-b", "main")
	Git(ctx, t, dir, "config", "user.email", "rgit-test@example.com")
	Git(ctx, t, dir, "config", "user.name", "rgit Test")

	return dir, gitx.New(dir)
}

// RepoWithFile is New plus one committed file, the starting state most
// tests want: an anchor can only resolve against a symbol that exists at
// HEAD, so a repository with no commit is useless to them.
//
// It returns the gitx.Repo as well as the directory because the two are
// wanted together often enough that a dir-only variant just pushes every
// such caller back to spelling the three calls out again.
func RepoWithFile(ctx context.Context, t testing.TB, rel, content, message string) (dir string, repo *gitx.Repo) {
	t.Helper()
	dir, repo = New(ctx, t)
	Write(t, dir, rel, content)
	Commit(ctx, t, dir, message)
	return dir, repo
}

// Git runs one git command in dir and returns its combined output, failing
// the test if git does. The output is returned rather than discarded so a
// caller can assert on it without a second spelling of this function.
func Git(ctx context.Context, t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixture arguments go directly to the real git binary, never through a shell
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	if len(args) > 0 && args[0] == "init" {
		disableAutoMaintenance(t, ctx, dir)
	}
	return string(out)
}

// disableAutoMaintenance turns off the one thing that touches .git after the
// command that triggered it has already exited: git forks auto-maintenance
// detached, so no caller waits on it, and it can still be writing under
// .git/objects when t.TempDir()'s RemoveAll runs -- surfacing as a
// "directory not empty" cleanup failure in whichever test happened to trip
// it. Applied to every repository this package creates, bare ones included,
// since all of them go through Git's own "init". None is remotely large
// enough to need either pass.
func disableAutoMaintenance(t testing.TB, ctx context.Context, dir string) {
	t.Helper()
	for _, kv := range [][2]string{{"gc.auto", "0"}, {"maintenance.auto", "false"}} {
		cmd := exec.CommandContext(ctx, "git", "config", kv[0], kv[1]) //nolint:gosec // fixed test config values go directly to git, never through a shell
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git config %s %s: %v: %s", kv[0], kv[1], err, out)
		}
	}
}

// GitInput runs one git command with input on stdin and returns its output,
// failing the test if git does. It is useful for plumbing commands whose
// input format is more precise than a sequence of command-line arguments.
func GitInput(ctx context.Context, t testing.TB, dir, input string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixture arguments go directly to the real git binary, never through a shell
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// BloblessClone returns a repository whose tracked.go entry names an
// unreachable blob while its configuration marks origin as a promisor remote.
// Git therefore reports the tracked path as an unavailable object rather than
// as an absent path.
func BloblessClone(ctx context.Context, t testing.TB) (dir string, repo *gitx.Repo) {
	t.Helper()
	dir, _ = New(ctx, t)
	const missingBlob = "1111111111111111111111111111111111111111"
	treeInput := "100644 blob " + missingBlob + "\ttracked.go\n"
	tree := strings.TrimSpace(GitInput(ctx, t, dir, treeInput, "mktree", "--missing"))
	commit := Git(ctx, t, dir, "commit-tree", tree, "-m", "chore: add tracked file")
	Git(ctx, t, dir, "update-ref", "refs/heads/main", strings.TrimSpace(commit))
	Git(ctx, t, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	Git(ctx, t, dir, "remote", "add", "origin", filepath.Join(t.TempDir(), "unreachable"))
	Git(ctx, t, dir, "config", "remote.origin.promisor", "true")
	Git(ctx, t, dir, "config", "extensions.partialClone", "origin")
	return dir, gitx.New(dir)
}

// Write creates a file under dir, making any parent directories it needs.
func Write(t testing.TB, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Commit stages everything and commits it, for a fixture whose starting
// state matters but whose history does not.
func Commit(ctx context.Context, t testing.TB, dir, message string) {
	t.Helper()
	Git(ctx, t, dir, "add", "-A")
	Git(ctx, t, dir, "commit", "-q", "-m", message)
}

// Unmerged rewrites path's index entry as a conflict, staging the same blob
// at all three merge stages. A real merge is not needed to produce one, and
// the index is the only thing rgit reads to tell a conflicted path apart.
func Unmerged(ctx context.Context, t testing.TB, dir, blob, path string) {
	t.Helper()
	GitInput(ctx, t, dir, fmt.Sprintf(
		"100644 %s 1\t%s\n100644 %s 2\t%s\n100644 %s 3\t%s\n",
		blob, path, blob, path, blob, path,
	), "update-index", "--index-info")
}

// InstallHook writes an executable git hook into dir's real .git/hooks --
// e.g. a pre-commit hook that exits non-zero, to exercise docs/USAGE.md's
// "a rejected commit rolls staging back to its pre-commit state, index-only
// with no worktree writes" rule against a real hook rather than something
// rgit only believes git does with one.
func InstallHook(ctx context.Context, t testing.TB, dir, name, script string) {
	t.Helper()
	path := filepath.Join(dir, ".git", "hooks", name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // this fixture's git hooks must retain owner execute permission
		t.Fatal(err)
	}
}
