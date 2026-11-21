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
	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gittest"
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

// An ordinal anchor refuses when the count of same-named functions changed, since
// it would pair HEAD's init#2 with a different function.
func TestRun_OrdinalRefusedWhenSameNameCountChanged(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.New(t.Context(), t)
	writeAppFile(t, dir, "a.go", "package a\n\nvar x, y int\n\nfunc init() {\n\tx = 1\n}\n\nfunc init() {\n\ty = 2\n}\n")
	gitOut(t, dir, "add", "a.go")
	gitOut(t, dir, "commit", "-q", "-m", "chore: init")
	writeAppFile(t, dir, "a.go", "package a\n\nvar x, y int\n\nfunc init() {\n\tprintln(\"new\")\n}\n\nfunc init() {\n\tx = 1\n}\n\nfunc init() {\n\ty = 20\n}\n")
	before := gitOut(t, dir, "rev-parse", "HEAD")
	_, _, code := runApp(t, "-C", dir, "commit", "-m", "fix: y", "a.go:init#2")
	qt.Assert(t, qt.Equals(code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "rev-parse", "HEAD"), before))
}

// A member inserted after a blank line in an indented container keeps that blank line.
func TestRun_CommitIndentedNewMemberKeepsLeadingBlankLine(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.New(t.Context(), t)
	writeAppFile(t, dir, "a.go", "package a\n\ntype T struct {\n\tA int\n}\n")
	gitOut(t, dir, "add", "a.go")
	gitOut(t, dir, "commit", "-q", "-m", "chore: init")
	want := "package a\n\ntype T struct {\n\tA int\n\n\tN string\n}\n"
	writeAppFile(t, dir, "a.go", want)
	_, stderr, code := runApp(t, "-C", dir, "commit", "-m", "feat: n", "a.go:T.N")
	qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "show", "HEAD:a.go"), want))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "status", "--porcelain"), ""))
}

// With eol=crlf a symbol that did not change exits 11, not git's 128.
func TestRun_CRLFUnchangedSymbolExitsNothingToCommit(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.New(t.Context(), t)
	writeAppFile(t, dir, ".gitattributes", "*.go text eol=crlf\n")
	writeAppFile(t, dir, "a.go", "package a\r\n\r\nfunc A() int {\r\n\treturn 1\r\n}\r\n\r\nfunc B() int {\r\n\treturn 2\r\n}\r\n")
	gitOut(t, dir, "add", ".gitattributes", "a.go")
	gitOut(t, dir, "commit", "-q", "-m", "chore: init")
	_, _, code := runApp(t, "-C", dir, "commit", "-m", "fix: b", "a.go:B")
	qt.Assert(t, qt.Equals(code, exitcode.NothingToCommit))
}

// A name in a multi-name Go var spec shares one extent with its siblings, so
// committing it would commit the sibling's change; it is refused.
func TestRun_CommitMultiNameVarSpecRefused(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.New(t.Context(), t)
	writeAppFile(t, dir, "a.go", "package a\n\nvar p, q = 1, 2\n")
	gitOut(t, dir, "add", "a.go")
	gitOut(t, dir, "commit", "-q", "-m", "chore: init")
	writeAppFile(t, dir, "a.go", "package a\n\nvar p, q = 10, 2\n")
	before := gitOut(t, dir, "rev-parse", "HEAD")
	_, _, code := runApp(t, "-C", dir, "commit", "-m", "fix: q", "a.go:q")
	qt.Assert(t, qt.Equals(code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "rev-parse", "HEAD"), before))
}
