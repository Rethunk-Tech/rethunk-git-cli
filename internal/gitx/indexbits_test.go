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
		marks  []string
		skip   bool
		assume bool
		found  bool
	}{
		{name: "normal", found: true},
		{name: "skip-worktree", marks: []string{"--skip-worktree"}, skip: true, found: true},
		{name: "assume-unchanged", marks: []string{"--assume-unchanged"}, assume: true, found: true},
		{name: "skip-worktree-and-assume-unchanged", marks: []string{"--skip-worktree", "--assume-unchanged"}, skip: true, assume: true, found: true},
		{name: "absent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir, repo := gittest.RepoWithFile(t, "tracked.txt", "content\n", "test: add tracked file")
			for _, mark := range test.marks {
				gittest.Git(t, dir, "update-index", mark, "tracked.txt")
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
