package synth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gittest"
)

// TestCallerIndexPath_HonoursGitIndexFile pins which file staging copies and
// swaps: git's own GIT_INDEX_FILE when the caller set one, resolved against
// the repository root when it is relative, and <gitdir>/index otherwise. The
// fallback file is the one git itself names, so a linked worktree or a
// GIT_DIR override does not silently stage into a copy nothing reads.
//
// No t.Parallel: t.Setenv forbids it (CONTRIBUTING.md § Tests).
func TestCallerIndexPath_HonoursGitIndexFile(t *testing.T) {
	dir, _ := gittest.New(t.Context(), t)
	ctx := t.Context()

	defaultPath, err := callerIndexPath(ctx, dir)
	if err != nil {
		t.Fatalf("callerIndexPath(default): %v", err)
	}
	gitDir := gittest.Git(ctx, t, dir, "rev-parse", "--absolute-git-dir")
	if want := filepath.Join(trimNewline(gitDir), "index"); defaultPath != want {
		t.Errorf("callerIndexPath(default) = %q; want %q", defaultPath, want)
	}

	alternative := filepath.Join(t.TempDir(), "alt-index")
	t.Setenv("GIT_INDEX_FILE", alternative)
	if got, err := callerIndexPath(ctx, dir); err != nil {
		t.Fatalf("callerIndexPath(absolute): %v", err)
	} else if got != alternative {
		t.Errorf("callerIndexPath(absolute) = %q; want %q", got, alternative)
	}

	t.Setenv("GIT_INDEX_FILE", "relative-index")
	if got, err := callerIndexPath(ctx, dir); err != nil {
		t.Fatalf("callerIndexPath(relative): %v", err)
	} else if want := filepath.Join(dir, "relative-index"); got != want {
		t.Errorf("callerIndexPath(relative) = %q; want %q (resolved against the root)", got, want)
	}

	// An empty value is not a caller-chosen index: git treats it as unset.
	t.Setenv("GIT_INDEX_FILE", "")
	if got, err := callerIndexPath(ctx, dir); err != nil {
		t.Fatalf("callerIndexPath(empty): %v", err)
	} else if got != defaultPath {
		t.Errorf("callerIndexPath(empty) = %q; want the default %q", got, defaultPath)
	}

	if _, err := os.Stat(filepath.Dir(defaultPath)); err != nil {
		t.Fatalf("git directory missing: %v", err)
	}
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
