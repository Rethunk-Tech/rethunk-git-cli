package synth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

// TestOpenFilePlan_UnsupportedLanguageReason pins the exit-9 message
// openFilePlan builds when no grammar matches. The old message was always
// "no grammar registered for " + ext, which for an extensionless path
// rendered as a dangling "... for " with no sign that shebang sniffing was
// even attempted -- these cases pin the replacement across an unmapped
// extension, an unmapped shebang, and no worktree copy to sniff at all.
func TestOpenFilePlan_UnsupportedLanguageReason(t *testing.T) {
	t.Parallel()

	t.Run("unmapped extension with no shebang names both", func(t *testing.T) {
		t.Parallel()
		dir, repo := gittest.New(t.Context(), t)
		gittest.Write(t, dir, "main.rb", "def main; end\n")

		_, err := openFilePlan(context.Background(), repo, dir, "main.rb")

		var perr *PathError
		if !errors.As(err, &perr) {
			t.Fatalf("openFilePlan error = %v; want *PathError", err)
		}
		want := `no grammar registered for main.rb (extension ".rb", shebang unmapped)`
		if perr.Reason != want {
			t.Errorf("Reason = %q; want %q", perr.Reason, want)
		}
	})

	t.Run("extensionless path with an unmapped shebang", func(t *testing.T) {
		t.Parallel()
		dir, repo := gittest.New(t.Context(), t)
		gittest.Write(t, dir, "bin/hook", "echo hi\n")

		_, err := openFilePlan(context.Background(), repo, dir, "bin/hook")

		var perr *PathError
		if !errors.As(err, &perr) {
			t.Fatalf("openFilePlan error = %v; want *PathError", err)
		}
		want := `no grammar registered for bin/hook (extension "", shebang unmapped)`
		if perr.Reason != want {
			t.Errorf("Reason = %q; want %q", perr.Reason, want)
		}
	})

	t.Run("extensionless path deleted from the worktree samples HEAD", func(t *testing.T) {
		t.Parallel()
		dir, repo := gittest.RepoWithFile(t.Context(), t, "bin/hook", "echo hi\n", "chore: add hook")
		if err := os.Remove(filepath.Join(dir, "bin", "hook")); err != nil {
			t.Fatal(err)
		}

		_, err := openFilePlan(context.Background(), repo, dir, "bin/hook")

		var perr *PathError
		if !errors.As(err, &perr) {
			t.Fatalf("openFilePlan error = %v; want *PathError", err)
		}
		want := `no grammar registered for bin/hook (extension "", shebang unmapped)`
		if perr.Reason != want {
			t.Errorf("Reason = %q; want %q", perr.Reason, want)
		}
	})
}

func TestOpenFilePlan_UsesNonEmptyIndexBlobWhenWorktreeGone(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	want := "package sample\n\nfunc New() {}\n"
	gittest.Write(t, dir, "new.go", want)
	gittest.Git(t.Context(), t, dir, "add", "new.go")
	if err := os.Remove(filepath.Join(dir, "new.go")); err != nil {
		t.Fatal(err)
	}

	fp, err := openFilePlan(context.Background(), repo, dir, "new.go")
	if err != nil {
		t.Fatalf("openFilePlan: %v", err)
	}
	defer fp.close()
	if !fp.workExists {
		t.Fatal("workExists = false; want index-backed source")
	}
	if string(fp.workSrc) != want {
		t.Errorf("workSrc = %q; want %q", fp.workSrc, want)
	}
	if fp.headExists {
		t.Error("headExists = true; want false for an uncommitted new file")
	}
}

func TestOpenFilePlan_RefusesEmptyIntentToAddWithoutWorktree(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	gittest.Write(t, dir, "new.go", "package sample\n\nfunc New() {}\n")
	gittest.Git(t.Context(), t, dir, "add", "-N", "new.go")
	if err := os.Remove(filepath.Join(dir, "new.go")); err != nil {
		t.Fatal(err)
	}

	_, err := openFilePlan(context.Background(), repo, dir, "new.go")
	var perr *PathError
	if !errors.As(err, &perr) {
		t.Fatalf("openFilePlan error = %v; want *PathError", err)
	}
	if !strings.Contains(perr.Reason, "empty intent-to-add blob") {
		t.Errorf("PathError.Reason = %q; want empty intent-to-add explanation", perr.Reason)
	}
}

