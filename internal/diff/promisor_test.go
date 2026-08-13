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
	dir, repo := gittest.BloblessClone(t)

	_, err := Run(context.Background(), repo, dir, Options{
		Files: []string{"tracked.go"},
	})

	var gerr *gitx.GitError
	if !errors.As(err, &gerr) {
		t.Fatalf("Run error = %v (%T); want *gitx.GitError", err, err)
	}
	if gerr.ExitCode != 128 {
		t.Errorf("Run exit code = %d; want 128", gerr.ExitCode)
	}
}
