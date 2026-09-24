// Package gitx_test is an external test package (rather than the whitebox
// "package gitx" the rest of this package's tests could use) so it can
// build its fixtures through internal/gittest: gittest itself imports gitx,
// and a whitebox test file importing gittest back would be a cycle.
package gitx_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

func TestLsFilesStageAndMergeBase(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	gittest.Write(t, dir, "file.txt", "content")

	// LsFilesStage for untracked file
	_, found, err := repo.LsFilesStage(ctx, "file.txt")
	if err != nil {
		t.Fatalf("LsFilesStage error: %v", err)
	}
	if found {
		t.Errorf("expected untracked file not to be found in stage")
	}

	// Add file and check LsFilesStage
	gittest.Git(t, dir, "add", "file.txt")
	mode, found, err := repo.LsFilesStage(ctx, "file.txt")
	if err != nil {
		t.Fatalf("LsFilesStage error: %v", err)
	}
	if !found || mode == "" {
		t.Errorf("expected tracked file to be found in stage, got mode=%q found=%v", mode, found)
	}

	gittest.Write(t, dir, "conflict.txt", "conflict\n")
	blob := hashObject(t, dir, "conflict\n")
	gittest.Unmerged(t, dir, blob, "conflict.txt")

	if unmerged, err := repo.IsUnmerged(ctx, "conflict.txt"); err != nil {
		t.Fatalf("IsUnmerged error: %v", err)
	} else if !unmerged {
		t.Error("IsUnmerged = false; want true")
	}
	if mode, found, err := repo.LsFilesStage(ctx, "conflict.txt"); err != nil {
		t.Fatalf("LsFilesStage(unmerged) error: %v", err)
	} else if found || mode != "" {
		t.Errorf("LsFilesStage(unmerged) = (%q, %v); want (empty, false)", mode, found)
	}

	// Commit initial commit for MergeBase testing
	gittest.Commit(t, dir, "initial")
	branch1SHA, _, err := repo.RevParseVerify(ctx, "HEAD")
	if err != nil {
		t.Fatalf("RevParseVerify error: %v", err)
	}

	// Create branch feature
	gittest.Git(t, dir, "checkout", "-b", "feature")
	gittest.Write(t, dir, "file.txt", "feature content")
	gittest.Git(t, dir, "commit", "-am", "feature commit")

	mbSHA, ok, err := repo.MergeBase(ctx, "main", "feature")
	if err != nil || !ok {
		t.Fatalf("MergeBase error: %v ok=%v", err, ok)
	}
	if mbSHA != branch1SHA {
		t.Errorf("MergeBase SHA = %q; want %q", mbSHA, branch1SHA)
	}
}

func TestSequencerOpAndIgnoreCase(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	gittest.Write(t, dir, "file.txt", "content\n")
	gittest.Commit(t, dir, "initial")
	sha, ok, err := repo.RevParseVerify(ctx, "HEAD")
	if err != nil || !ok {
		t.Fatalf("RevParseVerify(HEAD) = (%q, %v, %v)", sha, ok, err)
	}

	if op, found, err := repo.SequencerOp(ctx); err != nil || found || op != "" {
		t.Fatalf("SequencerOp(clean) = (%q, %v, %v); want empty", op, found, err)
	}
	if err := os.WriteFile(dir+"/.git/MERGE_HEAD", []byte(sha+"\n"), 0o644); err != nil {
		t.Fatalf("write MERGE_HEAD: %v", err)
	}
	if op, found, err := repo.SequencerOp(ctx); err != nil || !found || op != "merge" {
		t.Fatalf("SequencerOp(merge) = (%q, %v, %v); want merge", op, found, err)
	}
	if err := os.Remove(dir + "/.git/MERGE_HEAD"); err != nil {
		t.Fatalf("remove MERGE_HEAD: %v", err)
	}

	gittest.Git(t, dir, "config", "core.ignorecase", "true")
	if ignoreCase, err := repo.IgnoreCase(ctx); err != nil || !ignoreCase {
		t.Fatalf("IgnoreCase() = (%v, %v); want true", ignoreCase, err)
	}
	gittest.Git(t, dir, "config", "core.ignorecase", "false")
	if ignoreCase, err := repo.IgnoreCase(ctx); err != nil || !ignoreCase {
		t.Fatalf("IgnoreCase() after config change = (%v, %v); want cached true", ignoreCase, err)
	}
}