// TestPathspecFileCounts_UnreadableUntrackedFileWarns pins the --dry-run
// counting path: an untracked file whose content cannot be read must not
// just vanish from the preview -- the numstat and ls-files failures right
// above it in pathspecFileCounts both warn instead of silently shrinking
// the total, and an unreadable untracked file is the same kind of gap.
func TestPathspecFileCounts_UnreadableUntrackedFileWarns(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := context.Background()

	full := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(full, []byte("shh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(full, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(full, 0o644) })

	files, warnings := pathspecFileCounts(ctx, repo, dir, "secret.txt")

	for _, f := range files {
		if f.path == "secret.txt" {
			t.Errorf("unreadable file %q was still counted: %+v", f.path, f)
		}
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "secret.txt") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v; want one naming secret.txt", warnings)
	}
}

func TestStage_PromisorMissingBlobIsAnError(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.BloblessClone(t.Context(), t)

	// CatFile fails before symbol classification can inspect the missing blob.
	err := stageTargets(context.Background(), repo, dir, []Target{AnchorTarget("tracked.go", "@header")})

	var gerr *gitx.GitError
	if !errors.As(err, &gerr) {
		t.Fatalf("Stage error = %v (%T); want *gitx.GitError", err, err)
	}
	if gerr.ExitCode != 128 {
		t.Errorf("Stage exit code = %d; want 128", gerr.ExitCode)
	}
	if len(gerr.Stderr) == 0 {
		t.Error("Stage GitError.Stderr is empty; want git's diagnostic")
	}
}

// TestStage_MidLoopFailureLeavesIndexUntouched pins Apply's atomicity: when
// the second file's staging fails after the first already staged, the
// caller's index must read exactly as before Apply ran -- the earlier blob
// went to a temp index that is discarded, and unrelated pre-staged work
// survives. Deleting b.go after resolution forces the failure inside Apply
// itself (resolveMode stats a worktree file the plan recorded as present),
// past the point a files-one-at-a-time staging would already have dirtied
// the index.
//
// The temp index lives on the Repo, so this is safe to run beside other
// tests that also talk to git.
func TestStage_MidLoopFailureLeavesIndexUntouched(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := context.Background()
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n")
	gittest.Write(t, dir, "b.go", "package b\n\nfunc B() int {\n\treturn 2\n}\n")
	gittest.Write(t, dir, "other.txt", "staged\n")
	gittest.Commit(t.Context(), t, dir, "chore: initial")

	// Unrelated pre-staged work the failure must preserve.
	gittest.Write(t, dir, "other.txt", "staged change\n")
	gittest.Git(t.Context(), t, dir, "add", "--", "other.txt")

	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 111\n}\n")
	gittest.Write(t, dir, "b.go", "package b\n\nfunc B() int {\n\treturn 222\n}\n")
	plan, err := PlanStage(ctx, repo, dir, []Target{AnchorTarget("a.go", "A"), AnchorTarget("b.go", "B")})
	if err != nil {
		t.Fatalf("PlanStage: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
		t.Fatal(err)
	}

	if err := plan.Apply(ctx, repo, dir); err == nil {
		t.Fatal("Apply = nil; want the deleted file's mode lookup to fail")
	}

	if got := strings.TrimSpace(gittest.Git(t.Context(), t, dir, "diff", "--cached", "--name-only")); got != "other.txt" {
		t.Errorf("staged after failed Apply = %q; want only pre-staged other.txt", got)
	}
	if got := gittest.Git(t.Context(), t, dir, "show", ":other.txt"); !strings.Contains(got, "staged change") {
		t.Errorf(":other.txt = %q; want pre-staged change preserved", got)
	}
	if got := gittest.Git(t.Context(), t, dir, "show", ":a.go"); strings.Contains(got, "return 111") {
		t.Errorf(":a.go = %q; want HEAD content, nothing of the failed stage", got)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "a.go")); err != nil { //nolint:gosec // path is inside the test temp directory
		t.Fatal(err)
	} else if !strings.Contains(string(got), "return 111") {
		t.Errorf("worktree a.go = %q; want the edit still there, untouched", got)
	}
}

// TestStage_SuccessStagesExactlyPlannedBlobs pins the success path of the
// same machinery: the planned anchor's blob lands in the index, nothing
// else moves, and the worktree is byte-identical before and after.
func TestStage_SuccessStagesExactlyPlannedBlobs(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := context.Background()
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gittest.Commit(t.Context(), t, dir, "chore: initial")
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	before, err := os.ReadFile(filepath.Join(dir, "a.go")) //nolint:gosec // path is inside the test temp directory
	if err != nil {
		t.Fatal(err)
	}

	if err := stageTargets(ctx, repo, dir, []Target{AnchorTarget("a.go", "A")}); err != nil {
		t.Fatalf("stageTargets: %v", err)
	}

	indexed := gittest.Git(t.Context(), t, dir, "show", ":a.go")
	if !strings.Contains(indexed, "return 111") {
		t.Errorf(":a.go = %q; want the synthesized A edit staged", indexed)
	}
	if !strings.Contains(indexed, "return 2") {
		t.Errorf(":a.go = %q; want B's HEAD body carried through", indexed)
	}
	if got := strings.TrimSpace(gittest.Git(t.Context(), t, dir, "status", "--porcelain")); got != "M  a.go" {
		t.Errorf("status = %q; want exactly the staged a.go edit", got)
	}
	if after, err := os.ReadFile(filepath.Join(dir, "a.go")); err != nil { //nolint:gosec // path is inside the test temp directory
		t.Fatal(err)
	} else if string(after) != string(before) {
		t.Errorf("worktree a.go changed by staging: before %q, after %q", before, after)
	}
}

// stageTargets runs synth's two steps back to back. Production always keeps
// them apart -- rgit commit resolves first so --dry-run and the exit-11
// "nothing to commit" check can decide before anything is written.
func stageTargets(ctx context.Context, repo *gitx.Repo, root string, targets []Target) error {
	plan, err := PlanStage(ctx, repo, root, targets)
	if err != nil {
		return err
	}
	return plan.Apply(ctx, repo, root)
}
