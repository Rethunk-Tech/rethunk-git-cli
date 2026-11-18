package gitx_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gitx"
)

// TestCommittableBase covers both ends of the "what tree does a commit build
// on" question: an unborn branch names no commit, so the empty tree stands in
// and every tracked path reads as an addition; once a commit exists, HEAD is
// the base itself.
func TestCommittableBase(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	emptyTree, err := repo.EmptyTree(ctx)
	if err != nil {
		t.Fatalf("EmptyTree: %v", err)
	}
	if len(emptyTree) < 40 {
		t.Errorf("EmptyTree = %q; want a full object id", emptyTree)
	}

	base, err := repo.CommittableBase(ctx)
	if err != nil {
		t.Fatalf("CommittableBase(unborn) = %v", err)
	}
	if base != emptyTree {
		t.Errorf("CommittableBase(unborn) = %q; want the empty tree %q", base, emptyTree)
	}

	gittest.Write(t, dir, "a.txt", "a\n")
	gittest.Commit(ctx, t, dir, "chore: initial")

	base, err = repo.CommittableBase(ctx)
	if err != nil {
		t.Fatalf("CommittableBase(committed) = %v", err)
	}
	if base != "HEAD" {
		t.Errorf("CommittableBase(committed) = %q; want \"HEAD\"", base)
	}
}

// TestHashObject_AppliesCleanFilter pins the one thing that makes --path
// mandatory rather than cosmetic: a .gitattributes clean filter must rewrite
// the content before it is written as a blob. Without --path git would store
// the raw bytes and the staged blob would disagree with what `git add` of the
// same file produces.
func TestHashObject_AppliesCleanFilter(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	gittest.Write(t, dir, ".gitattributes", "*.upper filter=upper\n")
	gittest.Git(ctx, t, dir, "config", "filter.upper.clean", "tr a-z A-Z")
	gittest.Commit(ctx, t, dir, "chore: filter fixture")

	filtered, err := repo.HashObject(ctx, "note.upper", []byte("hello\n"))
	if err != nil {
		t.Fatalf("HashObject(filtered path): %v", err)
	}
	raw, err := repo.HashObject(ctx, "note.txt", []byte("hello\n"))
	if err != nil {
		t.Fatalf("HashObject(unfiltered path): %v", err)
	}
	if filtered == raw {
		t.Errorf("HashObject wrote the same object for a filtered and an unfiltered path; want --path to apply the clean filter")
	}

	shown := gittest.Git(ctx, t, dir, "cat-file", "-p", filtered)
	if shown != "HELLO\n" {
		t.Errorf("blob content = %q; want the clean filter's uppercase rewrite", shown)
	}
}

// TestUnmergedPaths pins the whole-index conflict query against a real
// multi-stage index, including the NUL-separated record shape: every stage of
// the same path collapses to one entry, and a non-conflicted path is absent.
func TestUnmergedPaths(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	gittest.Write(t, dir, "clean.txt", "clean\n")
	gittest.Write(t, dir, "conflict.txt", "conflict\n")
	gittest.Commit(ctx, t, dir, "chore: fixtures")

	blob := hashObject(t, dir, "conflict\n")
	gittest.Unmerged(ctx, t, dir, blob, "conflict.txt")

	paths, err := repo.UnmergedPaths(ctx)
	if err != nil {
		t.Fatalf("UnmergedPaths: %v", err)
	}
	if !paths["conflict.txt"] {
		t.Errorf("UnmergedPaths = %v; want it to name conflict.txt", paths)
	}
	if paths["clean.txt"] {
		t.Errorf("UnmergedPaths = %v; want the clean path absent", paths)
	}
}