func hashObject(t *testing.T, dir, content string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "-C", dir, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git hash-object: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestAdd_AlreadyStagedDeletionSucceeds pins Add's own fallback (the unit
// lane's only path to hasStagedChange -- the e2e lane's
// TestCommit_PathAlreadyStagedAsDeleted is the only other case that reaches
// it, driving the whole binary to prove the same thing): after `git rm`, a
// path matches nothing in either the worktree or the index, so plain
// `git add` calls it a bad pathspec. Naming something already staged
// exactly as asked must not fail -- the commit includes it either way.
func TestAdd_AlreadyStagedDeletionSucceeds(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	gittest.Write(t, dir, "gone.md", "bye\n")
	gittest.Commit(t, dir, "chore: add gone.md")
	gittest.Git(t, dir, "rm", "-q", "gone.md")

	if err := repo.Add(ctx, "gone.md"); err != nil {
		t.Errorf("Add(already-staged deletion) = %v; want nil", err)
	}
}

// TestAdd_MixedAlreadyStagedDeletionStagesTheRest pins Add's per-pathspec
// tolerance: `git add` fails its whole invocation on the one pathspec that
// matches nothing, so tolerating it must not cost every other named path
// its staging. Naming a bogus path alongside real ones must still fail
// (and leave the index exactly as found), or a typo would silently drop
// every path beside it.
func TestAdd_MixedAlreadyStagedDeletionStagesTheRest(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	gittest.Write(t, dir, "gone.md", "bye\n")
	gittest.Write(t, dir, "keep.txt", "a\n")
	gittest.Commit(t, dir, "chore: fixtures")
	gittest.Git(t, dir, "rm", "-q", "gone.md")
	gittest.Write(t, dir, "keep.txt", "a-modified\n")

	if err := repo.Add(ctx, "gone.md", "keep.txt"); err != nil {
		t.Fatalf("Add(already-staged deletion, modified file) = %v; want nil", err)
	}
	staged := gittest.Git(t, dir, "diff", "--cached", "--name-only")
	if !strings.Contains(staged, "keep.txt") {
		t.Errorf("git diff --cached --name-only = %q; want keep.txt staged alongside the tolerated deletion", staged)
	}

	t.Run("genuine bad pathspec still errors and stages nothing", func(t *testing.T) {
		gittest.Write(t, dir, "other.txt", "c\n")
		gittest.Commit(t, dir, "chore: other.txt")
		gittest.Write(t, dir, "other.txt", "c-modified\n")

		before := gittest.Git(t, dir, "diff", "--cached", "--name-only")
		err := repo.Add(ctx, "other.txt", "does-not-exist.txt")
		if err == nil {
			t.Fatalf("Add(real path, bogus path) = nil; want error")
		}
		after := gittest.Git(t, dir, "diff", "--cached", "--name-only")
		if after != before {
			t.Errorf("index changed on a genuine bad-pathspec error: before %q, after %q", before, after)
		}
	})
}

// TestCatFileSample pins the bounded-read contract classifyPath relies on:
// a sample no larger than the caller's own limit, the same exists=false
// folding CatFile itself does for a missing path or revision, and -- the
// point of the whole method -- a blob larger than limit still returns
// only limit bytes rather than the full content.
func TestCatFileSample(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	gittest.Write(t, dir, "small.txt", "hi\n")
	gittest.Write(t, dir, "large.txt", strings.Repeat("a", 100))
	gittest.Commit(t, dir, "chore: fixtures")

	t.Run("blob smaller than limit returns its full content", func(t *testing.T) {
		t.Parallel()
		sample, exists, err := repo.CatFileSample(ctx, "HEAD", "small.txt", 16)
		if err != nil {
			t.Fatalf("CatFileSample: %v", err)
		}
		if !exists {
			t.Fatal("exists = false; want true")
		}
		if string(sample) != "hi\n" {
			t.Errorf("sample = %q; want %q", sample, "hi\n")
		}
	})

	t.Run("blob larger than limit is truncated to it", func(t *testing.T) {
		t.Parallel()
		sample, exists, err := repo.CatFileSample(ctx, "HEAD", "large.txt", 10)
		if err != nil {
			t.Fatalf("CatFileSample: %v", err)
		}
		if !exists {
			t.Fatal("exists = false; want true")
		}
		if len(sample) != 10 {
			t.Errorf("len(sample) = %d; want 10", len(sample))
		}
		if string(sample) != strings.Repeat("a", 10) {
			t.Errorf("sample = %q; want 10 'a's", sample)
		}
	})

	t.Run("path absent from rev reports exists=false, err=nil", func(t *testing.T) {
		t.Parallel()
		sample, exists, err := repo.CatFileSample(ctx, "HEAD", "nosuch.txt", 16)
		if err != nil {
			t.Fatalf("CatFileSample: %v", err)
		}
		if exists {
			t.Error("exists = true; want false")
		}
		if sample != nil {
			t.Errorf("sample = %q; want nil", sample)
		}
	})

	t.Run("revision that does not resolve reports exists=false, err=nil", func(t *testing.T) {
		t.Parallel()
		_, exists, err := repo.CatFileSample(ctx, "nosuchrev", "small.txt", 16)
		if err != nil {
			t.Fatalf("CatFileSample: %v", err)
		}
		if exists {
			t.Error("exists = true; want false")
		}
	})
}

func TestCatFilePromisorMissingIsAnError(t *testing.T) {
	t.Parallel()
	_, repo := newBloblessClone(t)
	ctx := context.Background()

	if _, exists, err := repo.CatFile(ctx, "HEAD", "absent.go"); err != nil || exists {
		t.Fatalf("CatFile(absent.go) = (_, %v, %v); want (nil, false)", exists, err)
	}
	_, exists, err := repo.CatFile(ctx, "HEAD", "tracked.go")
	assertPromisorMissing(t, "CatFile", exists, err)

	if _, exists, err := repo.CatFileSample(ctx, "HEAD", "absent.go", 16); err != nil || exists {
		t.Fatalf("CatFileSample(absent.go) = (_, %v, %v); want (nil, false)", exists, err)
	}
	_, exists, err = repo.CatFileSample(ctx, "HEAD", "tracked.go", 16)
	assertPromisorMissing(t, "CatFileSample", exists, err)

	results, err := repo.BatchCatFile(ctx, []gitx.BatchCatFileRequest{
		{Rev: "HEAD", Path: "absent.go"},
	})
	if err != nil || len(results) != 1 || results[0].Exists {
		t.Fatalf("BatchCatFile(absent.go) = (%v, %v); want one absent result", results, err)
	}
	_, err = repo.BatchCatFile(ctx, []gitx.BatchCatFileRequest{
		{Rev: "HEAD", Path: "tracked.go"},
	})
	assertPromisorMissing(t, "BatchCatFile", false, err)
}

func assertPromisorMissing(t *testing.T, name string, exists bool, err error) {
	t.Helper()
	if exists {
		t.Errorf("%s exists = true; want false", name)
	}
	var gerr *gitx.GitError
	if !errors.As(err, &gerr) {
		t.Fatalf("%s error = %v (%T); want *gitx.GitError", name, err, err)
	}
	if gerr.ExitCode != 128 {
		t.Errorf("%s exit code = %d; want 128", name, gerr.ExitCode)
	}
	if len(gerr.Stderr) == 0 {
		t.Errorf("%s stderr is empty; want git's failure message", name)
	}
	if strings.Contains(string(gerr.Stderr), "promisor object is unavailable") {
		t.Errorf("%s stderr contains synthetic promisor message: %q", name, gerr.Stderr)
	}
}

func newBloblessClone(t *testing.T) (string, *gitx.Repo) {
	t.Helper()
	dir, _ := gittest.New(t)
	const missingBlob = "1111111111111111111111111111111111111111"
	treeInput := fmt.Sprintf("100644 blob %s\ttracked.go\n", missingBlob)
	tree := runGitInput(t, dir, treeInput, "mktree", "--missing")
	commit := gittest.Git(t, dir, "commit-tree", tree, "-m", "chore: add tracked file")
	gittest.Git(t, dir, "update-ref", "refs/heads/main", strings.TrimSpace(commit))
	gittest.Git(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	gittest.Git(t, dir, "remote", "add", "origin", filepath.Join(t.TempDir(), "unreachable"))
	gittest.Git(t, dir, "config", "remote.origin.promisor", "true")
	gittest.Git(t, dir, "config", "extensions.partialClone", "origin")
	return dir, gitx.New(dir)
}

func runGitInput(t *testing.T, dir, input string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// TestBlame pins the -L bounding itself: blaming lines 2,2 of a three-line
// file must name only that line's own commit, never the ones before or
// after it -- the guardrail rgit blame exists to hold (docs/USAGE.md §
// Blame: no whole-file fallback).
func TestBlame(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	gittest.Write(t, dir, "f.txt", "one\ntwo\nthree\n")
	gittest.Commit(t, dir, "chore: three lines")

	out, err := repo.Blame(ctx, "f.txt", 2, 2)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "two") {
		t.Errorf("Blame(2,2) output = %q; want it to name line 2's content (\"two\")", got)
	}
	if strings.Contains(got, "one") || strings.Contains(got, "three") {
		t.Errorf("Blame(2,2) output = %q; want it bounded to line 2 only, not the whole file", got)
	}
}

// TestBlame_Porcelain pins that extra args (here --porcelain) actually
// reach git: porcelain blame output names the author on its own line,
// which the default human-readable format does not.
func TestBlame_Porcelain(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	gittest.Write(t, dir, "f.txt", "one\n")
	gittest.Commit(t, dir, "chore: one line")

	out, err := repo.Blame(ctx, "f.txt", 1, 1, "--porcelain")
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	if !strings.Contains(string(out), "\nauthor ") {
		t.Errorf("Blame(--porcelain) output = %q; want an \"author \" line", out)
	}
}

// TestLogLineRange_RejectsColonInPath pins LogLineRange's own refusal
// (LogLineRange's doc comment): git log's "-L<range>:<path>" argument joins
// the range and the path with ':' and has no escape for one inside path, and
// -- measured directly against this repo's own git -- log flatly refuses
// the one alternative shape that would sidestep it ("-L<range>:<path> --
// <pathspec>" is "fatal: -L<range>:<file> cannot be used with pathspec"), so
// there is no safe way to run the command at all for such a path. A colon
// must be refused before the argument is ever built, not discovered from
// git's own exit status.
func TestLogLineRange_RejectsColonInPath(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	gittest.Write(t, dir, "weird:file.txt", "one\ntwo\n")
	gittest.Commit(t, dir, "chore: add weird:file.txt")

	_, err := repo.LogLineRange(ctx, "weird:file.txt", 1, 1)

	var perr *gitx.LineRangePathError
	if !errors.As(err, &perr) {
		t.Fatalf("LogLineRange error = %v (%T); want *gitx.LineRangePathError", err, err)
	}
	if perr.Path != "weird:file.txt" {
		t.Errorf("LineRangePathError.Path = %q; want %q", perr.Path, "weird:file.txt")
	}
}

// TestDiffPatch pins that DiffPatch returns git's own real patch body
// unmodified, and that it respects the same extra args (--staged, a
// pathspec after --) DiffNumstat already does -- the two are supposed to
// describe the identical scope, just in raw vs. parsed form.
func TestDiffPatch(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	gittest.Write(t, dir, "a.txt", "one\n")
	gittest.Write(t, dir, "b.txt", "one\n")
	gittest.Commit(t, dir, "chore: add a and b")

	gittest.Write(t, dir, "a.txt", "two\n")
	gittest.Write(t, dir, "b.txt", "two\n")
	gittest.Git(t, dir, "add", "-A")

	out, err := repo.DiffPatch(ctx, "--staged", "--", "a.txt")
	if err != nil {
		t.Fatalf("DiffPatch: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "diff --git a/a.txt b/a.txt") {
		t.Errorf("DiffPatch(--staged, a.txt) = %q; want a real patch header for a.txt", got)
	}
	if !strings.Contains(got, "@@") {
		t.Errorf("DiffPatch(--staged, a.txt) = %q; want a hunk marker", got)
	}
	if strings.Contains(got, "b.txt") {
		t.Errorf("DiffPatch(--staged, a.txt) = %q; want b.txt excluded by the pathspec", got)
	}
}

// TestLog_FiltersByPath pins rgit log's own path-scoped shape (docs/USAGE.md
// § Log): a path filter narrows to only the commits that actually touched
// it, the same way plain `git log -- path` does, and every other commit is
// excluded even though it happened in between.
func TestLog_FiltersByPath(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	gittest.Write(t, dir, "a.txt", "a\n")
	gittest.Write(t, dir, "b.txt", "b\n")
	gittest.Commit(t, dir, "chore: add a and b")

	gittest.Write(t, dir, "a.txt", "a2\n")
	gittest.Commit(t, dir, "fix(a): bump a")

	gittest.Write(t, dir, "b.txt", "b2\n")
	gittest.Commit(t, dir, "fix(b): bump b")

	out, err := repo.Log(ctx, "", "", []string{"a.txt"}, "--no-patch", "--format=%s")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "bump a") {
		t.Errorf("Log(paths=[a.txt]) = %q; want it to include the commit that touched a.txt", got)
	}
	if strings.Contains(got, "bump b") {
		t.Errorf("Log(paths=[a.txt]) = %q; want it to exclude the commit that never touched a.txt", got)
	}
}

// TestLog_SinceExcludesEarlierCommits pins that --since is forwarded to
// git rather than reimplemented: an old commit dated well before the bound
// is excluded, and a commit dated after it is not -- the actual date
// comparison is entirely git's own.
//
// No t.Parallel: t.Setenv forbids it (CONTRIBUTING.md § Tests).
func TestLog_SinceExcludesEarlierCommits(t *testing.T) {
	dir, repo := gittest.New(t)
	ctx := context.Background()

	t.Setenv("GIT_AUTHOR_DATE", "2020-01-01T00:00:00")
	t.Setenv("GIT_COMMITTER_DATE", "2020-01-01T00:00:00")
	gittest.Write(t, dir, "f.txt", "one\n")
	gittest.Commit(t, dir, "chore: old commit")

	t.Setenv("GIT_AUTHOR_DATE", "2030-01-01T00:00:00")
	t.Setenv("GIT_COMMITTER_DATE", "2030-01-01T00:00:00")
	gittest.Write(t, dir, "f.txt", "two\n")
	gittest.Commit(t, dir, "chore: new commit")

	out, err := repo.Log(ctx, "2025-01-01", "", nil, "--no-patch", "--format=%s")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "new commit") {
		t.Errorf("Log(since=2025-01-01) = %q; want the 2030 commit included", got)
	}
	if strings.Contains(got, "old commit") {
		t.Errorf("Log(since=2025-01-01) = %q; want the 2020 commit excluded", got)
	}
}

// TestLog_MaxCount pins that Log forwards git's own count limit through its
// existing extra-argument path rather than truncating output in Go.
func TestLog_MaxCount(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	gittest.Write(t, dir, "f.txt", "one\n")
	gittest.Commit(t, dir, "chore: first commit")
	gittest.Write(t, dir, "f.txt", "two\n")
	gittest.Commit(t, dir, "chore: second commit")
	gittest.Write(t, dir, "f.txt", "three\n")
	gittest.Commit(t, dir, "chore: third commit")

	out, err := repo.Log(ctx, "", "", []string{"f.txt"}, "-n", "1", "--no-patch", "--format=%s")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	got := string(out)
	if got != "chore: third commit\n" {
		t.Errorf("Log(max-count=1) = %q; want only the newest commit", got)
	}
}

// TestUpstreamAndAheadBehind pins the rgit-context B record's own two
// primitives: no upstream configured is a normal negative answer, not a
// failure or a *GitError -- and once one is configured, AheadBehind counts
// diverge correctly in both directions after a local-only commit.
func TestUpstreamAndAheadBehind(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "a.go", "package a\n", "chore: initial")

	if _, ok, err := repo.Upstream(context.Background()); err != nil || ok {
		t.Fatalf("Upstream() = (_, %v, %v); want ok=false with no upstream configured", ok, err)
	}
	if _, _, err := repo.AheadBehind(context.Background()); err == nil {
		t.Error("AheadBehind() with no upstream configured; want an error, not a silent 0/0")
	}

	remote := t.TempDir()
	gittest.Git(t, remote, "init", "-q", "--bare")
	gittest.Git(t, dir, "remote", "add", "origin", remote)
	gittest.Git(t, dir, "push", "-q", "-u", "origin", "main")

	name, ok, err := repo.Upstream(context.Background())
	if err != nil || !ok || name != "origin/main" {
		t.Fatalf("Upstream() = (%q, %v, %v); want (\"origin/main\", true, nil)", name, ok, err)
	}
	if ahead, behind, err := repo.AheadBehind(context.Background()); err != nil || ahead != 0 || behind != 0 {
		t.Fatalf("AheadBehind() = (%d, %d, %v); want (0, 0, nil) right after push", ahead, behind, err)
	}

	gittest.Write(t, dir, "b.go", "package a\n")
	gittest.Commit(t, dir, "chore: local-only commit")

	if ahead, behind, err := repo.AheadBehind(context.Background()); err != nil || ahead != 1 || behind != 0 {
		t.Fatalf("AheadBehind() = (%d, %d, %v); want (1, 0, nil) one commit ahead", ahead, behind, err)
	}
}

