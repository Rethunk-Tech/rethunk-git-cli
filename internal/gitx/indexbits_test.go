package gitx_test

import (
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gittest"
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
			t.Parallel()
			dir, repo := gittest.RepoWithFile(t.Context(), t, "tracked.txt", "content\n", "test: add tracked file")
			for _, mark := range test.marks {
				gittest.Git(t.Context(), t, dir, "update-index", mark, "tracked.txt")
			}

			path := "tracked.txt"
			if !test.found {
				path = "absent.txt"
			}
			skip, assume, found, err := repo.IndexWorktreeBits(t.Context(), path)
			if err != nil {
				t.Fatalf("IndexWorktreeBits: %v", err)
			}
			if skip != test.skip || assume != test.assume || found != test.found {
				t.Errorf("IndexWorktreeBits = (%t, %t, %t); want (%t, %t, %t)", skip, assume, found, test.skip, test.assume, test.found)
			}
		})
	}
}

// TestIndexQueriesTakePathsLiterally pins that a bracketed path is never
// glob-matched against a sibling: r/[id].ts must not find r/i.ts.
func TestIndexQueriesTakePathsLiterally(t *testing.T) {
	t.Parallel()
	_, repo := gittest.RepoWithFile(t.Context(), t, "r/i.ts", "x\n", "test: add i")
	if _, _, found, err := repo.IndexWorktreeBits(t.Context(), "r/[id].ts"); err != nil || found {
		t.Errorf("IndexWorktreeBits found=%t err=%v; want not found", found, err)
	}
	if _, found, err := repo.LsFilesStage(t.Context(), "r/[id].ts"); err != nil || found {
		t.Errorf("LsFilesStage found=%t err=%v; want not found", found, err)
	}
	if bad, err := repo.IsUnmerged(t.Context(), "r/[id].ts"); err != nil || bad {
		t.Errorf("IsUnmerged = %t, %v; want false", bad, err)
	}
}
