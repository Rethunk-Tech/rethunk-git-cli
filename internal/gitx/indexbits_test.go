package gitx_test

import (
	"context"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

func TestIndexWorktreeBits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mark   string
		skip   bool
		assume bool
		found  bool
	}{
		{name: "normal", found: true},
		{name: "skip-worktree", mark: "--skip-worktree", skip: true, found: true},
		{name: "assume-unchanged", mark: "--assume-unchanged", assume: true, found: true},
		{name: "absent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir, repo := gittest.New(t)
			gittest.Write(t, dir, "tracked.txt", "content\n")
			gittest.Commit(t, dir, "test: add tracked file")
			if test.mark != "" {
				gittest.Git(t, dir, "update-index", test.mark, "tracked.txt")
			}

			path := "tracked.txt"
			if !test.found {
				path = "absent.txt"
			}
			skip, assume, found, err := repo.IndexWorktreeBits(context.Background(), path)
			if err != nil {
				t.Fatalf("IndexWorktreeBits: %v", err)
			}
			if skip != test.skip || assume != test.assume || found != test.found {
				t.Errorf("IndexWorktreeBits = (%t, %t, %t); want (%t, %t, %t)", skip, assume, found, test.skip, test.assume, test.found)
			}
		})
	}
}