// TestBatchCatFile pins the batch reader's own two response shapes and
// ordering guarantee against a real `git cat-file --batch` process: found
// blobs come back with their content, missing ones with Exists=false, in
// request order regardless of which rev each one names -- the whole point
// of batching many files' reads into one process (internal/diff's own
// per-file scope.read loop).
func TestBatchCatFile(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "a.go", "package a\n")
	gittest.Write(t, dir, "path with spaces.go", "package a\n")
	gittest.Commit(t, dir, "chore: first")
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() {}\n")
	gittest.Commit(t, dir, "chore: second")

	results, err := repo.BatchCatFile(context.Background(), []gitx.BatchCatFileRequest{
		{Rev: "HEAD", Path: "a.go"},
		{Rev: "HEAD~1", Path: "a.go"},
		{Rev: "HEAD", Path: "path with spaces.go"},
		{Rev: "HEAD", Path: "does-not-exist.go"},
	})
	if err != nil {
		t.Fatalf("BatchCatFile: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("len(results) = %d; want 4", len(results))
	}

	want := []struct {
		exists  bool
		content string
	}{
		{true, "package a\n\nfunc A() {}\n"},
		{true, "package a\n"},
		{true, "package a\n"},
		{false, ""},
	}
	for i, w := range want {
		if results[i].Exists != w.exists {
			t.Errorf("results[%d].Exists = %v; want %v", i, results[i].Exists, w.exists)
		}
		if w.exists && string(results[i].Content) != w.content {
			t.Errorf("results[%d].Content = %q; want %q", i, results[i].Content, w.content)
		}
	}
}

