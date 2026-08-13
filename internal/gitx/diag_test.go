package gitx_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

func TestHasStash(t *testing.T) {
	dir, repo := gittest.New(t)
	ctx := context.Background()

	hasStash, err := repo.HasStash(ctx)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsFalse(hasStash))

	path := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDiagGit(t, dir, "add", "tracked.txt")
	runDiagGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDiagGit(t, dir, "stash", "push", "-m", "test")

	hasStash, err = repo.HasStash(ctx)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(hasStash))

	runDiagGit(t, dir, "stash", "drop")
	hasStash, err = repo.HasStash(ctx)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsFalse(hasStash))
}

func TestSparseCheckout(t *testing.T) {
	dir, repo := gittest.New(t)
	ctx := context.Background()

	sparse, err := repo.SparseCheckout(ctx)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsFalse(sparse))

	runDiagGit(t, dir, "config", "core.sparseCheckout", "true")
	sparse, err = repo.SparseCheckout(ctx)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(sparse))

	runDiagGit(t, dir, "config", "--unset", "core.sparseCheckout")
	sparse, err = repo.SparseCheckout(ctx)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsFalse(sparse))
}

func runDiagGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
