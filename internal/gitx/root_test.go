package gitx_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gitx"
)

// TestRootAndPrefix pins both halves of the answer every rgit command asks
// for first: the worktree root, and the invocation directory's path relative
// to it. git prints one line per rev, so the empty prefix at the root has to
// survive as an empty field rather than being trimmed away with the newline.
func TestRootAndPrefix(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.RepoWithFile(t.Context(), t, "a.go", "package a\n", "chore: initial")

	root, prefix, err := gitx.New(dir).RootAndPrefix(t.Context())
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(root, dir))
	qt.Assert(t, qt.Equals(prefix, ""))

	nested := filepath.Join(dir, "pkg", "deep")
	qt.Assert(t, qt.IsNil(os.MkdirAll(nested, 0o750)))
	root, prefix, err = gitx.New(nested).RootAndPrefix(t.Context())
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(root, dir))
	qt.Assert(t, qt.Equals(prefix, "pkg/deep/"))

	// Outside a repository both probes fail, and the pair reports git's
	// failure rather than a zero-value root.
	_, _, err = gitx.New(t.TempDir()).RootAndPrefix(t.Context())
	qt.Assert(t, qt.IsNotNil(err))
}

// TestRootAndPrefixCostsOneGitProcess pins why the two are answered together:
// rgit asks at the start of every command, so the pair has to cost one git
// process rather than one per question.
func TestRootAndPrefixCostsOneGitProcess(t *testing.T) {
	dir, _ := gittest.RepoWithFile(t.Context(), t, "a.go", "package a\n", "chore: initial")
	bin, countFile := countingGit(t)
	t.Setenv("PATH", bin)

	_, _, err := gitx.New(dir).RootAndPrefix(t.Context())
	qt.Assert(t, qt.IsNil(err))

	info, err := os.Stat(countFile)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(info.Size(), int64(1)))
}

// countingGit puts a shim named git on PATH that appends one byte to a count
// file per invocation and then execs the real git. PATH is replaced rather
// than prepended so nothing else on it can satisfy a lookup, and the shim
// carries git's absolute path because it no longer resolves through PATH.
func countingGit(t *testing.T) (bin, countFile string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh not on PATH: %v", err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	bin = t.TempDir()
	countFile = filepath.Join(bin, "count")
	script := "#!/bin/sh\nprintf x >> '" + countFile + "'\nexec '" + gitPath + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil { //nolint:gosec // executable git shim is the behavior under test
		t.Fatal(err)
	}
	return bin, countFile
}