// TestBatchCatFile_SubmodulePathIsExistsFalse pins the third response shape
// `git cat-file --batch` writes for a gitlink entry: "<sha> submodule\n",
// no size field and no content bytes -- unlike the "<sha> <type> <size>"
// shape TestBatchCatFile above already covers. CatFile (singular, "-p")
// already reports a submodule path as Exists=false, because `cat-file -p
// rev:path` on a gitlink exits non-zero ("Not a valid object name") and
// that non-zero exit hits CatFile's own exists=false convention. BatchCatFile
// has to agree: prefetchBlobs' blobCache doc comment requires a cache hit
// and contentSide.read's live CatFile fallback to answer identically for the
// same (rev, path), and a submodule path reached this exact request shape
// unhandled in production -- rgit diff/commit on any repo with a submodule
// whose pointer changed (docs/USAGE.md's own numstat scope includes gitlink
// entries) errored "malformed response header" instead of treating the
// pointer bump like any other whole-file change.
func TestBatchCatFile_SubmodulePathIsExistsFalse(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "a.go", "package a\n", "chore: first")
	gittest.Git(t, dir, "update-index", "--add", "--cacheinfo",
		"160000,0123456789abcdef0123456789abcdef01234567,mysub")
	gittest.Git(t, dir, "commit", "-q", "-m", "chore: add gitlink")

	results, err := repo.BatchCatFile(context.Background(), []gitx.BatchCatFileRequest{
		{Rev: "HEAD", Path: "mysub"},
		{Rev: "HEAD", Path: "a.go"},
	})
	if err != nil {
		t.Fatalf("BatchCatFile: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d; want 2", len(results))
	}
	if results[0].Exists {
		t.Errorf("results[0] (submodule path) Exists = true; want false, matching CatFile's own convention")
	}
	if len(results[0].Content) != 0 {
		t.Errorf("results[0] (submodule path) Content = %q; want empty", results[0].Content)
	}
	if !results[1].Exists || string(results[1].Content) != "package a\n" {
		t.Errorf("results[1] (a.go) = (%v, %q); want (true, %q)", results[1].Exists, results[1].Content, "package a\n")
	}
}

