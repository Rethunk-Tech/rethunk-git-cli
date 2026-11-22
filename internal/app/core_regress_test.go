// Regression cases for the core-audit defects: each reproduces the failing
// shape through app.Run against a real git.
package app

import (
	"os"
	"path/filepath"
	"strconv"
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
	_, stderr, code := runApp(t, "-C", dir, "commit", "-m", "fix: y", "a.go:init#2")
	qt.Assert(t, qt.Equals(code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.StringContains(stderr, "ordinal moved; commit the whole file"))
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
	_, stderr, code := runApp(t, "-C", dir, "commit", "-m", "fix: q", "a.go:q")
	qt.Assert(t, qt.Equals(code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.StringContains(stderr, "shares one declaration with p; commit the whole file"))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "qualify with one of")))
	qt.Assert(t, qt.Equals(gitOut(t, dir, "rev-parse", "HEAD"), before))
}

// A staged rename shows its source path in rgit diff.
func TestRun_DiffStagedRenameNamesSource(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	gitOut(t, dir, "mv", "a.go", "b.go")
	stdout, stderr, code := runApp(t, "-C", dir, "diff")
	qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.StringContains(stdout, "b.go (renamed from a.go)"))
}

// Per-symbol rows add up to git's own totals and agree with --dry-run: a line added
// to an import list, or a method added to a class, is counted once.
func TestRun_DiffRowsMatchNumstatAndDryRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, path, head, work, anchors string }{
		{
			"import line", "a.ts", "import { a } from \"a\";\n\nexport function f(): number {\n  return 1;\n}\n",
			"import { a } from \"a\";\nimport { b } from \"b\";\n\nexport function f(): number {\n  return 1;\n}\n", "@imports",
		},
		{
			"python method", "c.py", "class C:\n    def a(self):\n        return 1\n",
			"class C:\n    def a(self):\n        return 1\n\n    def b(self):\n        return 2\n", "C.b",
		},
		{
			"go field after blank line", "d.go", "package p\n\ntype T struct {\n\tA int\n}\n",
			"package p\n\ntype T struct {\n\tA int\n\n\tN string\n}\n", "T.N",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, _ := gittest.New(t.Context(), t)
			writeAppFile(t, dir, tc.path, tc.head)
			gitOut(t, dir, "add", tc.path)
			gitOut(t, dir, "commit", "-q", "-m", "chore: init")
			writeAppFile(t, dir, tc.path, tc.work)

			diffOut, _, code := runApp(t, "-C", dir, "diff", "--porcelain")
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			var added, deleted int
			rows := map[string]string{}
			for line := range strings.SplitSeq(strings.TrimSpace(diffOut), "\n") {
				f := strings.Split(line, "\t")
				a, _ := strconv.Atoi(f[3])
				d, _ := strconv.Atoi(f[4])
				added, deleted = added+a, deleted+d
				rows[f[1]] = f[3] + "\t" + f[4]
			}
			numstat := strings.Fields(gitOut(t, dir, "diff", "--numstat", "HEAD"))
			qt.Assert(t, qt.Equals(strconv.Itoa(added)+"\t"+strconv.Itoa(deleted), numstat[0]+"\t"+numstat[1]))

			dry, _, code := runApp(t, "-C", dir, "commit", "--dry-run", "--porcelain", "-m", "x", tc.path+":"+tc.anchors)
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			f := strings.Split(strings.TrimSpace(dry), "\t")
			qt.Assert(t, qt.Equals(f[len(f)-2]+"\t"+f[len(f)-1], rows[tc.anchors]))
		})
	}
}

// For a new file, diff's rows and the commit preview give the same count to @header and the symbol.
func TestRun_NewFileRowsAgreeBetweenDiffAndDryRun(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	writeAppFile(t, dir, "n.go", "package n\n\nfunc N() int {\n\treturn 1\n}\n")
	diffOut, _, code := runApp(t, "-C", dir, "diff", "--sym", "n.go:N", "--porcelain")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	dry, _, code := runApp(t, "-C", dir, "commit", "--dry-run", "--porcelain", "-m", "feat: n", "n.go:@header", "n.go:N")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	n := strings.Split(strings.TrimSpace(diffOut), "\t")
	var got string
	for line := range strings.SplitSeq(strings.TrimSpace(dry), "\n") {
		if f := strings.Split(line, "\t"); f[1] == "N" {
			got = f[2] + "\t" + f[3]
		}
	}
	qt.Assert(t, qt.Equals(got, n[3]+"\t"+n[4]))
}