// TestChangedPaths pins the two lists apart: a staged edit and an unstaged
// edit of different files must not be reported as each other, and an
// untracked file belongs to neither.
func TestChangedPaths(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	gittest.Write(t, dir, "staged.txt", "one\n")
	gittest.Write(t, dir, "unstaged.txt", "one\n")
	gittest.Commit(ctx, t, dir, "chore: fixtures")

	gittest.Write(t, dir, "staged.txt", "two\n")
	gittest.Git(ctx, t, dir, "add", "staged.txt")
	gittest.Write(t, dir, "unstaged.txt", "two\n")
	gittest.Write(t, dir, "untracked.txt", "new\n")

	staged, unstaged, err := repo.ChangedPaths(ctx)
	if err != nil {
		t.Fatalf("ChangedPaths: %v", err)
	}
	if got, want := strings.Join(staged, ","), "staged.txt"; got != want {
		t.Errorf("staged = %q; want %q", got, want)
	}
	if got, want := strings.Join(unstaged, ","), "unstaged.txt"; got != want {
		t.Errorf("unstaged = %q; want %q", got, want)
	}

	// A clean repository answers both lists empty, not an error and not a
	// list holding one empty string.
	gittest.Commit(ctx, t, dir, "chore: commit everything")
	staged, unstaged, err = repo.ChangedPaths(ctx)
	if err != nil {
		t.Fatalf("ChangedPaths(clean): %v", err)
	}
	if len(staged) != 0 || len(unstaged) != 0 {
		t.Errorf("ChangedPaths(clean) = (%v, %v); want both empty", staged, unstaged)
	}
}

// TestDirtyPaths_ReportsRenameDestinationOnly pins the rename record shape:
// git writes the destination first and the source as its own NUL-separated
// field, so a caller listing dirty paths must name the file that exists now
// and must not treat the source name as a path with a leading two-letter
// status.
func TestDirtyPaths_ReportsRenameDestinationOnly(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	gittest.Write(t, dir, "before.txt", "content\n")
	gittest.Commit(ctx, t, dir, "chore: initial")
	gittest.Git(ctx, t, dir, "mv", "before.txt", "after.txt")
	gittest.Write(t, dir, "untracked.txt", "new\n")

	paths, err := repo.DirtyPaths(ctx)
	if err != nil {
		t.Fatalf("DirtyPaths: %v", err)
	}
	got := strings.Join(paths, ",")
	if !strings.Contains(got, "after.txt") {
		t.Errorf("DirtyPaths = %v; want the rename destination after.txt", paths)
	}
	if strings.Contains(got, "before.txt") {
		t.Errorf("DirtyPaths = %v; want the rename source skipped", paths)
	}
	if !strings.Contains(got, "untracked.txt") {
		t.Errorf("DirtyPaths = %v; want the untracked file", paths)
	}
}

// TestFindRename pins the walk one segment back: the rename commit's own hash
// and the name the path carried before it. A path that was never renamed
// answers found=false rather than inventing a commit.
func TestFindRename(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	gittest.Write(t, dir, "before.txt", strings.Repeat("body line\n", 20))
	gittest.Commit(ctx, t, dir, "chore: initial")
	gittest.Git(ctx, t, dir, "mv", "before.txt", "after.txt")
	gittest.Git(ctx, t, dir, "commit", "-q", "-m", "chore: rename")

	commit, oldPath, found, err := repo.FindRename(ctx, "HEAD", "after.txt")
	if err != nil {
		t.Fatalf("FindRename: %v", err)
	}
	if !found {
		t.Fatal("FindRename = found false; want the rename commit")
	}
	if oldPath != "before.txt" {
		t.Errorf("FindRename oldPath = %q; want %q", oldPath, "before.txt")
	}
	if head := gittest.Git(ctx, t, dir, "rev-parse", "HEAD", "HEAD^"); !strings.HasPrefix(head, commit) {
		t.Errorf("FindRename commit = %q; want HEAD's own hash (%q)", commit, head)
	}

	if _, _, found, err := repo.FindRename(ctx, "HEAD", "before.txt"); err != nil {
		t.Fatalf("FindRename(never renamed): %v", err)
	} else if found {
		t.Error("FindRename(before.txt) = found true; want false -- it was never renamed into that name")
	}
}