// TestBatchCatFile_EmptyRequestIsANoop pins that zero requests never spawns
// a process at all -- there is nothing for one to answer.
func TestBatchCatFile_EmptyRequestIsANoop(t *testing.T) {
	t.Parallel()
	_, repo := gittest.New(t)

	results, err := repo.BatchCatFile(context.Background(), nil)
	if err != nil || results != nil {
		t.Errorf("BatchCatFile(nil) = (%v, %v); want (nil, nil)", results, err)
	}
}

// TestErrorMessagesNameTheCommand pins what a caller actually reads when
// something goes wrong. Both types are surfaced verbatim by internal/app's
// error mapping, so their text is the whole failure report -- and an
// unasserted message is how it can quietly lose the one detail that makes
// it actionable.
func TestErrorMessagesNameTheCommand(t *testing.T) {
	t.Parallel()

	// GitError: git ran and chose a status. The command, the status, and
	// git's own stderr all have to survive, with the stderr trimmed so the
	// message stays one line.
	gerr := &gitx.GitError{
		Args:     []string{"commit", "-m", "x"},
		ExitCode: 128,
		Stderr:   []byte("fatal: nothing to commit\n\n"),
	}
	if got, want := gerr.Error(), "git commit -m x: exit 128: fatal: nothing to commit"; got != want {
		t.Errorf("GitError.Error() = %q; want %q", got, want)
	}
	quiet := &gitx.GitError{
		Args:     []string{"commit", "-m", "x"},
		ExitCode: 1,
		Stdout:   []byte("On branch main\nnothing to commit, working tree clean\n"),
	}
	if got, want := quiet.Error(), "git commit -m x: exit 1: On branch main\nnothing to commit, working tree clean"; got != want {
		t.Errorf("GitError.Error() with empty stderr = %q; want %q", got, want)
	}

	// ExecError: git never ran. It wraps the cause, so errors.Is/As still
	// reach it -- callers distinguish "git failed" from "git is missing".
	cause := os.ErrNotExist
	eerr := &gitx.ExecError{Args: []string{"rev-parse", "HEAD"}, Err: cause}
	if got, want := eerr.Error(), "git rev-parse HEAD: "+cause.Error(); got != want {
		t.Errorf("ExecError.Error() = %q; want %q", got, want)
	}
	if !errors.Is(eerr, cause) {
		t.Error("ExecError does not unwrap to its cause")
	}
}

