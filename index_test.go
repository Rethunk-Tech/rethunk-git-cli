// Staging coverage for internal/synth, per CONTRIBUTING.md's three-file
// test budget. Every case runs against a real temporary git repository --
// no gitx mocking -- because these assertions are about git's own
// behaviour (rename detection, mode changes, clean filters) and a mock
// cannot be wrong in the same way git is right. Byte-exact expectations
// were pinned by running the fixtures and reading back real output
// (fixture-first discipline, CONTRIBUTING.md § Tests).
package main

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/synth"
)

// newSynthRepo is newTempRepo plus a committer identity -- unlike the
// cases in rgit_e2e_test.go, these actually call `git commit`.
func newSynthRepo(t *testing.T) (dir string, repo *gitx.Repo) {
	t.Helper()
	dir = newTempRepo(t)
	gitIn(t, dir, "config", "user.email", "synth-test@example.com")
	gitIn(t, dir, "config", "user.name", "Synth Test")
	return dir, gitx.New(dir)
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", msg)
}

// assertPathError pins both halves of a refusal: that staging failed with a
// *synth.PathError at all, and that it carries the exit code docs/USAGE.md
// assigns that refusal. Asserting only the code would pass for any error
// type that happened to wrap one.
func assertPathError(t *testing.T, err error, want exitcode.Code) {
	t.Helper()
	qt.Assert(t, qt.IsNotNil(err))
	var perr *synth.PathError
	qt.Assert(t, qt.ErrorAs(err, &perr))
	qt.Assert(t, qt.Equals(perr.Code, want))
}

// indexBlob reads a path's staged (index) content -- `git cat-file -p
// :path`, the same ":path" form gitx.CatFile already produces when rev is
// empty.
func indexBlob(t *testing.T, repo *gitx.Repo, path string) string {
	t.Helper()
	content, exists, err := repo.CatFile(context.Background(), "", path)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(exists))
	return string(content)
}

func mustStage(t *testing.T, repo *gitx.Repo, dir string, targets ...synth.Target) {
	t.Helper()
	err := synth.Stage(context.Background(), repo, dir, targets)
	qt.Assert(t, qt.IsNil(err))
}

// mustParseGo fails if src is not valid Go. Every synthesized blob must
// re-parse with no ERROR nodes, and this is the cheap check that catches
// a splice landing at the wrong offset: a misplaced extent
// usually produces syntactically broken output rather than subtly wrong
// output. go/parser is a stricter oracle than tree-sitter here, which is
// error-tolerant by design and would report a damaged tree rather than
// refuse it.
func mustParseGo(t *testing.T, label, src string) {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), "synthesized.go", src, parser.ParseComments); err != nil {
		t.Fatalf("%s: synthesized blob does not parse: %v\n%s", label, err, src)
	}
}

func TestStage_SingleSymbolSynthesizedIntoRealIndex(t *testing.T) {
	dir, repo := newSynthRepo(t)
	head := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 1\n}\n\n// B returns two.\nfunc B() int {\n\treturn 2\n}\n"
	writeFile(t, dir, "greet.go", head)
	commitAll(t, dir, "chore: initial greet.go")

	// Both symbols change in the worktree, but only A is named -- the
	// litmus test for symbol-granular staging.
	work := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 100\n}\n\n// B returns two.\nfunc B() int {\n\treturn 200\n}\n"
	writeFile(t, dir, "greet.go", work)

	mustStage(t, repo, dir, synth.AnchorTarget("greet.go", "A"))

	want := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 100\n}\n\n// B returns two.\nfunc B() int {\n\treturn 2\n}\n"
	qt.Assert(t, qt.Equals(indexBlob(t, repo, "greet.go"), want))

	// The worktree file itself is never touched by staging.
	onDisk, err := os.ReadFile(filepath.Join(dir, "greet.go"))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(string(onDisk), work))

	// B's own change is still outstanding, unstaged.
	unstaged := gitIn(t, dir, "diff", "--numstat")
	qt.Assert(t, qt.StringContains(unstaged, "greet.go"))
}

