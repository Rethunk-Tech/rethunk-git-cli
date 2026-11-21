// Regression cases for the core-audit defects: each reproduces the failing
// shape through app.Run against a real git.
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/exitcode"
)

const twoEdits = "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 222\n}\n"

// --stage-only twice in one file keeps the first symbol staged.
func TestRun_StageOnlyKeepsEarlierStagedSymbolInSameFile(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	writeAppFile(t, dir, "a.go", twoEdits)
	for _, sym := range []string{"a.go:A", "a.go:B"} {
		_, stderr, code := runApp(t, "-C", dir, "commit", "--stage-only", sym)
		qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("stderr: %s", stderr))
	}
	staged := gitOut(t, dir, "show", ":a.go")
	qt.Assert(t, qt.Equals(staged, twoEdits))
}

// -i takes work already staged in the same file along with the named symbol.
func TestRun_IncludeKeepsStagedSymbolInSameFile(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	writeAppFile(t, dir, "a.go", twoEdits)
	_, stderr, code := runApp(t, "-C", dir, "commit", "--stage-only", "a.go:B")
	qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("stderr: %s", stderr))
	_, stderr, code = runApp(t, "-C", dir, "commit", "-i", "-m", "feat(a): both", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "show", "HEAD:a.go"), twoEdits))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "status", "--porcelain"), ""))
}

// Naming every symbol of a deleted file deletes it instead of committing an empty file.
func TestRun_CommitAllSymbolsOfDeletedFileDeletesIt(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	qt.Assert(t, qt.IsNil(os.Remove(filepath.Join(dir, "a.go"))))
	_, stderr, code := runApp(t, "-C", dir, "commit", "-m", "chore: drop a", "a.go:@header", "a.go:A", "a.go:B")
	qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(strings.TrimSpace(gitOut(t, dir, "ls-tree", "-r", "--name-only", "HEAD")), ""))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "status", "--porcelain"), ""))
}
