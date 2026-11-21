package gitx_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gittest"
)

func TestHasStash(t *testing.T) {
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	hasStash, err := repo.HasStash(ctx)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsFalse(hasStash))

	path := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t.Context(), t, dir, "add", "tracked.txt")
	gittest.Git(t.Context(), t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(path, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t.Context(), t, dir, "stash", "push", "-m", "test")

	hasStash, err = repo.HasStash(ctx)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(hasStash))

	gittest.Git(t.Context(), t, dir, "stash", "drop")
	hasStash, err = repo.HasStash(ctx)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsFalse(hasStash))
}

func TestSparseCheckout(t *testing.T) {
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	sparse, err := repo.SparseCheckout(ctx)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsFalse(sparse))

	gittest.Git(t.Context(), t, dir, "config", "core.sparseCheckout", "true")
	sparse, err = repo.SparseCheckout(ctx)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(sparse))

	gittest.Git(t.Context(), t, dir, "config", "--unset", "core.sparseCheckout")
	sparse, err = repo.SparseCheckout(ctx)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsFalse(sparse))
}

// TestSparseCheckoutRejectsANonBooleanValue pins git's own refusal of a
// core.sparseCheckout that is neither true nor false: rgit must surface that
// as an error rather than treating it as "sparse checkout is off".
func TestSparseCheckoutRejectsANonBooleanValue(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	gittest.Git(t.Context(), t, dir, "config", "core.sparseCheckout", "maybe")

	_, err := repo.SparseCheckout(t.Context())
	if err == nil {
		t.Fatal("SparseCheckout = nil; want an error for a non-boolean value")
	}
}

// TestSparseCheckoutPropagatesACancelledContext pins the run-failure path:
// git never starts, so the answer is the context error, not "sparse checkout
// is off".
func TestSparseCheckoutPropagatesACancelledContext(t *testing.T) {
	t.Parallel()
	_, repo := gittest.New(t.Context(), t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := repo.SparseCheckout(ctx)
	if err == nil {
		t.Fatal("SparseCheckout(cancelled) = nil; want the cancelled context")
	}
}