func TestStage_OverlappingAnchorsCoalesceIntoOneExtent(t *testing.T) {
	// Pinned defect: two targets whose extents cover the same bytes were
	// spliced independently. Every op addresses HEAD's original offsets and
	// the pass runs in descending start order, so the inner splice shifted
	// the bytes the outer one's end still pointed at; the outer splice then
	// landed mid-token and staged "return 100\n}00\n}" -- a blob that does
	// not parse, committed at exit 0 with nothing on stderr.
	//
	// docs/ANCHORS.md requires overlapping or nested anchors to merge into a
	// single contiguous extent. The enclosing extent is already that merged
	// result: its replacement text is its own worktree content, which
	// contains the nested anchor in its new form.
	head := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 1\n}\n\n// B returns two.\nfunc B() int {\n\treturn 2\n}\n"

	t.Run("pseudo-anchor encloses a named symbol", func(t *testing.T) {
		dir, repo := newSynthRepo(t)
		writeFile(t, dir, "greet.go", head)
		commitAll(t, dir, "chore: initial greet.go")

		work := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 100\n}\n\n// B returns two.\nfunc B() int {\n\treturn 200\n}\n"
		writeFile(t, dir, "greet.go", work)

		// @toplevel spans both functions, so it strictly contains A.
		mustStage(t, repo, dir,
			synth.AnchorTarget("greet.go", "@toplevel"),
			synth.AnchorTarget("greet.go", "A"))

		got := indexBlob(t, repo, "greet.go")
		mustParseGo(t, "overlapping @toplevel and A", got)
		// @toplevel won, so B's change comes along with it -- naming the
		// wider extent is what asked for that.
		qt.Assert(t, qt.Equals(got, work))
	})

	t.Run("a new symbol inserted inside an enclosing extent", func(t *testing.T) {
		// The insertion path: C exists only in the worktree, so it resolves
		// to an insert point rather than a replaced range. That point falls
		// inside @toplevel, whose text already contains C -- splicing it in
		// again would stage C twice.
		dir, repo := newSynthRepo(t)
		writeFile(t, dir, "greet.go", head)
		commitAll(t, dir, "chore: initial greet.go")

		work := head + "\n// C returns three.\nfunc C() int {\n\treturn 3\n}\n"
		writeFile(t, dir, "greet.go", work)

		mustStage(t, repo, dir,
			synth.AnchorTarget("greet.go", "@toplevel"),
			synth.AnchorTarget("greet.go", "C"))

		got := indexBlob(t, repo, "greet.go")
		mustParseGo(t, "insertion inside @toplevel", got)
		qt.Assert(t, qt.Equals(got, work))
		qt.Assert(t, qt.Equals(strings.Count(got, "func C() int"), 1))
	})

	t.Run("the same anchor named twice", func(t *testing.T) {
		// Degenerate overlap: an extent overlaps itself. Applying the
		// identical replacement twice cut the wrong bytes the second time
		// whenever the new text was not the same length as the old.
		dir, repo := newSynthRepo(t)
		writeFile(t, dir, "greet.go", head)
		commitAll(t, dir, "chore: initial greet.go")

		work := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 7777777\n}\n\n// B returns two.\nfunc B() int {\n\treturn 2\n}\n"
		writeFile(t, dir, "greet.go", work)

		mustStage(t, repo, dir,
			synth.AnchorTarget("greet.go", "A"),
			synth.AnchorTarget("greet.go", "A"))

		got := indexBlob(t, repo, "greet.go")
		mustParseGo(t, "same anchor twice", got)
		qt.Assert(t, qt.Equals(got, work))
	})
}

func TestStage_UnbornBranchInitialCommit(t *testing.T) {
	// No commits at all: HEAD does not resolve, so CatFile reports
	// headExists=false rather than erroring (git itself exits 128 for
	// "invalid object name 'HEAD'" uniformly with "path not in tree",
	// which gitx.CatFile already folds into a plain false).
	dir, repo := newSynthRepo(t)
	writeFile(t, dir, "new.go", "package main\n\nfunc Hello() string {\n\treturn \"hi\"\n}\n")

	mustStage(t, repo, dir, synth.AnchorTarget("new.go", "Hello"))

	qt.Assert(t, qt.Equals(indexBlob(t, repo, "new.go"), "func Hello() string {\n\treturn \"hi\"\n}"))

	gitIn(t, dir, "commit", "-q", "-m", "feat: add Hello")
	head := gitIn(t, dir, "cat-file", "-p", "HEAD:new.go")
	qt.Assert(t, qt.Equals(head, "func Hello() string {\n\treturn \"hi\"\n}"))
}