func TestCommit_ReuseMessageForwarded(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "a.txt", "content\n", "feat: original")
	gittest.Write(t, dir, "a.txt", "staged content\n")
	gittest.Git(t, dir, "add", "a.txt")

	if _, err := repo.Commit(context.Background(), gitx.CommitOptions{
		ReuseMessage: "HEAD",
	}); err != nil {
		t.Fatalf("Commit(--reuse-message=HEAD): %v", err)
	}
	if got := gittest.Git(t, dir, "log", "-1", "--format=%s"); got != "feat: original\n" {
		t.Errorf("reused commit subject = %q; want %q", got, "feat: original\n")
	}
	if got := gittest.Git(t, dir, "show", "HEAD:a.txt"); got != "staged content\n" {
		t.Errorf("HEAD:a.txt = %q; want staged content", got)
	}
}

func TestCommit_SignoffAppendsSignedOffBy(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "a.txt", "content\n", "chore: initial")
	gittest.Write(t, dir, "a.txt", "updated content\n")
	gittest.Git(t, dir, "add", "a.txt")

	if _, err := repo.Commit(context.Background(), gitx.CommitOptions{
		Messages: []string{"feat: signoff"},
		Signoff:  true,
	}); err != nil {
		t.Fatalf("Commit(--signoff): %v", err)
	}
	if got := gittest.Git(t, dir, "log", "-1", "--format=%b"); !strings.Contains(got, "Signed-off-by: rgit Test <rgit-test@example.com>") {
		t.Errorf("commit body = %q; want signoff trailer", got)
	}
}

