package synth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

// The fixtures below are self-contained temp repos (gitx_test.go's own
// style): this is white-box package synth, testing classifyPath's
// unexported branches directly rather than through Stage's public surface,
// which index_test.go's TestStage_SubmoduleAndSymlinkPathStaging already
// covers for the worktree-present half of classifyPath.

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
// their special paths still exist on disk. Each of the first four subtests
// commits a special path, then removes it from the worktree only, leaving
// HEAD's tree entry (the classification source) intact.
//
// The last two subtests are the opposite side, folded in here rather than a
// second test function: classifyWorktreeEntry's own two thin branches --
// a plain (non-submodule) directory, and a binary file the worktree copy
// itself still has -- which index_test.go's
// TestStage_SubmoduleAndSymlinkPathStaging leaves uncovered since it only
// anchors the symlink case for its worktree-present half.
func TestClassifyPath_HeadOnlyBranches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("symlink deleted from the worktree classifies via HEAD's 120000 entry", func(t *testing.T) {
		dir, repo := gittest.New(t)
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
		dir, repo := gittest.New(t)
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

	t.Run("uninitialized submodule directory classifies via HEAD's 160000 entry, not pathRegular", func(t *testing.T) {
		// "git submodule deinit" leaves the directory itself in the
		// worktree, emptied of its own ".git" -- unlike the case above,
		// where the whole directory is gone. classifyWorktreeEntry's own
		// ".git present" heuristic cannot tell this apart from an ordinary
		// directory by local shape alone; classifyPath must still cross-
		// check HEAD's own tree mode rather than settling for pathRegular.
		dir, repo := gittest.New(t)
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

		// Deinit-shaped: the directory survives, empty, with no ".git".
		if err := os.RemoveAll(filepath.Join(subDir, ".git")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(subDir, "x.txt")); err != nil {
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
		dir, repo := gittest.New(t)
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
		dir, repo := gittest.New(t)
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

	t.Run("plain directory in the worktree classifies as pathRegular, not a submodule", func(t *testing.T) {
		dir, repo := gittest.New(t)
		if err := os.MkdirAll(filepath.Join(dir, "plaindir"), 0o755); err != nil {
			t.Fatal(err)
		}

		kind, err := classifyPath(ctx, repo, dir, "plaindir")
		if err != nil {
			t.Fatalf("classifyPath: %v", err)
		}
		if kind != pathRegular {
			t.Errorf("kind = %v; want pathRegular", kind)
		}
	})

	t.Run("binary file present in the worktree refuses a symbol anchor", func(t *testing.T) {
		dir, repo := gittest.New(t)
		if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("a\x00b\x00c"), 0o644); err != nil {
			t.Fatal(err)
		}

		// openFilePlan, not classifyPath directly: this is the branch
		// TestStage_SubmoduleAndSymlinkPathStaging (index_test.go) leaves
		// untouched, since it only anchors the symlink there -- driving
		// openFilePlan exercises classifyWorktreeEntry's own binary check
		// (Lstat succeeds; the worktree copy is what gets sniffed, an
		// untracked file included) and refusalFor's pathBinary case in the
		// same call, the way a real anchor target actually reaches both.
		_, err := openFilePlan(ctx, repo, dir, "blob.bin")
		pathErr, ok := err.(*PathError)
		if !ok {
			t.Fatalf("openFilePlan error = %v (%T); want *PathError", err, err)
		}
		if pathErr.Code != exitcode.SpecialPathRefused {
			t.Errorf("Code = %v; want SpecialPathRefused", pathErr.Code)
		}
	})

	t.Run("path present on neither side classifies as pathRegular", func(t *testing.T) {
		dir, repo := gittest.New(t)
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

func TestStage_RefusesUnmergedSymbol(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "conflict.go", "package p\n\nfunc Keep() {}\n")
	gittest.Commit(t, dir, "chore: add conflict fixture")

	blob := strings.TrimSpace(gittest.Git(t, dir, "rev-parse", "HEAD:conflict.go"))
	gittest.Unmerged(t, dir, blob, "conflict.go")

	err := stageTargets(context.Background(), repo, dir, []Target{AnchorTarget("conflict.go", "Keep")})
	var pathErr *PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("Stage error = %v (%T); want *PathError", err, err)
	}
	if pathErr.Code != exitcode.SpecialPathRefused {
		t.Errorf("Code = %v; want SpecialPathRefused", pathErr.Code)
	}
	if pathErr.Reason != "unmerged; name the path instead of a symbol" {
		t.Errorf("Reason = %q; want unmerged refusal", pathErr.Reason)
	}
	if unmerged, err := repo.IsUnmerged(context.Background(), "conflict.go"); err != nil {
		t.Fatalf("IsUnmerged after refusal: %v", err)
	} else if !unmerged {
		t.Error("IsUnmerged after refusal = false; want true")
	}
}

func TestStage_RefusesIndexWorktreeBits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mark   string
		reason string
		skip   bool
		assume bool
	}{
		{
			name:   "skip-worktree",
			mark:   "--skip-worktree",
			reason: "skip-worktree; name the path instead of a symbol",
			skip:   true,
		},
		{
			name:   "assume-unchanged",
			mark:   "--assume-unchanged",
			reason: "assume-unchanged; name the path instead of a symbol",
			assume: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir, repo := gittest.New(t)
			gittest.Write(t, dir, "tracked.go", "package p\n\nfunc Keep() {}\n")
			commitSpecial(t, dir, "tracked.go")
			gittest.Git(t, dir, "update-index", test.mark, "tracked.go")

			err := stageTargets(context.Background(), repo, dir, []Target{AnchorTarget("tracked.go", "Keep")})
			var pathErr *PathError
			if !errors.As(err, &pathErr) {
				t.Fatalf("Stage error = %v (%T); want *PathError", err, err)
			}
			if pathErr.Code != exitcode.SpecialPathRefused {
				t.Errorf("Code = %v; want SpecialPathRefused", pathErr.Code)
			}
			if pathErr.Reason != test.reason {
				t.Errorf("Reason = %q; want %q", pathErr.Reason, test.reason)
			}

			skip, assume, found, err := repo.IndexWorktreeBits(context.Background(), "tracked.go")
			if err != nil {
				t.Fatalf("IndexWorktreeBits after refusal: %v", err)
			}
			if skip != test.skip || assume != test.assume || !found {
				t.Errorf("IndexWorktreeBits after refusal = (%t, %t, %t); want (%t, %t, true)", skip, assume, found, test.skip, test.assume)
			}
		})
	}
}

func TestCheckGitignoreRefusal_IndexEntryCountsAsTracked(t *testing.T) {
	t.Parallel()

	t.Run("intent-to-add entry is allowed", func(t *testing.T) {
		dir, repo := gittest.New(t)
		gittest.Write(t, dir, ".gitignore", "skip-me.go\n")
		gittest.Write(t, dir, "skip-me.go", "package p\n")
		commitSpecial(t, dir, ".gitignore")
		gittest.Git(t, dir, "add", "-f", "-N", "skip-me.go")

		if err := checkGitignoreRefusal(context.Background(), repo, "skip-me.go"); err != nil {
			t.Fatalf("checkGitignoreRefusal: %v; want nil", err)
		}
	})

	t.Run("folded index name is allowed", func(t *testing.T) {
		dir, repo := gittest.New(t)
		gittest.Git(t, dir, "config", "core.ignorecase", "true")
		gittest.Write(t, dir, ".gitignore", "skip-me.go\n")
		gittest.Write(t, dir, "Skip-Me.go", "package p\n")
		commitSpecial(t, dir, ".gitignore")
		gittest.Git(t, dir, "add", "-f", "-N", "Skip-Me.go")

		if err := checkGitignoreRefusal(context.Background(), repo, "skip-me.go"); err != nil {
			t.Fatalf("checkGitignoreRefusal: %v; want nil", err)
		}
	})

	t.Run("never-indexed entry is refused", func(t *testing.T) {
		dir, repo := gittest.New(t)
		gittest.Write(t, dir, ".gitignore", "skip-me.go\n")
		gittest.Write(t, dir, "skip-me.go", "package p\n")
		commitSpecial(t, dir, ".gitignore")

		err := checkGitignoreRefusal(context.Background(), repo, "skip-me.go")
		var pathErr *PathError
		if !errors.As(err, &pathErr) {
			t.Fatalf("checkGitignoreRefusal error = %v (%T); want *PathError", err, err)
		}
		if pathErr.Code != exitcode.PathRefused {
			t.Errorf("Code = %v; want PathRefused", pathErr.Code)
		}
		if pathErr.Reason != "gitignored and untracked" {
			t.Errorf("Reason = %q; want gitignore refusal", pathErr.Reason)
		}
	})
}