func TestStage_UnbornBranchGitignoredPathRefused(t *testing.T) {
	// The gitignore refusal consults HEAD to let an already-tracked path
	// through; an unborn branch has no HEAD to consult, and must still
	// produce the documented exit 7.
	dir, repo := newSynthRepo(t)
	writeFile(t, dir, ".gitignore", "*.log\n")
	writeFile(t, dir, "debug.log", "noise\n")

	err := synth.Stage(context.Background(), repo, dir, []synth.Target{synth.PathTarget("debug.log")})
	assertPathError(t, err, exitcode.PathRefused)
}

func TestStage_NoNewlineAtEOFPreserved(t *testing.T) {
	dir, repo := newSynthRepo(t)
	head := "package main\n\nfunc A() {}\n\nfunc B() int { return 1 }" // deliberately no trailing \n
	writeFile(t, dir, "tail.go", head)
	commitAll(t, dir, "chore: initial tail.go")

	work := "package main\n\nfunc A() {}\n\nfunc B() int { return 42 }" // still no trailing \n
	writeFile(t, dir, "tail.go", work)

	mustStage(t, repo, dir, synth.AnchorTarget("tail.go", "B"))

	got := indexBlob(t, repo, "tail.go")
	qt.Assert(t, qt.Equals(got, work))
	qt.Assert(t, qt.IsFalse(len(got) > 0 && got[len(got)-1] == '\n'))
}

func TestStage_AppendedSymbolInheritsEOFNewline(t *testing.T) {
	// Appending after the last symbol lands the insertion point just
	// BEFORE HEAD's trailing newline, not at true end-of-file -- the
	// case where that terminator is easiest to drop (AGENTS.md: EOF
	// newline inherited, never normalized).
	dir, repo := newSynthRepo(t)
	writeFile(t, dir, "tail.go", "package main\n\nfunc A() {}\n")
	commitAll(t, dir, "chore: initial tail.go")

	writeFile(t, dir, "tail.go", "package main\n\nfunc A() {}\n\nfunc Z() int { return 2 }\n")
	mustStage(t, repo, dir, synth.AnchorTarget("tail.go", "Z"))

	got := indexBlob(t, repo, "tail.go")
	qt.Assert(t, qt.IsTrue(len(got) > 0 && got[len(got)-1] == '\n'))
	qt.Assert(t, qt.IsFalse(len(got) > 1 && got[len(got)-2] == '\n'))
}

func TestStage_RenameStagedAsTwoPathsYieldsR100(t *testing.T) {
	dir, repo := newSynthRepo(t)
	writeFile(t, dir, "old.txt", "hello world\nsecond line\n")
	commitAll(t, dir, "chore: add old.txt")

	// A rename staged as a removal-plus-addition -- not `git mv` -- is
	// exactly what a caller naming two pathspecs produces; git's own
	// status still detects it by content similarity.
	qt.Assert(t, qt.IsNil(os.Remove(filepath.Join(dir, "old.txt"))))
	writeFile(t, dir, "new.txt", "hello world\nsecond line\n")

	mustStage(t, repo, dir, synth.PathTarget("old.txt"), synth.PathTarget("new.txt"))

	status := gitIn(t, dir, "status", "--porcelain")
	qt.Assert(t, qt.StringContains(status, "R  old.txt -> new.txt"))
}

func TestStage_PathspecGlobAndExcludePassThrough(t *testing.T) {
	dir, repo := newSynthRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	writeFile(t, dir, "docs/c.txt", "c\n")

	mustStage(t, repo, dir, synth.PathTarget(":(glob)**/*.txt"), synth.PathTarget(":(exclude)docs/*"))

	status := gitIn(t, dir, "status", "--porcelain")
	qt.Assert(t, qt.StringContains(status, "A  a.txt"))
	qt.Assert(t, qt.Not(qt.StringContains(status, "c.txt")))
}

func TestStage_ModeOnlyChangeStages(t *testing.T) {
	dir, repo := newSynthRepo(t)
	writeFile(t, dir, "script.sh", "#!/bin/sh\necho hi\n")
	commitAll(t, dir, "chore: add script.sh")

	// --sym cannot express a mode change (docs/USAGE.md); a mode-only
	// edit stages by pathspec instead.
	qt.Assert(t, qt.IsNil(os.Chmod(filepath.Join(dir, "script.sh"), 0o755)))

	mustStage(t, repo, dir, synth.PathTarget("script.sh"))

	summary := gitIn(t, dir, "diff", "--staged", "--summary")
	qt.Assert(t, qt.StringContains(summary, "mode change 100644 => 100755"))
	numstat := gitIn(t, dir, "diff", "--staged", "--numstat")
	qt.Assert(t, qt.StringContains(numstat, "0\t0\tscript.sh"))
}