// TestRecentCommits pins the two negative cases: an unborn branch reports no
// commits rather than a *GitError, and the count bound is git's own -n limit
// rather than a slice after the fact.
func TestRecentCommits(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	got, err := repo.RecentCommits(ctx, 3)
	if err != nil {
		t.Fatalf("RecentCommits(unborn) = %v; want no commits and no error", err)
	}
	if len(got) != 0 {
		t.Errorf("RecentCommits(unborn) = %v; want empty", got)
	}

	for _, msg := range []string{"chore: one", "chore: two", "chore: three"} {
		gittest.Write(t, dir, "f.txt", msg+"\n")
		gittest.Commit(ctx, t, dir, msg)
	}

	got, err = repo.RecentCommits(ctx, 2)
	if err != nil {
		t.Fatalf("RecentCommits: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("RecentCommits(limit=2) returned %d commits; want 2", len(got))
	}
	if got[0].Subject != "chore: three" || got[1].Subject != "chore: two" {
		t.Errorf("RecentCommits = %+v; want newest first", got)
	}
	if len(got[0].Hash) < 40 {
		t.Errorf("RecentCommits[0].Hash = %q; want a full object id", got[0].Hash)
	}
}

// TestCheckIgnore pins all three answers: an ignored path, a path that is
// simply not ignored (git's exit 1, a normal negative answer, not a
// *GitError), and a fatal error git itself reports.
func TestCheckIgnore(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	gittest.Write(t, dir, ".gitignore", "*.log\n")
	gittest.Commit(ctx, t, dir, "chore: ignore logs")

	ignored, err := repo.CheckIgnore(ctx, "trace.log")
	if err != nil || !ignored {
		t.Errorf("CheckIgnore(trace.log) = (%v, %v); want (true, nil)", ignored, err)
	}
	ignored, err = repo.CheckIgnore(ctx, "main.go")
	if err != nil || ignored {
		t.Errorf("CheckIgnore(main.go) = (%v, %v); want (false, nil) -- exit 1 is a plain no", ignored, err)
	}
}

// TestLsTreeAndTolerant pins the difference between the two: a path absent
// from a real tree is found=false for both, but a rev that does not resolve
// at all is an error for LsTree and a plain not-found for LsTreeTolerant --
// while a git that cannot be run at all stays a real failure for both.
func TestLsTreeAndTolerant(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	gittest.Write(t, dir, "a.go", "package a\n")
	gittest.Commit(ctx, t, dir, "chore: initial")

	entry, found, err := repo.LsTree(ctx, "HEAD", "a.go")
	if err != nil || !found {
		t.Fatalf("LsTree(HEAD, a.go) = (%+v, %v, %v); want found", entry, found, err)
	}
	if entry.Mode != "100644" || entry.Type != "blob" {
		t.Errorf("LsTree(HEAD, a.go) = %+v; want mode 100644 type blob", entry)
	}
	if _, found, err := repo.LsTree(ctx, "HEAD", "absent.go"); err != nil || found {
		t.Errorf("LsTree(HEAD, absent.go) = (_, %v, %v); want (false, nil)", found, err)
	}
	if _, _, err := repo.LsTree(ctx, "nosuchrev", "a.go"); err == nil {
		t.Error("LsTree(nosuchrev) = nil error; want a *GitError")
	}

	if _, found, err := repo.LsTreeTolerant(ctx, "nosuchrev", "a.go"); err != nil || found {
		t.Errorf("LsTreeTolerant(nosuchrev) = (_, %v, %v); want (false, nil)", found, err)
	}
}

// TestExecErrorWhenGitIsMissing pins the third outcome of the package's
// contract: a git binary that cannot be run at all is an *ExecError, never a
// *GitError, so a caller can tell "git said no" from "git is not there".
//
// No t.Parallel: t.Setenv forbids it (CONTRIBUTING.md § Tests).
func TestExecErrorWhenGitIsMissing(t *testing.T) {
	_, repo := gittest.New(t.Context(), t)

	t.Setenv("PATH", t.TempDir())
	_, _, err := repo.RevParseVerify(t.Context(), "HEAD")

	if _, ok := errors.AsType[*gitx.ExecError](err); !ok {
		t.Fatalf("RevParseVerify with no git on PATH = %v (%T); want *gitx.ExecError", err, err)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("ExecError = %v; want it to wrap exec.ErrNotFound", err)
	}
}

// TestCleansCRLFReportsWhetherGitWouldConvertLineEndings pins the decision
// tree git's own `ls-files --eol` plus core.autocrlf feed: a text attribute
// forces conversion, -text forbids it, text=auto and an unset attribute
// follow the index copy (and autocrlf, when the attribute is unset), and an
// untracked path is reported unconverted because there is no index copy to
// consult.
func TestCleansCRLFReportsWhetherGitWouldConvertLineEndings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tracked   bool
		content   string
		attr      string
		autocrlf  string
		wantClean bool
	}{
		{
			name:      "untracked path has no index copy",
			content:   "hello\n",
			wantClean: false,
		},
		{
			name:      "-text never converts",
			tracked:   true,
			content:   "hello\n",
			attr:      "* -text\n",
			wantClean: false,
		},
		{
			name:      "text forces conversion",
			tracked:   true,
			content:   "hello\n",
			attr:      "* text\n",
			wantClean: true,
		},
		{
			name:      "text with extra tokens still forces conversion",
			tracked:   true,
			content:   "hello\n",
			attr:      "* text eol=lf\n",
			wantClean: true,
		},
		{
			name:      "unset attribute and autocrlf off leaves LF alone",
			tracked:   true,
			content:   "hello\n",
			wantClean: false,
		},
		{
			name:      "text=auto converts an LF index copy",
			tracked:   true,
			content:   "hello\n",
			attr:      "* text=auto\n",
			wantClean: true,
		},
		{
			name:      "unset attribute under autocrlf=input converts LF",
			tracked:   true,
			content:   "hello\n",
			autocrlf:  "input",
			wantClean: true,
		},
		{
			name:      "unset attribute under autocrlf=true leaves CRLF already in the index",
			tracked:   true,
			content:   "hello\r\n",
			autocrlf:  "true",
			wantClean: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir, repo := gittest.New(t.Context(), t)
			ctx := t.Context()
			gittest.Write(t, dir, "file.txt", tt.content)
			if tt.attr != "" {
				gittest.Write(t, dir, ".gitattributes", tt.attr)
			}
			if tt.tracked {
				gittest.Git(ctx, t, dir, "add", "file.txt")
				if tt.attr != "" {
					gittest.Git(ctx, t, dir, "add", ".gitattributes")
				}
				gittest.Git(ctx, t, dir, "commit", "-q", "-m", "chore: fixture")
			}
			// autocrlf is set after the index copy exists so a CRLF blob is
			// still i/crlf; setting it first would let `git add` convert.
			if tt.autocrlf != "" {
				gittest.Git(ctx, t, dir, "config", "core.autocrlf", tt.autocrlf)
			}

			got, err := repo.CleansCRLF(ctx, "file.txt")
			if err != nil {
				t.Fatalf("CleansCRLF: %v", err)
			}
			if got != tt.wantClean {
				t.Errorf("CleansCRLF = %v; want %v", got, tt.wantClean)
			}
		})
	}
}

