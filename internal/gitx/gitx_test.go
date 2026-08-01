// Package gitx_test is an external test package (rather than the whitebox
// "package gitx" the rest of this package's tests could use) so it can
// build its fixtures through internal/gittest: gittest itself imports gitx,
// and a whitebox test file importing gittest back would be a cycle.
package gitx_test

import (
	"context"
	"errors"
	"os"
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
