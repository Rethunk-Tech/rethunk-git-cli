package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This package changes the process working directory via t.Chdir. That is
// process-global, so a test here that also calls t.Parallel() lets one test's
// directory move under another, which surfaces as a commit finding no worktree
// changes rather than as an obvious failure.
func TestNoParallelTestsInThisPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, "_test.go") || name == filepath.Base("parallel_chdir_guard_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(src), "t.Parallel()") {
			t.Errorf("%s calls t.Parallel(); this package uses t.Chdir, which is process-global", name)
		}
	}
}