// TestLsFilesSplitsTrackedUntrackedAndPathspec pins the three ls-files
// wrappers against a real index: empty output is an empty list (not a
// one-element slice of ""), two untracked names split on NUL, and extra
// arguments after the flags scope the listing the way `git add -- <pathspec>`
// would.
func TestLsFilesSplitsTrackedUntrackedAndPathspec(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	assertNames := func(t *testing.T, got []string, err error, want ...string) {
		t.Helper()
		if err != nil {
			t.Fatalf("ls-files: %v", err)
		}
		if got == nil {
			got = []string{}
		}
		slices.Sort(got)
		want = append([]string(nil), want...)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("names = %v; want %v", got, want)
		}
	}

	others, err := repo.LsFilesOthers(ctx)
	assertNames(t, others, err)
	tracked, err := repo.LsFilesTracked(ctx)
	assertNames(t, tracked, err)
	both, err := repo.LsFilesTrackedAndOthers(ctx, ".")
	assertNames(t, both, err)

	gittest.Write(t, dir, "keep.txt", "keep\n")
	gittest.Write(t, dir, "skip.txt", "skip\n")
	others, err = repo.LsFilesOthers(ctx)
	assertNames(t, others, err, "keep.txt", "skip.txt")
	tracked, err = repo.LsFilesTracked(ctx)
	assertNames(t, tracked, err)
	both, err = repo.LsFilesTrackedAndOthers(ctx, "keep.txt")
	assertNames(t, both, err, "keep.txt")

	gittest.Git(ctx, t, dir, "add", "keep.txt")
	gittest.Git(ctx, t, dir, "commit", "-q", "-m", "chore: keep")
	others, err = repo.LsFilesOthers(ctx)
	assertNames(t, others, err, "skip.txt")
	tracked, err = repo.LsFilesTracked(ctx)
	assertNames(t, tracked, err, "keep.txt")
	both, err = repo.LsFilesTrackedAndOthers(ctx, ".")
	assertNames(t, both, err, "keep.txt", "skip.txt")
}

