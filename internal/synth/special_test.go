package synth

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

// newSpecialTestRepo is a self-contained temp repo (gitx_test.go's own
// style): this is white-box package synth, testing classifyPath's
// unexported branches directly rather than through Stage's public surface,
// which index_test.go's TestStage_SubmoduleAndSymlinkPathStaging already
// covers for the worktree-present half of classifyPath.
func newSpecialTestRepo(t *testing.T) (dir string, repo *gitx.Repo) {
	t.Helper()
	return gittest.New(t)
}

func commitSpecial(t *testing.T, dir string, paths ...string) {
	t.Helper()
	args := append([]string{"add"}, paths...)
	gittest.Git(t, dir, args...)
	gittest.Git(t, dir, "commit", "-q", "-m", "chore: commit special path")
}

// TestClassifyPath_HeadOnlyBranches covers classifyPath's HEAD-tree fallback
// (os.Lstat finds nothing, so it falls back to LsTreeTolerant) -- the branch
// docs/ANCHORS.md's "staging a symbol deletion from a deleted file" case
// reaches, and index_test.go's worktree-present cases never touch since
// their special paths still exist on disk. Each subtest commits a special
// path, then removes it from the worktree only, leaving HEAD's tree entry
// (the classification source) intact.
func TestClassifyPath_HeadOnlyBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("symlink deleted from the worktree classifies via HEAD's 120000 entry", func(t *testing.T) {
		dir, repo := newSpecialTestRepo(t)
		if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("target.txt", filepath.Join(dir, "link.txt")); err != nil {
			t.Fatal(err)
		}
		commitSpecial(t, dir, "target.txt", "link.txt")

		if err := os.Remove(filepath.Join(dir, "link.txt")); err != nil {
			t.Fatal(err)
		}

		kind, err := classifyPath(ctx, repo, dir, "link.txt")
		if err != nil {
			t.Fatalf("classifyPath: %v", err)
		}
		if kind != pathSymlink {
			t.Errorf("kind = %v; want pathSymlink", kind)
		}
	})

	t.Run("submodule directory removed from the worktree classifies via HEAD's 160000 entry", func(t *testing.T) {
		dir, repo := newSpecialTestRepo(t)
		subDir := filepath.Join(dir, "sub")
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatal(err)
		}
		gittest.Git(t, subDir, "init", "-q")
		gittest.Git(t, subDir, "config", "user.email", "sub@example.com")
		gittest.Git(t, subDir, "config", "user.name", "Sub")
		if err := os.WriteFile(filepath.Join(subDir, "x.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gittest.Git(t, subDir, "add", "x.txt")
		gittest.Git(t, subDir, "commit", "-q", "-m", "chore: sub commit")

		commitSpecial(t, dir, "sub")

		if err := os.RemoveAll(subDir); err != nil {
			t.Fatal(err)
		}

		kind, err := classifyPath(ctx, repo, dir, "sub")
		if err != nil {
			t.Fatalf("classifyPath: %v", err)
		}
		if kind != pathGitlink {
			t.Errorf("kind = %v; want pathGitlink", kind)
		}
	})

	t.Run("binary file deleted from the worktree classifies via HEAD's content", func(t *testing.T) {
		dir, repo := newSpecialTestRepo(t)
		if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("a\x00b\x00c"), 0o644); err != nil {
			t.Fatal(err)
		}
		commitSpecial(t, dir, "blob.bin")

		if err := os.Remove(filepath.Join(dir, "blob.bin")); err != nil {
			t.Fatal(err)
		}

		kind, err := classifyPath(ctx, repo, dir, "blob.bin")
		if err != nil {
			t.Fatalf("classifyPath: %v", err)
		}
		if kind != pathBinary {
			t.Errorf("kind = %v; want pathBinary", kind)
		}
	})

	t.Run("regular file deleted from the worktree classifies as pathRegular via HEAD", func(t *testing.T) {
		dir, repo := newSpecialTestRepo(t)
		if err := os.WriteFile(filepath.Join(dir, "plain.go"), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		commitSpecial(t, dir, "plain.go")

		if err := os.Remove(filepath.Join(dir, "plain.go")); err != nil {
			t.Fatal(err)
		}

		kind, err := classifyPath(ctx, repo, dir, "plain.go")
		if err != nil {
			t.Fatalf("classifyPath: %v", err)
		}
		if kind != pathRegular {
			t.Errorf("kind = %v; want pathRegular", kind)
		}
	})

	t.Run("path present on neither side classifies as pathRegular", func(t *testing.T) {
		dir, repo := newSpecialTestRepo(t)
		// An empty repo: the path was never committed and never existed in
		// the worktree either. classifyPath's job is refusing an
		// addressable-but-wrong-kind path, not diagnosing absence -- that is
		// resolve.Resolve's job, with its own exit code.
		kind, err := classifyPath(ctx, repo, dir, "never.go")
		if err != nil {
			t.Fatalf("classifyPath: %v", err)
		}
		if kind != pathRegular {
			t.Errorf("kind = %v; want pathRegular", kind)
		}
	})
}