func TestCommit_TrailerForwarded(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "a.txt", "content\n", "chore: initial")
	gittest.Write(t, dir, "a.txt", "updated content\n")
	gittest.Git(t, dir, "add", "a.txt")

	if _, err := repo.Commit(context.Background(), gitx.CommitOptions{
		Messages: []string{"feat: trailer"},
		Trailers: []string{"Refs: #1"},
	}); err != nil {
		t.Fatalf("Commit(--trailer): %v", err)
	}
	if got := gittest.Git(t, dir, "log", "-1", "--format=%b"); !strings.Contains(got, "Refs: #1") {
		t.Errorf("commit body = %q; want trailer", got)
	}
}

func TestCommitOnlyUsesTemporaryIndex(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "a.txt", "a before\n")
	gittest.Write(t, dir, "b.txt", "b before\n")
	gittest.Commit(t, dir, "chore: initial")
	gittest.Write(t, dir, "a.txt", "a after\n")
	gittest.Write(t, dir, "b.txt", "b after\n")
	gittest.Git(t, dir, "add", "a.txt", "b.txt")

	if _, err := repo.Commit(context.Background(), gitx.CommitOptions{
		Messages:  []string{"feat: update a"},
		Only:      true,
		OnlyPaths: []string{"a.txt"},
	}); err != nil {
		t.Fatalf("Commit(Only): %v", err)
	}

	if got := gittest.Git(t, dir, "show", "HEAD:a.txt"); got != "a after\n" {
		t.Errorf("HEAD:a.txt = %q; want updated content", got)
	}
	if got := gittest.Git(t, dir, "show", "HEAD:b.txt"); got != "b before\n" {
		t.Errorf("HEAD:b.txt = %q; want prior content", got)
	}
	if got := gittest.Git(t, dir, "diff", "--cached", "--name-only"); got != "b.txt\n" {
		t.Errorf("cached paths after --only commit = %q; want b.txt only", got)
	}
}

