package diff

import (
	"context"
	"errors"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

func TestRun_PromisorMissingBlobIsAnError(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.BloblessClone(t.Context(), t)

	report, err := Run(context.Background(), repo, dir, Options{
		Files: []string{"tracked.go"},
	})

	if report != nil {
		t.Fatalf("Run report = %#v; want nil on error", report)
	}
	var gerr *gitx.GitError
	if !errors.As(err, &gerr) {
		t.Fatalf("Run error = %v (%T); want *gitx.GitError", err, err)
	}
	if gerr.ExitCode != 128 {
		t.Errorf("Run exit code = %d; want 128", gerr.ExitCode)
	}
	if len(gerr.Stderr) == 0 {
		t.Error("Run GitError.Stderr is empty; want git's diagnostic")
	}
}

func TestPrefetchBlobs_PromisorMissingBlobIsAnError(t *testing.T) {
	t.Parallel()
	_, repo := gittest.BloblessClone(t.Context(), t)

	cache, err := prefetchBlobs(
		context.Background(),
		repo,
		Scope{Old: revSide("HEAD"), New: worktreeSide()},
		[]string{"tracked.go"},
		[]string{"tracked.go"},
	)

	if cache != nil {
		t.Fatalf("prefetchBlobs cache = %#v; want nil on error", cache)
	}
	var gerr *gitx.GitError
	if !errors.As(err, &gerr) {
		t.Fatalf("prefetchBlobs error = %v (%T); want *gitx.GitError", err, err)
	}
	if gerr.ExitCode != 128 {
		t.Errorf("prefetchBlobs exit code = %d; want 128", gerr.ExitCode)
	}
	if len(gerr.Stderr) == 0 {
		t.Error("prefetchBlobs GitError.Stderr is empty; want git's diagnostic")
	}
}