// TestIgnoreCaseReportsFalseWhenTheSettingIsUnset pins git's own missing-key
// answer (exit 1), distinct from a configured false: rgit must not turn that
// into an error, because a newly-initialized Linux repository has no
// core.ignorecase at all.
func TestIgnoreCaseReportsFalseWhenTheSettingIsUnset(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.New(t.Context(), t)
	cmd := exec.CommandContext(t.Context(), "git", "-C", dir, "config", "--unset-all", "core.ignorecase") //nolint:gosec // dir is a test temp path passed as argv
	_ = cmd.Run()
	repo := gitx.New(dir)

	got, err := repo.IgnoreCase(t.Context())
	if err != nil {
		t.Fatalf("IgnoreCase: %v", err)
	}
	if got {
		t.Error("IgnoreCase = true; want false when core.ignorecase is unset")
	}
}

// TestCurrentBranchNamesMainAfterTheFirstCommit pins rev-parse --abbrev-ref
// HEAD once a commit exists: gittest forces the branch to "main", and a
// detached HEAD is the one case that would instead return the literal "HEAD".
func TestCurrentBranchNamesMainAfterTheFirstCommit(t *testing.T) {
	t.Parallel()
	_, repo := gittest.RepoWithFile(t.Context(), t, "a.txt", "a\n", "chore: initial")

	got, err := repo.CurrentBranch(t.Context())
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if got != "main" {
		t.Errorf("CurrentBranch = %q; want main", got)
	}
}

// TestBlameRevisionReadsTheNamedCommitNotTheWorktree pins that BlameRevision
// blames rev's blob: a worktree edit of the same line must not appear in the
// output when blaming HEAD.
func TestBlameRevisionReadsTheNamedCommitNotTheWorktree(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t.Context(), t, "a.txt", "committed\n", "chore: initial")
	gittest.Write(t, dir, "a.txt", "dirty\n")

	out, err := repo.BlameRevision(t.Context(), "HEAD", "a.txt", 1, 1)
	if err != nil {
		t.Fatalf("BlameRevision: %v", err)
	}
	if !strings.Contains(string(out), "committed") {
		t.Errorf("BlameRevision = %q; want HEAD's line", out)
	}
	if strings.Contains(string(out), "dirty") {
		t.Errorf("BlameRevision = %q; want the worktree edit absent", out)
	}
}

// TestRerootRunsGitAgainstTheGivenDirectory pins that Reroot copies the
// environment but replaces the root: a blob that exists only in the other
// repository must be readable through the rerooted handle.
func TestRerootRunsGitAgainstTheGivenDirectory(t *testing.T) {
	t.Parallel()
	_, repo := gittest.RepoWithFile(t.Context(), t, "here.txt", "here\n", "chore: here")
	other, _ := gittest.RepoWithFile(t.Context(), t, "there.txt", "there\n", "chore: there")

	moved := repo.Reroot(other)
	content, exists, err := moved.CatFile(t.Context(), "HEAD", "there.txt")
	if err != nil || !exists {
		t.Fatalf("CatFile(rerooted, there.txt) = (%q, %v, %v); want found", content, exists, err)
	}
	if string(content) != "there\n" {
		t.Errorf("CatFile(rerooted, there.txt) = %q; want there", content)
	}
	if _, exists, err := moved.CatFile(t.Context(), "HEAD", "here.txt"); err != nil || exists {
		t.Errorf("CatFile(rerooted, here.txt) exists=%v err=%v; want absent", exists, err)
	}
}

// TestUpdateIndexCacheinfoStagesAHashedBlob pins the last step of blob
// synthesis: a SHA produced by HashObject must be addressable from the index
// after UpdateIndexCacheinfo, without a worktree add.
func TestUpdateIndexCacheinfoStagesAHashedBlob(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()
	const rel = "staged.txt"
	sha, err := repo.HashObject(ctx, rel, []byte("blob\n"))
	if err != nil {
		t.Fatalf("HashObject: %v", err)
	}
	if err := repo.UpdateIndexCacheinfo(ctx, "100644", sha, rel); err != nil {
		t.Fatalf("UpdateIndexCacheinfo: %v", err)
	}
	content, exists, err := repo.CatFile(ctx, "", rel)
	if err != nil || !exists {
		t.Fatalf("CatFile(index, %s) = (%q, %v, %v); want found", rel, content, exists, err)
	}
	if string(content) != "blob\n" {
		t.Errorf("index blob = %q; want blob\\n", content)
	}
	if _, err := os.Stat(filepath.Join(dir, rel)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree %s: %v; want it absent -- only the index was written", rel, err)
	}
}