// A pre-commit hook that bumps and stages a file runs against --only's
// temporary index; its bump must land in HEAD and in the real index, while
// work staged before the commit stays staged.
func TestCommitOnlySyncsHookStagedPathsToIndex(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "a.txt", "a before\n")
	gittest.Write(t, dir, "version.txt", "1\n")
	gittest.Write(t, dir, "staged.txt", "staged before\n")
	gittest.Commit(t, dir, "chore: initial")
	gittest.InstallHook(t, dir, "pre-commit", "#!/bin/sh\necho 2 > version.txt\ngit add version.txt\n")
	gittest.Write(t, dir, "a.txt", "a after\n")
	gittest.Write(t, dir, "staged.txt", "staged after\n")
	gittest.Git(t, dir, "add", "a.txt", "staged.txt")

	if _, err := repo.Commit(context.Background(), gitx.CommitOptions{
		Messages:  []string{"feat: update a"},
		Only:      true,
		OnlyPaths: []string{"a.txt"},
	}); err != nil {
		t.Fatalf("Commit(Only): %v", err)
	}

	if got := gittest.Git(t, dir, "show", "HEAD:version.txt"); got != "2\n" {
		t.Errorf("HEAD:version.txt = %q; want the hook's bump", got)
	}
	if got := gittest.Git(t, dir, "status", "--porcelain"); got != "M  staged.txt\n" {
		t.Errorf("status after --only commit = %q; want only staged.txt staged, index in sync with HEAD", got)
	}
}

func TestCommitOnlyCommitsADeletedPath(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "gone.txt", "gone\n")
	gittest.Write(t, dir, "kept.txt", "kept\n")
	gittest.Commit(t, dir, "chore: initial")
	gittest.Git(t, dir, "rm", "--quiet", "gone.txt")

	if _, err := repo.Commit(context.Background(), gitx.CommitOptions{
		Messages:  []string{"chore: drop gone.txt"},
		Only:      true,
		OnlyPaths: []string{"gone.txt"},
	}); err != nil {
		t.Fatalf("Commit(Only, deleted path): %v", err)
	}

	if got := gittest.Git(t, dir, "ls-tree", "HEAD", "--", "gone.txt"); got != "" {
		t.Errorf("HEAD gone.txt = %q; want absent from tree", got)
	}
	if got := gittest.Git(t, dir, "show", "HEAD:kept.txt"); got != "kept\n" {
		t.Errorf("HEAD:kept.txt = %q; want untouched", got)
	}
}

func TestCommitOnlyWithNoPathsLeavesOtherStagedWork(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "base.txt", "base\n", "chore: initial")
	gittest.Write(t, dir, "extra.txt", "staged only\n")
	gittest.Git(t, dir, "add", "extra.txt")

	if _, err := repo.Commit(context.Background(), gitx.CommitOptions{
		Only:      true,
		Amend:     true,
		NoEdit:    true,
		OnlyPaths: nil,
	}); err != nil {
		t.Fatalf("Commit(Only, no paths): %v", err)
	}

	if got := gittest.Git(t, dir, "ls-tree", "HEAD", "--", "extra.txt"); got != "" {
		t.Errorf("HEAD extra.txt = %q; want absent from amended tree", got)
	}
	if got := gittest.Git(t, dir, "diff", "--cached", "--name-only"); got != "extra.txt\n" {
		t.Errorf("cached paths after --only amend = %q; want extra.txt only", got)
	}
}

func TestWithIndexFileDoesNotMutateProcessEnv(t *testing.T) {
	t.Parallel()
	before, had := os.LookupEnv("GIT_INDEX_FILE")
	_ = gitx.New(t.TempDir()).WithIndexFile(filepath.Join(t.TempDir(), "index"))
	got, still := os.LookupEnv("GIT_INDEX_FILE")
	if had != still || got != before {
		t.Fatalf("GIT_INDEX_FILE process env changed: before had=%v val=%q, after had=%v val=%q", had, before, still, got)
	}
}