func TestStage_SubmoduleAndSymlinkPathStaging(t *testing.T) {
	dir, repo := newSynthRepo(t)

	writeFile(t, dir, "target.txt", "content\n")
	qt.Assert(t, qt.IsNil(os.Symlink("target.txt", filepath.Join(dir, "link.txt"))))

	subDir := filepath.Join(dir, "sub")
	qt.Assert(t, qt.IsNil(os.MkdirAll(subDir, 0o755)))
	gitIn(t, subDir, "init", "-q")
	gitIn(t, subDir, "config", "user.email", "sub@example.com")
	gitIn(t, subDir, "config", "user.name", "Sub")
	writeFile(t, subDir, "x.txt", "x\n")
	commitAll(t, subDir, "chore: sub commit")

	mustStage(t, repo, dir, synth.PathTarget("target.txt"), synth.PathTarget("link.txt"), synth.PathTarget("sub"))

	lsFiles := gitIn(t, dir, "ls-files", "-s")
	qt.Assert(t, qt.StringContains(lsFiles, "120000"))
	qt.Assert(t, qt.StringContains(lsFiles, "160000"))

	// A symbol anchor on the same symlink is refused rather than
	// misresolved (docs/ANCHORS.md § Paths that anchors cannot address).
	err := synth.Stage(context.Background(), repo, dir, []synth.Target{synth.AnchorTarget("link.txt", "Foo")})
	assertPathError(t, err, exitcode.SpecialPathRefused)
}

func TestStage_GitattributesCleanFilterRequiresPath(t *testing.T) {
	dir, repo := newSynthRepo(t)
	writeFile(t, dir, ".gitattributes", "*.go filter=upper\n")
	gitIn(t, dir, "config", "filter.upper.clean", "tr a-z A-Z")
	gitIn(t, dir, "config", "filter.upper.smudge", "cat")
	commitAll(t, dir, "chore: add gitattributes")

	// A brand new file: staging it exercises HashObject with no prior
	// HEAD blob at all, still through the same --path-carrying call.
	writeFile(t, dir, "f.go", "package p\n\nfunc A() int { return 1 }\n")

	mustStage(t, repo, dir, synth.AnchorTarget("f.go", "A"))

	// Without --path on hash-object, the clean filter is bypassed and
	// this would read back lower-case (AGENTS.md's invariant table).
	qt.Assert(t, qt.Equals(indexBlob(t, repo, "f.go"), "FUNC A() INT { RETURN 1 }"))
}

func TestStage_GitignoredUntrackedRefused(t *testing.T) {
	dir, repo := newSynthRepo(t)
	writeFile(t, dir, ".gitignore", "*.log\n")
	commitAll(t, dir, "chore: add gitignore")
	writeFile(t, dir, "debug.log", "noise\n")

	err := synth.Stage(context.Background(), repo, dir, []synth.Target{synth.PathTarget("debug.log")})
	assertPathError(t, err, exitcode.PathRefused)
}

func TestStage_UnsupportedLanguageAnchorRefused(t *testing.T) {
	dir, repo := newSynthRepo(t)
	writeFile(t, dir, "notes.rs", "fn main() {}\n")
	commitAll(t, dir, "chore: add notes.rs")

	err := synth.Stage(context.Background(), repo, dir, []synth.Target{synth.AnchorTarget("notes.rs", "main")})
	assertPathError(t, err, exitcode.UnsupportedLanguage)
}

func TestStage_ResolveAllBeforeStagingAnyLeavesIndexUntouched(t *testing.T) {
	dir, repo := newSynthRepo(t)
	writeFile(t, dir, "a.go", "package main\n\nfunc A() {}\n")
	commitAll(t, dir, "chore: add a.go")
	writeFile(t, dir, "a.go", "package main\n\nfunc A() { println(1) }\n")

	// Foo does not exist anywhere -- the whole batch must fail before A
	// (which resolves cleanly) is ever staged.
	err := synth.Stage(context.Background(), repo, dir, []synth.Target{
		synth.AnchorTarget("a.go", "A"),
		synth.AnchorTarget("a.go", "Foo"),
	})
	qt.Assert(t, qt.IsNotNil(err))

	status := gitIn(t, dir, "status", "--porcelain")
	qt.Assert(t, qt.Equals(status, " M a.go\n"))
}

