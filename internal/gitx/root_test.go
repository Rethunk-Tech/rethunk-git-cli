package gitx_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gitx"
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
	bin, countFile := gittest.CountingGit(t, "")
	t.Setenv("PATH", bin)

	_, _, err := gitx.New(dir).RootAndPrefix(t.Context())
	qt.Assert(t, qt.IsNil(err))

	info, err := os.Stat(countFile)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(info.Size(), int64(1)))
}