func TestStage_NewSymbolInsertsAtNearestSiblingIncludingNewNeighbours(t *testing.T) {
	dir, repo := newSynthRepo(t)
	head := "package main\n\nfunc A() {}\n\nfunc C() {}\n"
	writeFile(t, dir, "sib.go", head)
	commitAll(t, dir, "chore: add sib.go")

	// XNew is not staged; YNew's nearest existing sibling must be walked
	// back to A past XNew.
	work := "package main\n\nfunc A() {}\n\nfunc XNew() {}\n\nfunc YNew() {}\n\nfunc C() {}\n"
	writeFile(t, dir, "sib.go", work)

	mustStage(t, repo, dir, synth.AnchorTarget("sib.go", "YNew"))

	got := indexBlob(t, repo, "sib.go")
	qt.Assert(t, qt.StringContains(got, "func YNew() {}"))
	qt.Assert(t, qt.Not(qt.StringContains(got, "XNew")))

	ia := strings.Index(got, "func A()")
	iy := strings.Index(got, "func YNew()")
	ic := strings.Index(got, "func C()")
	qt.Assert(t, qt.IsTrue(ia >= 0 && ia < iy && iy < ic))
}

func TestStage_MultipleSymbolsSpliceInReverseOffsetOrder(t *testing.T) {
	// Two extents in one file must be
	// applied in reverse byte-offset order, or the first splice shifts the
	// bytes out from under the second. A and B are adjacent with no blank
	// line between them, which is where an off-by-one boundary shows up as
	// run-together syntax rather than as a wrong value.
	dir, repo := newSynthRepo(t)
	head := "package main\n\n" +
		"// A returns one.\nfunc A() int { return 1 }\n" +
		"func B() int { return 2 }\n\n" +
		"// C returns three.\nfunc C() int { return 3 }\n"
	writeFile(t, dir, "m.go", head)
	commitAll(t, dir, "chore: initial m.go")

	// Every symbol changes, but only the outer two are named.
	work := strings.NewReplacer(
		"return 1", "return 11",
		"return 2", "return 22",
		"return 3", "return 33",
	).Replace(head)
	writeFile(t, dir, "m.go", work)

	mustStage(t, repo, dir, synth.AnchorTarget("m.go", "A"), synth.AnchorTarget("m.go", "C"))
	got := indexBlob(t, repo, "m.go")

	qt.Assert(t, qt.StringContains(got, "func A() int { return 11 }"))
	qt.Assert(t, qt.StringContains(got, "func C() int { return 33 }"))
	// B was not named, so it keeps HEAD's value even though the worktree
	// changed it -- the whole point of symbol granularity.
	qt.Assert(t, qt.StringContains(got, "func B() int { return 2 }"))
	qt.Assert(t, qt.Not(qt.StringContains(got, "return 22")))

	// A splice applied at a stale offset duplicates or truncates content
	// rather than failing outright, so count rather than trusting the
	// substring assertions above.
	qt.Assert(t, qt.Equals(strings.Count(got, "func A()"), 1))
	qt.Assert(t, qt.Equals(strings.Count(got, "func B()"), 1))
	qt.Assert(t, qt.Equals(strings.Count(got, "func C()"), 1))
	qt.Assert(t, qt.Equals(strings.Count(got, "// A returns one."), 1))

	mustParseGo(t, "multi-symbol splice", got)
}

func TestStage_DeletedSymbolExcisedFromBlob(t *testing.T) {
	// Deleting a symbol is
	// anchored like any other change: the extent resolves against HEAD,
	// where the symbol still exists, and staging removes it -- doc comment
	// included, since the doc comment is part of the extent.
	dir, repo := newSynthRepo(t)
	head := "package main\n\n" +
		"// A does a thing.\nfunc A() {}\n\n" +
		"// B does another.\nfunc B() {}\n\n" +
		"func C() {}\n"
	writeFile(t, dir, "d.go", head)
	commitAll(t, dir, "chore: initial d.go")

	writeFile(t, dir, "d.go", "package main\n\n"+
		"// A does a thing.\nfunc A() {}\n\n"+
		"func C() {}\n")

	mustStage(t, repo, dir, synth.AnchorTarget("d.go", "B"))
	got := indexBlob(t, repo, "d.go")

	qt.Assert(t, qt.Not(qt.StringContains(got, "func B()")))
	qt.Assert(t, qt.Not(qt.StringContains(got, "B does another")))
	qt.Assert(t, qt.StringContains(got, "func A() {}"))
	qt.Assert(t, qt.StringContains(got, "// A does a thing."))
	qt.Assert(t, qt.StringContains(got, "func C() {}"))

	mustParseGo(t, "deletion", got)
}
