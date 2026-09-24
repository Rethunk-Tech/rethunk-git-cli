// Staging coverage for internal/synth, per CONTRIBUTING.md's three-file
// test budget. Every case runs against a real temporary git repository --
// no gitx mocking -- because these assertions are about git's own
// behaviour (rename detection, mode changes, clean filters) and a mock
// cannot be wrong in the same way git is right.
package main

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/synth"
)

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
	err := stageTargets(context.Background(), repo, dir, targets)
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
	t.Parallel()
	dir, repo := gittest.New(t)
	head := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 1\n}\n\n// B returns two.\nfunc B() int {\n\treturn 2\n}\n"
	gittest.Write(t, dir, "greet.go", head)
	gittest.Commit(t, dir, "chore: initial greet.go")

	// Both symbols change in the worktree, but only A is named -- the
	// litmus test for symbol-granular staging.
	work := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 100\n}\n\n// B returns two.\nfunc B() int {\n\treturn 200\n}\n"
	gittest.Write(t, dir, "greet.go", work)

	mustStage(t, repo, dir, synth.AnchorTarget("greet.go", "A"))

	want := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 100\n}\n\n// B returns two.\nfunc B() int {\n\treturn 2\n}\n"
	qt.Assert(t, qt.Equals(indexBlob(t, repo, "greet.go"), want))

	// The worktree file itself is never touched by staging.
	onDisk, err := os.ReadFile(filepath.Join(dir, "greet.go"))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(string(onDisk), work))

	// B's own change is still outstanding, unstaged.
	unstaged := gittest.Git(t, dir, "diff", "--numstat")
	qt.Assert(t, qt.StringContains(unstaged, "greet.go"))
}

func TestStage_OverlappingAnchorsCoalesceIntoOneExtent(t *testing.T) {
	t.Parallel()
	// Two targets whose extents cover the same bytes must not be spliced
	// independently. Every op addresses HEAD's original offsets and the
	// pass runs in descending start order, so an inner splice that shifts
	// bytes an outer splice's end still points at must not land mid-token
	// and stage a blob that does not parse.
	//
	// docs/ANCHORS.md requires overlapping or nested anchors to merge into a
	// single contiguous extent. The enclosing extent is already that merged
	// result: its replacement text is its own worktree content, which
	// contains the nested anchor in its new form.
	head := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 1\n}\n\n// B returns two.\nfunc B() int {\n\treturn 2\n}\n"

	t.Run("pseudo-anchor encloses a named symbol", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "greet.go", head, "chore: initial greet.go")

		work := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 100\n}\n\n// B returns two.\nfunc B() int {\n\treturn 200\n}\n"
		gittest.Write(t, dir, "greet.go", work)

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
		dir, repo := gittest.RepoWithFile(t, "greet.go", head, "chore: initial greet.go")

		work := head + "\n// C returns three.\nfunc C() int {\n\treturn 3\n}\n"
		gittest.Write(t, dir, "greet.go", work)

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
		// identical replacement twice must not cut the wrong bytes the
		// second time when the new text is not the same length as the old.
		dir, repo := gittest.RepoWithFile(t, "greet.go", head, "chore: initial greet.go")

		work := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 7777777\n}\n\n// B returns two.\nfunc B() int {\n\treturn 2\n}\n"
		gittest.Write(t, dir, "greet.go", work)

		mustStage(t, repo, dir,
			synth.AnchorTarget("greet.go", "A"),
			synth.AnchorTarget("greet.go", "A"))

		got := indexBlob(t, repo, "greet.go")
		mustParseGo(t, "same anchor twice", got)
		qt.Assert(t, qt.Equals(got, work))
	})
}

func TestStage_NewFileCarriesHeaderAndImports(t *testing.T) {
	t.Parallel()
	// docs/ANCHORS.md: "@header plus @imports is enough to make a synthesized
	// new file compile, which is why both are staged automatically for an
	// untracked file." Naming one symbol in a file absent from HEAD must
	// stage the preamble along with it, not a bare declaration -- no
	// package clause, no imports -- that would commit a file that does not
	// parse, at exit 0.
	work := "package main\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nfunc Shout(s string) string {\n\treturn strings.ToUpper(fmt.Sprint(s))\n}\n\nfunc Unrelated() {}\n"

	t.Run("staged automatically", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "seed.go", "package main\n\nfunc Seed() {}\n", "chore: seed")
		gittest.Write(t, dir, "new.go", work)

		mustStage(t, repo, dir, synth.AnchorTarget("new.go", "Shout"))

		got := indexBlob(t, repo, "new.go")
		mustParseGo(t, "new file staged by symbol", got)
		qt.Assert(t, qt.StringContains(got, "package main"))
		qt.Assert(t, qt.StringContains(got, `"strings"`))
		// Symbol granularity still holds: the preamble comes along, the
		// symbols the caller did not name do not.
		qt.Assert(t, qt.Not(qt.StringContains(got, "Unrelated")))
		// A file with no HEAD blob inherits its EOF newline from the
		// worktree, the only side that has one.
		qt.Assert(t, qt.IsTrue(strings.HasSuffix(got, "}\n")))
	})

	t.Run("naming the header explicitly does not duplicate or reorder it", func(t *testing.T) {
		// insertionPoint ranks insertions by index in the declaration
		// table, and a pseudo-anchor has no entry there, so an explicitly
		// named @header must not sort after every symbol and land the
		// package clause at the bottom of the file.
		dir, repo := gittest.RepoWithFile(t, "seed.go", "package main\n\nfunc Seed() {}\n", "chore: seed")
		gittest.Write(t, dir, "new.go", work)

		mustStage(t, repo, dir,
			synth.AnchorTarget("new.go", "@header"),
			synth.AnchorTarget("new.go", "Shout"))

		got := indexBlob(t, repo, "new.go")
		mustParseGo(t, "explicit @header on a new file", got)
		qt.Assert(t, qt.Equals(strings.Count(got, "package main"), 1))
		qt.Assert(t, qt.IsTrue(strings.HasPrefix(got, "package main")))
	})

	t.Run("a file already in HEAD gets no preamble", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "tracked.go", "package main\n\nimport \"fmt\"\n\nfunc A() { fmt.Println(1) }\n\nfunc B() { fmt.Println(2) }\n", "chore: tracked")
		gittest.Write(t, dir, "tracked.go", "package main\n\nimport \"fmt\"\n\nfunc A() { fmt.Println(100) }\n\nfunc B() { fmt.Println(200) }\n")

		mustStage(t, repo, dir, synth.AnchorTarget("tracked.go", "A"))

		got := indexBlob(t, repo, "tracked.go")
		mustParseGo(t, "tracked file", got)
		qt.Assert(t, qt.Equals(strings.Count(got, "package main"), 1))
		// B is untouched, proving nothing beyond A was restaged.
		qt.Assert(t, qt.StringContains(got, "fmt.Println(2)"))
	})
}

func TestStage_ClassMemberAnchors(t *testing.T) {
	t.Parallel()
	// Go's methods are file-scope, so A.Get always resolves at that
	// granularity. TypeScript and Python put theirs inside a class body, so
	// a member must resolve at its own extent too, not merely at the whole
	// class -- in an idiomatic one-class-per-file module, stopping at the
	// class would be the same as naming the path.
	tsHead := "export class Svc {\n  login(): number { return 1; }\n  logout(): number { return 2; }\n}\n"
	tsWork := "export class Svc {\n  login(): number { return 111; }\n  logout(): number { return 222; }\n}\n"
	pyHead := "class Svc:\n    def login(self):\n        return 1\n\n    def logout(self):\n        return 2\n"
	pyWork := "class Svc:\n    def login(self):\n        return 111\n\n    def logout(self):\n        return 222\n"

	t.Run("typescript member stages without its sibling", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "svc.ts", tsHead, "chore: svc.ts")
		gittest.Write(t, dir, "svc.ts", tsWork)

		mustStage(t, repo, dir, synth.AnchorTarget("svc.ts", "Svc.login"))

		got := indexBlob(t, repo, "svc.ts")
		qt.Assert(t, qt.StringContains(got, "return 111"))
		qt.Assert(t, qt.StringContains(got, "return 2; }")) // logout untouched
		qt.Assert(t, qt.Not(qt.StringContains(got, "return 222")))
	})

	t.Run("python member stages without its sibling", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "svc.py", pyHead, "chore: svc.py")
		gittest.Write(t, dir, "svc.py", pyWork)

		mustStage(t, repo, dir, synth.AnchorTarget("svc.py", "Svc.login"))

		got := indexBlob(t, repo, "svc.py")
		qt.Assert(t, qt.StringContains(got, "return 111"))
		qt.Assert(t, qt.StringContains(got, "return 2\n"))
		qt.Assert(t, qt.Not(qt.StringContains(got, "return 222")))
	})

	t.Run("the class itself remains addressable", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "svc.ts", tsHead, "chore: svc.ts")
		gittest.Write(t, dir, "svc.ts", tsWork)

		mustStage(t, repo, dir, synth.AnchorTarget("svc.ts", "Svc"))

		// Naming the container claims every member, which is what asking for
		// the class means.
		qt.Assert(t, qt.Equals(indexBlob(t, repo, "svc.ts"), tsWork))
	})

	t.Run("a member of a class new to HEAD stages the class", func(t *testing.T) {
		// Splicing the member alone puts a method at file scope, which is
		// not the file in the worktree and does not parse as TypeScript.
		dir, repo := gittest.RepoWithFile(t, "svc.ts", "export const seed = 1;\n", "chore: seed")
		gittest.Write(t, dir, "svc.ts", "export const seed = 1;\n\nexport class Fresh {\n  hello(): number { return 1; }\n}\n")

		mustStage(t, repo, dir, synth.AnchorTarget("svc.ts", "Fresh.hello"))

		got := indexBlob(t, repo, "svc.ts")
		qt.Assert(t, qt.StringContains(got, "class Fresh"))
		qt.Assert(t, qt.StringContains(got, "hello(): number"))
	})

	t.Run("a Go receiver container is a sibling, never escalated into", func(t *testing.T) {
		// The same container-qualified shape, but the type declaration does
		// not enclose the method, so staging the method must not drag it in.
		dir, repo := gittest.RepoWithFile(t, "a.go", "package main\n\ntype A struct{}\n\nfunc Seed() {}\n", "chore: a.go")
		gittest.Write(t, dir, "a.go", "package main\n\ntype A struct{}\n\nfunc (a *A) Get() int { return 1 }\n\nfunc Seed() {}\n")

		mustStage(t, repo, dir, synth.AnchorTarget("a.go", "A.Get"))

		got := indexBlob(t, repo, "a.go")
		mustParseGo(t, "new Go method", got)
		qt.Assert(t, qt.Equals(strings.Count(got, "type A struct{}"), 1))
	})
}

func TestStage_PythonModuleLevelAssignment(t *testing.T) {
	t.Parallel()
	// A module-level "X = 1" is an addressable symbol in the Python adapter.
	// Subscripted and attribute targets name nothing addressable and must
	// stay unaddressable rather than resolving under a bogus symbol.
	dir, repo := gittest.New(t)
	head := "TIMEOUT = 30\nRETRIES = 3\nCONFIG = {}\nCONFIG[\"k\"] = 1\n"
	gittest.Write(t, dir, "conf.py", head)
	gittest.Commit(t, dir, "chore: conf.py")
	gittest.Write(t, dir, "conf.py", "TIMEOUT = 90\nRETRIES = 9\nCONFIG = {}\nCONFIG[\"k\"] = 1\n")

	mustStage(t, repo, dir, synth.AnchorTarget("conf.py", "TIMEOUT"))

	got := indexBlob(t, repo, "conf.py")
	qt.Assert(t, qt.StringContains(got, "TIMEOUT = 90"))
	qt.Assert(t, qt.StringContains(got, "RETRIES = 3")) // sibling untouched

	// CONFIG["k"] names no symbol, so it cannot be staged by anchor.
	err := stageTargets(context.Background(), repo, dir, []synth.Target{synth.AnchorTarget("conf.py", "CONFIG[\"k\"]")})
	qt.Assert(t, qt.IsNotNil(err))
}

func TestStage_MemberDeletionKeepsTheFileParseable(t *testing.T) {
	t.Parallel()
	// spliceExcise collapses the gap a removal leaves by joining what
	// precedes the cut to what follows it, which assumes the cut starts at a
	// line boundary. That holds for a top-level declaration in column zero
	// but not for a class member: the member's own indentation must not be
	// left behind to run into the next member's, producing a Python file
	// that raises IndentationError or a TypeScript file with a stray brace.
	t.Run("python", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "svc.py", "class Svc:\n    def keep(self):\n        return 1\n\n    def gone(self):\n        return 2\n\n    def also(self):\n        return 3\n", "chore: svc.py")
		gittest.Write(t, dir, "svc.py", "class Svc:\n    def keep(self):\n        return 1\n\n    def also(self):\n        return 3\n")

		mustStage(t, repo, dir, synth.AnchorTarget("svc.py", "Svc.gone"))

		qt.Assert(t, qt.Equals(indexBlob(t, repo, "svc.py"),
			"class Svc:\n    def keep(self):\n        return 1\n\n    def also(self):\n        return 3\n"))
	})

	t.Run("typescript", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "svc.ts", "export class Svc {\n  keep(): number { return 1; }\n  gone(): number { return 2; }\n}\n", "chore: svc.ts")
		gittest.Write(t, dir, "svc.ts", "export class Svc {\n  keep(): number { return 1; }\n}\n")

		mustStage(t, repo, dir, synth.AnchorTarget("svc.ts", "Svc.gone"))

		qt.Assert(t, qt.Equals(indexBlob(t, repo, "svc.ts"),
			"export class Svc {\n  keep(): number { return 1; }\n}\n"))
	})

	t.Run("a top-level deletion is unaffected", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "a.go", "package main\n\nfunc Keep() {}\n\nfunc Gone() {}\n\nfunc Also() {}\n", "chore: a.go")
		gittest.Write(t, dir, "a.go", "package main\n\nfunc Keep() {}\n\nfunc Also() {}\n")

		mustStage(t, repo, dir, synth.AnchorTarget("a.go", "Gone"))

		got := indexBlob(t, repo, "a.go")
		mustParseGo(t, "top-level deletion", got)
		qt.Assert(t, qt.Equals(got, "package main\n\nfunc Keep() {}\n\nfunc Also() {}\n"))
	})
}

// TestStage_YAMLNestedKeyByteIdenticalRoundTrip pins the acceptance bar
// for this grammar: YAML's indentation handling is exactly where synthesis
// bugs are easiest to hide, so a nested key's own replace must reproduce
// the worktree byte-for-byte, not merely "close."
func TestStage_YAMLNestedKeyByteIdenticalRoundTrip(t *testing.T) {
	t.Parallel()
	head := "name: CI\n\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go build ./...\n\n" +
		"  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"

	t.Run("a lone nested-key edit round-trips byte-identical", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "ci.yml", head, "chore: ci.yml")
		work := "name: CI\n\njobs:\n  build:\n    runs-on: macos-latest\n    steps:\n      - run: go build ./...\n\n" +
			"  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"
		gittest.Write(t, dir, "ci.yml", work)

		mustStage(t, repo, dir, synth.AnchorTarget("ci.yml", "build.runs-on"))

		// Nothing else in the file changed, so staging the one nested key
		// that did must reproduce the worktree exactly -- indentation,
		// sibling jobs, and the block-scalar-free step list all untouched.
		qt.Assert(t, qt.Equals(indexBlob(t, repo, "ci.yml"), work))
	})

	t.Run("a sibling job's own pending edit stays unstaged", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "ci.yml", head, "chore: ci.yml")
		work := "name: CI\n\njobs:\n  build:\n    runs-on: macos-latest\n    steps:\n      - run: go build ./...\n\n" +
			"  test:\n    runs-on: windows-latest\n    steps:\n      - run: go test ./...\n"
		gittest.Write(t, dir, "ci.yml", work)

		mustStage(t, repo, dir, synth.AnchorTarget("ci.yml", "build.runs-on"))

		got := indexBlob(t, repo, "ci.yml")
		qt.Assert(t, qt.StringContains(got, "runs-on: macos-latest"))
		// test's own runs-on is still HEAD's value: naming build.runs-on
		// alone must never carry test's still-pending edit along with it.
		qt.Assert(t, qt.StringContains(got, "  test:\n    runs-on: ubuntu-latest\n"))
		qt.Assert(t, qt.Not(qt.StringContains(got, "windows-latest")))
	})

	t.Run("naming the job stages its whole subtree, block scalar included", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "ci.yml", head, "chore: ci.yml")
		work := "name: CI\n\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n" +
			"          go build ./...\n          go vet ./...\n\n" +
			"  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"
		gittest.Write(t, dir, "ci.yml", work)

		mustStage(t, repo, dir, synth.AnchorTarget("ci.yml", "jobs.build"))

		// The whole file changed only inside "build", so this must also
		// round-trip byte-identical -- including the block scalar's own
		// internal indentation, preserved verbatim rather than reasoned
		// about.
		qt.Assert(t, qt.Equals(indexBlob(t, repo, "ci.yml"), work))
	})
}

func TestStage_UnbornBranchInitialCommit(t *testing.T) {
	t.Parallel()
	// No commits at all: HEAD does not resolve, so CatFile reports
	// headExists=false rather than erroring (git itself exits 128 for
	// "invalid object name 'HEAD'" uniformly with "path not in tree",
	// which gitx.CatFile already folds into a plain false).
	//
	// Every file is absent from HEAD here, so this is also the new-file case:
	// the package clause must come along, or the repository's very first
	// commit holds a Go file that does not compile.
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "new.go", "package main\n\nfunc Hello() string {\n\treturn \"hi\"\n}\n")

	mustStage(t, repo, dir, synth.AnchorTarget("new.go", "Hello"))

	want := "package main\n\nfunc Hello() string {\n\treturn \"hi\"\n}\n"
	qt.Assert(t, qt.Equals(indexBlob(t, repo, "new.go"), want))

	gittest.Git(t, dir, "commit", "-q", "-m", "feat: add Hello")
	head := gittest.Git(t, dir, "cat-file", "-p", "HEAD:new.go")
	qt.Assert(t, qt.Equals(head, want))
	mustParseGo(t, "unborn-branch initial commit", head)
}

func TestStage_UnbornBranchGitignoredPathRefused(t *testing.T) {
	t.Parallel()
	// The gitignore refusal consults HEAD to let an already-tracked path
	// through; an unborn branch has no HEAD to consult, and must still
	// produce the documented exit 7.
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, ".gitignore", "*.log\n")
	gittest.Write(t, dir, "debug.log", "noise\n")

	err := stageTargets(context.Background(), repo, dir, []synth.Target{synth.PathTarget("debug.log")})
	assertPathError(t, err, exitcode.PathRefused)
}

func TestStage_NoNewlineAtEOFPreserved(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	head := "package main\n\nfunc A() {}\n\nfunc B() int { return 1 }" // deliberately no trailing \n
	gittest.Write(t, dir, "tail.go", head)
	gittest.Commit(t, dir, "chore: initial tail.go")

	work := "package main\n\nfunc A() {}\n\nfunc B() int { return 42 }" // still no trailing \n
	gittest.Write(t, dir, "tail.go", work)

	mustStage(t, repo, dir, synth.AnchorTarget("tail.go", "B"))

	got := indexBlob(t, repo, "tail.go")
	qt.Assert(t, qt.Equals(got, work))
	qt.Assert(t, qt.IsFalse(len(got) > 0 && got[len(got)-1] == '\n'))
}

func TestStage_AppendedSymbolInheritsEOFNewline(t *testing.T) {
	t.Parallel()
	// Appending after the last symbol lands the insertion point just
	// BEFORE HEAD's trailing newline, not at true end-of-file -- the
	// case where that terminator is easiest to drop (AGENTS.md: EOF
	// newline inherited, never normalized).
	dir, repo := gittest.RepoWithFile(t, "tail.go", "package main\n\nfunc A() {}\n", "chore: initial tail.go")

	gittest.Write(t, dir, "tail.go", "package main\n\nfunc A() {}\n\nfunc Z() int { return 2 }\n")
	mustStage(t, repo, dir, synth.AnchorTarget("tail.go", "Z"))

	got := indexBlob(t, repo, "tail.go")
	qt.Assert(t, qt.IsTrue(len(got) > 0 && got[len(got)-1] == '\n'))
	qt.Assert(t, qt.IsFalse(len(got) > 1 && got[len(got)-2] == '\n'))
}

func TestStage_RenameStagedAsTwoPathsYieldsR100(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "old.txt", "hello world\nsecond line\n", "chore: add old.txt")

	// A rename staged as a removal-plus-addition -- not `git mv` -- is
	// exactly what a caller naming two pathspecs produces; git's own
	// status still detects it by content similarity.
	qt.Assert(t, qt.IsNil(os.Remove(filepath.Join(dir, "old.txt"))))
	gittest.Write(t, dir, "new.txt", "hello world\nsecond line\n")

	mustStage(t, repo, dir, synth.PathTarget("old.txt"), synth.PathTarget("new.txt"))

	status := gittest.Git(t, dir, "status", "--porcelain")
	qt.Assert(t, qt.StringContains(status, "R  old.txt -> new.txt"))
}

func TestStage_PathspecGlobAndExcludePassThrough(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "a.txt", "a\n")
	gittest.Write(t, dir, "docs/c.txt", "c\n")

	mustStage(t, repo, dir, synth.PathTarget(":(glob)**/*.txt"), synth.PathTarget(":(exclude)docs/*"))

	status := gittest.Git(t, dir, "status", "--porcelain")
	qt.Assert(t, qt.StringContains(status, "A  a.txt"))
	qt.Assert(t, qt.Not(qt.StringContains(status, "c.txt")))
}

func TestStage_ModeOnlyChangeStages(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "script.sh", "#!/bin/sh\necho hi\n", "chore: add script.sh")

	// --sym cannot express a mode change (docs/USAGE.md); a mode-only
	// edit stages by pathspec instead.
	qt.Assert(t, qt.IsNil(os.Chmod(filepath.Join(dir, "script.sh"), 0o755)))

	mustStage(t, repo, dir, synth.PathTarget("script.sh"))

	summary := gittest.Git(t, dir, "diff", "--staged", "--summary")
	qt.Assert(t, qt.StringContains(summary, "mode change 100644 => 100755"))
	numstat := gittest.Git(t, dir, "diff", "--staged", "--numstat")
	qt.Assert(t, qt.StringContains(numstat, "0\t0\tscript.sh"))
}

func TestStage_SubmoduleAndSymlinkPathStaging(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)

	gittest.Write(t, dir, "target.txt", "content\n")
	qt.Assert(t, qt.IsNil(os.Symlink("target.txt", filepath.Join(dir, "link.txt"))))

	subDir := filepath.Join(dir, "sub")
	qt.Assert(t, qt.IsNil(os.MkdirAll(subDir, 0o750)))
	gittest.Git(t, subDir, "init", "-q")
	gittest.Git(t, subDir, "config", "user.email", "sub@example.com")
	gittest.Git(t, subDir, "config", "user.name", "Sub")
	gittest.Write(t, subDir, "x.txt", "x\n")
	gittest.Commit(t, subDir, "chore: sub commit")

	mustStage(t, repo, dir, synth.PathTarget("target.txt"), synth.PathTarget("link.txt"), synth.PathTarget("sub"))

	lsFiles := gittest.Git(t, dir, "ls-files", "-s")
	qt.Assert(t, qt.StringContains(lsFiles, "120000"))
	qt.Assert(t, qt.StringContains(lsFiles, "160000"))

	// A symbol anchor on the same symlink is refused rather than
	// misresolved (docs/ANCHORS.md § Paths that anchors cannot address).
	err := stageTargets(context.Background(), repo, dir, []synth.Target{synth.AnchorTarget("link.txt", "Foo")})
	assertPathError(t, err, exitcode.SpecialPathRefused)

	// A symbol anchor into the submodule directory is refused the same
	// way, at this same full Stage() level -- special_test.go's own
	// TestClassifyPath already proves classifyPath answers pathGitlink for
	// it; this proves the refusal actually reaches a caller through the
	// real staging path, not just the classifier in isolation.
	err = stageTargets(context.Background(), repo, dir, []synth.Target{synth.AnchorTarget("sub", "Foo")})
	assertPathError(t, err, exitcode.SpecialPathRefused)
}

func TestStage_GitattributesCleanFilterRequiresPath(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, ".gitattributes", "*.go filter=upper\n")
	gittest.Git(t, dir, "config", "filter.upper.clean", "tr a-z A-Z")
	gittest.Git(t, dir, "config", "filter.upper.smudge", "cat")
	gittest.Commit(t, dir, "chore: add gitattributes")

	// A brand new file: staging it exercises HashObject with no prior
	// HEAD blob at all, still through the same --path-carrying call.
	gittest.Write(t, dir, "f.go", "package p\n\nfunc A() int { return 1 }\n")

	mustStage(t, repo, dir, synth.AnchorTarget("f.go", "A"))

	// Without --path on hash-object, the clean filter is bypassed and
	// this would read back lower-case (AGENTS.md's invariant table). The
	// package clause is present because f.go is absent from HEAD, and the
	// filter has to reach the synthesized preamble too, not just the symbol.
	qt.Assert(t, qt.Equals(indexBlob(t, repo, "f.go"), "PACKAGE P\n\nFUNC A() INT { RETURN 1 }\n"))
}

func TestStage_GitignoredUntrackedRefused(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, ".gitignore", "*.log\n", "chore: add gitignore")
	gittest.Write(t, dir, "debug.log", "noise\n")

	err := stageTargets(context.Background(), repo, dir, []synth.Target{synth.PathTarget("debug.log")})
	assertPathError(t, err, exitcode.PathRefused)
}

func TestStage_UnsupportedLanguageAnchorRefused(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "notes.rb", "def main; end\n", "chore: add notes.rb")

	err := stageTargets(context.Background(), repo, dir, []synth.Target{synth.AnchorTarget("notes.rb", "main")})
	assertPathError(t, err, exitcode.UnsupportedLanguage)
}

func TestStage_ResolveAllBeforeStagingAnyLeavesIndexUntouched(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "a.go", "package main\n\nfunc A() {}\n", "chore: add a.go")
	gittest.Write(t, dir, "a.go", "package main\n\nfunc A() { println(1) }\n")

	// Foo does not exist anywhere -- the whole batch must fail before A
	// (which resolves cleanly) is ever staged.
	err := stageTargets(context.Background(), repo, dir, []synth.Target{
		synth.AnchorTarget("a.go", "A"),
		synth.AnchorTarget("a.go", "Foo"),
	})
	qt.Assert(t, qt.IsNotNil(err))

	status := gittest.Git(t, dir, "status", "--porcelain")
	qt.Assert(t, qt.Equals(status, " M a.go\n"))
}

func TestStage_NewSymbolInsertsAtNearestSiblingIncludingNewNeighbours(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	head := "package main\n\nfunc A() {}\n\nfunc C() {}\n"
	gittest.Write(t, dir, "sib.go", head)
	gittest.Commit(t, dir, "chore: add sib.go")

	// XNew is not staged; YNew's nearest existing sibling must be walked
	// back to A past XNew.
	work := "package main\n\nfunc A() {}\n\nfunc XNew() {}\n\nfunc YNew() {}\n\nfunc C() {}\n"
	gittest.Write(t, dir, "sib.go", work)

	mustStage(t, repo, dir, synth.AnchorTarget("sib.go", "YNew"))

	got := indexBlob(t, repo, "sib.go")
	qt.Assert(t, qt.StringContains(got, "func YNew() {}"))
	qt.Assert(t, qt.Not(qt.StringContains(got, "XNew")))

	ia := strings.Index(got, "func A()")
	iy := strings.Index(got, "func YNew()")
	ic := strings.Index(got, "func C()")
	qt.Assert(t, qt.IsTrue(ia >= 0 && ia < iy && iy < ic))
}

func TestStage_NewSiblingAdjacentToModifiedFunctionNotSwallowed(t *testing.T) {
	t.Parallel()
	// Guards against a data-integrity bug: a successful report must match
	// the blob actually written. C is new to HEAD and its nearest existing
	// sibling in worktree declaration order is A, which is ALSO being
	// modified in the same commit. insertionPoint resolves C's insertion
	// point as A's own HEAD extent.End -- "insert right after A" -- which
	// lands on the exact same byte offset as A's editReplace op's own end.
	// coalesceOverlaps' swallowedBy must not treat that boundary as
	// containment (op.start <= k.end, a closed interval) and discard C's
	// insertion as "already part of A's replacement text": A's replacement
	// text is only A's own body and never contains C at all. A report of C
	// staged (opLineCounts computes its line count independently of
	// coalescing) must correspond to a synthesized blob that actually
	// defines C.
	dir, repo := gittest.New(t)
	head := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 1\n}\n\n// B returns two.\nfunc B() int {\n\treturn 2\n}\n"
	gittest.Write(t, dir, "adj.go", head)
	gittest.Commit(t, dir, "chore: initial adj.go")

	// A is modified AND a brand new C is inserted immediately after it, so
	// C's nearest-existing-sibling walk lands on A -- the function also
	// being replaced in this same commit.
	work := "package main\n\n// A returns one.\nfunc A() int {\n\treturn 100\n}\n\n// C returns three.\nfunc C() int {\n\treturn 3\n}\n\n// B returns two.\nfunc B() int {\n\treturn 2\n}\n"
	gittest.Write(t, dir, "adj.go", work)

	mustStage(t, repo, dir, synth.AnchorTarget("adj.go", "A"), synth.AnchorTarget("adj.go", "C"))

	got := indexBlob(t, repo, "adj.go")
	mustParseGo(t, "new sibling adjacent to modified function", got)
	qt.Assert(t, qt.StringContains(got, "func A() int {\n\treturn 100\n}"))
	qt.Assert(t, qt.StringContains(got, "func C() int {\n\treturn 3\n}"))
	qt.Assert(t, qt.Equals(strings.Count(got, "func C() int"), 1))
	// B was never named, so it must keep HEAD's value.
	qt.Assert(t, qt.StringContains(got, "func B() int {\n\treturn 2\n}"))
}

func TestStage_MultipleSymbolsSpliceInReverseOffsetOrder(t *testing.T) {
	t.Parallel()
	// Two extents in one file must be
	// applied in reverse byte-offset order, or the first splice shifts the
	// bytes out from under the second. A and B are adjacent with no blank
	// line between them, which is where an off-by-one boundary shows up as
	// run-together syntax rather than as a wrong value.
	dir, repo := gittest.New(t)
	head := "package main\n\n" +
		"// A returns one.\nfunc A() int { return 1 }\n" +
		"func B() int { return 2 }\n\n" +
		"// C returns three.\nfunc C() int { return 3 }\n"
	gittest.Write(t, dir, "m.go", head)
	gittest.Commit(t, dir, "chore: initial m.go")

	// Every symbol changes, but only the outer two are named.
	work := strings.NewReplacer(
		"return 1", "return 11",
		"return 2", "return 22",
		"return 3", "return 33",
	).Replace(head)
	gittest.Write(t, dir, "m.go", work)

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

// TestStage_MultiAnchorSameFileMatchesWorktreeByteIdentical regresses a bug
// where naming several new anchors in one file placed insertions at wrong
// offsets and collapsed a pre-existing multi-blank-line gap. Root cause:
// insertionPoint (classify.go) ranks an insertion by its index in the
// worktree's declaration table, but a pseudo-anchor like @imports has no
// entry there and defaulted to sorting after every real declaration --
// starting its nearest-existing-sibling walk from the file's last
// declaration instead of from where @imports actually sits, landing its
// splice at the wrong sibling's boundary. When the named anchors cover
// every change in the file, the committed blob must equal the worktree
// byte-for-byte, so this asserts full equality rather than substrings.
func TestStage_MultiAnchorSameFileMatchesWorktreeByteIdentical(t *testing.T) {
	t.Parallel()
	head := "\"\"\"Doc.\"\"\"\n\nfrom __future__ import annotations\n\n\ndef alpha() -> int:\n    return 1\n"
	work := "\"\"\"Doc.\"\"\"\n\nfrom __future__ import annotations\n\nimport logging\n\nlogger = logging.getLogger(__name__)\n\n\ndef alpha() -> int:\n    return 1\n\n\ndef beta() -> int:\n    logger.info(\"x\")\n    return 2\n"

	t.Run("anchors in file order", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "mod.py", head, "chore: mod.py")
		gittest.Write(t, dir, "mod.py", work)

		mustStage(t, repo, dir,
			synth.AnchorTarget("mod.py", "@imports"),
			synth.AnchorTarget("mod.py", "logger"),
			synth.AnchorTarget("mod.py", "beta"))

		qt.Assert(t, qt.Equals(indexBlob(t, repo, "mod.py"), work))
	})

	t.Run("anchors reversed on the command line", func(t *testing.T) {
		// The synthesized blob must not depend on argument order: only the
		// worktree's own declaration order may decide where each insertion
		// lands.
		dir, repo := gittest.RepoWithFile(t, "mod.py", head, "chore: mod.py")
		gittest.Write(t, dir, "mod.py", work)

		mustStage(t, repo, dir,
			synth.AnchorTarget("mod.py", "beta"),
			synth.AnchorTarget("mod.py", "logger"),
			synth.AnchorTarget("mod.py", "@imports"))

		qt.Assert(t, qt.Equals(indexBlob(t, repo, "mod.py"), work))
	})
}

// TestStage_MultiAnchorOutOfOrderVarAndFunctions confirms the insertionPoint
// fix is not @imports-specific: an ordinary var and two functions, staged
// out of worktree order, must land at their true worktree positions too.
func TestStage_MultiAnchorOutOfOrderVarAndFunctions(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "v.go", "package main\n\nfunc Seed() {}\n", "chore: v.go")
	work := "package main\n\nvar Count = 0\n\nfunc Seed() {}\n\nfunc Bump() {\n\tCount++\n}\n"
	gittest.Write(t, dir, "v.go", work)

	mustStage(t, repo, dir,
		synth.AnchorTarget("v.go", "Bump"),
		synth.AnchorTarget("v.go", "Count"))

	got := indexBlob(t, repo, "v.go")
	mustParseGo(t, "var + function out of order", got)
	qt.Assert(t, qt.Equals(got, work))
}

func TestStage_DeletedSymbolExcisedFromBlob(t *testing.T) {
	t.Parallel()
	// Deleting a symbol is
	// anchored like any other change: the extent resolves against HEAD,
	// where the symbol still exists, and staging removes it -- doc comment
	// included, since the doc comment is part of the extent.
	dir, repo := gittest.New(t)
	head := "package main\n\n" +
		"// A does a thing.\nfunc A() {}\n\n" +
		"// B does another.\nfunc B() {}\n\n" +
		"func C() {}\n"
	gittest.Write(t, dir, "d.go", head)
	gittest.Commit(t, dir, "chore: initial d.go")

	gittest.Write(t, dir, "d.go", "package main\n\n"+
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

// TestStage_ModeInheritsFromHEADWhenWorktreeFileGone pins resolveMode's
// (internal/synth/stage.go) HEAD-fallback branch, measured at 36.4% under
// -short -coverpkg=./... with only the worktree-os.Stat arm exercised: when
// the whole file was itself removed from the worktree, staging a symbol
// deletion from it has no worktree entry left to os.Stat, so the mode has
// to come from `git ls-tree HEAD` instead (AGENTS.md's mode-inheritance
// invariant). The fixture is committed executable so a bug that quietly
// defaulted to plain 100644 -- rather than genuinely reading HEAD's own
// entry -- would be caught here, not just a bug that failed to stage at
// all.
func TestStage_ModeInheritsFromHEADWhenWorktreeFileGone(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "gone.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	qt.Assert(t, qt.IsNil(os.Chmod(filepath.Join(dir, "gone.go"), 0o755)))
	gittest.Commit(t, dir, "chore: add executable gone.go")

	qt.Assert(t, qt.IsNil(os.Remove(filepath.Join(dir, "gone.go"))))

	mustStage(t, repo, dir, synth.AnchorTarget("gone.go", "A"))

	lsFiles := gittest.Git(t, dir, "ls-files", "-s", "gone.go")
	qt.Assert(t, qt.StringContains(lsFiles, "100755"))

	got := indexBlob(t, repo, "gone.go")
	qt.Assert(t, qt.Not(qt.StringContains(got, "func A()")))
	qt.Assert(t, qt.StringContains(got, "func B()"))
}

// TestStage_ContainerMemberInsertIsByteIdenticalToWorktree guards against a
// symbol inserted into an existing container gaining a blank line on each
// side: a Go struct field, a Go interface method, and a TypeScript class
// method all sit flush against their siblings in idiomatic source, with no
// blank line between them, so staging a newly added one must not pad one
// in -- the committed blob has to be byte-identical to the worktree, or
// the file still reads as modified right after the commit that was
// supposed to capture it.
//
// Python is deliberately the odd one out here (the language adapter's
// own MembersSitFlush):
// PEP 8 requires a blank line between method definitions inside a class,
// so a Python class method keeps the ordinary top-level-shaped padding
// instead -- proven by its own subtest expecting that blank line to survive.
func TestStage_ContainerMemberInsertIsByteIdenticalToWorktree(t *testing.T) {
	t.Parallel()
	t.Run("go struct field", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "p.go", "package main\n\ntype Point struct {\n\tX int\n\tY int\n}\n", "chore: initial p.go")

		work := "package main\n\ntype Point struct {\n\tX int\n\tY int\n\tZ int\n}\n"
		gittest.Write(t, dir, "p.go", work)

		mustStage(t, repo, dir, synth.AnchorTarget("p.go", "Point.Z"))

		got := indexBlob(t, repo, "p.go")
		mustParseGo(t, "new struct field", got)
		qt.Assert(t, qt.Equals(got, work))
	})

	t.Run("go interface method", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "i.go", "package main\n\ntype Doer interface {\n\tDo()\n}\n", "chore: initial i.go")

		work := "package main\n\ntype Doer interface {\n\tDo()\n\tRedo()\n}\n"
		gittest.Write(t, dir, "i.go", work)

		mustStage(t, repo, dir, synth.AnchorTarget("i.go", "Doer.Redo"))

		got := indexBlob(t, repo, "i.go")
		mustParseGo(t, "new interface method", got)
		qt.Assert(t, qt.Equals(got, work))
	})

	t.Run("typescript class method", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "svc.ts", "export class Svc {\n  login(): number { return 1; }\n}\n", "chore: initial svc.ts")

		work := "export class Svc {\n  login(): number { return 1; }\n  logout(): number { return 2; }\n}\n"
		gittest.Write(t, dir, "svc.ts", work)

		mustStage(t, repo, dir, synth.AnchorTarget("svc.ts", "Svc.logout"))

		got := indexBlob(t, repo, "svc.ts")
		qt.Assert(t, qt.Equals(got, work))
	})

	t.Run("python class method keeps its PEP 8 blank line, not flush", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "svc.py", "class Svc:\n    def login(self):\n        return 1\n", "chore: initial svc.py")

		// A blank line between methods, matching PEP 8 -- unlike the three
		// flush cases above, this one is the byte-identical result BECAUSE
		// the separator is preserved, not suppressed.
		work := "class Svc:\n    def login(self):\n        return 1\n\n    def logout(self):\n        return 2\n"
		gittest.Write(t, dir, "svc.py", work)

		mustStage(t, repo, dir, synth.AnchorTarget("svc.py", "Svc.logout"))

		got := indexBlob(t, repo, "svc.py")
		qt.Assert(t, qt.Equals(got, work))
	})

	t.Run("no newline at EOF is not invented by a member insert", func(t *testing.T) {
		// AGENTS.md: EOF newline is inherited, never normalized. A container
		// that is itself the last thing in the file must not gain a
		// trailing newline it never had just because one of its members was
		// spliced in.
		dir, repo := gittest.New(t)
		head := "package main\n\ntype Point struct {\n\tX int\n}" // deliberately no trailing \n
		gittest.Write(t, dir, "p.go", head)
		gittest.Commit(t, dir, "chore: initial p.go")

		work := "package main\n\ntype Point struct {\n\tX int\n\tY int\n}" // still no trailing \n
		gittest.Write(t, dir, "p.go", work)

		mustStage(t, repo, dir, synth.AnchorTarget("p.go", "Point.Y"))

		got := indexBlob(t, repo, "p.go")
		qt.Assert(t, qt.Equals(got, work))
		qt.Assert(t, qt.IsFalse(len(got) > 0 && got[len(got)-1] == '\n'))
	})
}

// TestStage_WidenedMemberDoesNotDuplicateItsNewContainer guards against a
// data-integrity bug: naming a brand new container and one of its own
// members in the same commit must synthesize the container exactly once.
// escalateToContainer widens the member's op to the whole container (a
// struct field cannot be inserted alone into a type HEAD does not have),
// which resolves to the identical worktree extent the container's own
// anchor already produced -- two ops addressing the same bytes, not two
// distinct insertions that merely land at the same point. Only the latter
// case is what mergeInsertTies exists to concatenate.
func TestStage_WidenedMemberDoesNotDuplicateItsNewContainer(t *testing.T) {
	t.Parallel()

	t.Run("member widened to its container is not spliced twice", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "p.go", "package p\n\nfunc E() int { return 1 }\n", "chore: initial p.go")

		work := "package p\n\nfunc E() int { return 1 }\n\ntype T struct {\n\ta int\n}\n"
		gittest.Write(t, dir, "p.go", work)

		mustStage(t, repo, dir, synth.AnchorTarget("p.go", "T"), synth.AnchorTarget("p.go", "T.a"))

		got := indexBlob(t, repo, "p.go")
		mustParseGo(t, "widened member beside its new container", got)
		qt.Assert(t, qt.Equals(strings.Count(got, "type T struct"), 1))
		qt.Assert(t, qt.Equals(got, work))
	})

	t.Run("naming order does not matter", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "p.go", "package p\n\nfunc E() int { return 1 }\n", "chore: initial p.go")

		work := "package p\n\nfunc E() int { return 1 }\n\ntype T struct {\n\ta int\n}\n"
		gittest.Write(t, dir, "p.go", work)

		// The member named first this time, container second.
		mustStage(t, repo, dir, synth.AnchorTarget("p.go", "T.a"), synth.AnchorTarget("p.go", "T"))

		got := indexBlob(t, repo, "p.go")
		mustParseGo(t, "widened member named before its container", got)
		qt.Assert(t, qt.Equals(strings.Count(got, "type T struct"), 1))
		qt.Assert(t, qt.Equals(got, work))
	})

	t.Run("three members of one new container still dedupe to one", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "p.go", "package p\n\nfunc E() int { return 1 }\n", "chore: initial p.go")

		work := "package p\n\nfunc E() int { return 1 }\n\ntype T struct {\n\ta int\n\tb int\n\tc int\n}\n"
		gittest.Write(t, dir, "p.go", work)

		mustStage(t, repo, dir,
			synth.AnchorTarget("p.go", "T"),
			synth.AnchorTarget("p.go", "T.a"),
			synth.AnchorTarget("p.go", "T.b"),
			synth.AnchorTarget("p.go", "T.c"))

		got := indexBlob(t, repo, "p.go")
		mustParseGo(t, "three widened members beside their new container", got)
		qt.Assert(t, qt.Equals(strings.Count(got, "type T struct"), 1))
		qt.Assert(t, qt.Equals(got, work))
	})

	t.Run("two different new containers stay independent", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "p.go", "package p\n\nfunc E() int { return 1 }\n", "chore: initial p.go")

		work := "package p\n\nfunc E() int { return 1 }\n\ntype T struct {\n\ta int\n}\n\ntype U struct {\n\tb int\n}\n"
		gittest.Write(t, dir, "p.go", work)

		mustStage(t, repo, dir,
			synth.AnchorTarget("p.go", "T"),
			synth.AnchorTarget("p.go", "T.a"),
			synth.AnchorTarget("p.go", "U"),
			synth.AnchorTarget("p.go", "U.b"))

		got := indexBlob(t, repo, "p.go")
		mustParseGo(t, "two new containers each with a widened member", got)
		qt.Assert(t, qt.Equals(strings.Count(got, "type T struct"), 1))
		qt.Assert(t, qt.Equals(strings.Count(got, "type U struct"), 1))
		qt.Assert(t, qt.Equals(got, work))
	})
}

// TestStage_SiblingReceiverMethodKeepsBlankLinePadding is the counterpart
// guard to the test above: a Go receiver method is container-QUALIFIED
// (resolve.Resolution.Container is set) but not container-NESTED -- it is a
// top-level declaration beside its receiver type, not inside it -- and must
// keep the ordinary blank-line separation a new top-level declaration gets.
// Getting this wrong would splice a brand new method flush against
// whatever the nearest existing declaration is.
func TestStage_SiblingReceiverMethodKeepsBlankLinePadding(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "a.go", "package main\n\ntype A struct{}\n\nfunc Seed() {}\n", "chore: initial a.go")

	work := "package main\n\ntype A struct{}\n\nfunc (a *A) Get() int { return 1 }\n\nfunc Seed() {}\n"
	gittest.Write(t, dir, "a.go", work)

	mustStage(t, repo, dir, synth.AnchorTarget("a.go", "A.Get"))

	got := indexBlob(t, repo, "a.go")
	mustParseGo(t, "new Go receiver method", got)
	qt.Assert(t, qt.Equals(got, work))
}

// TestStage_HTMLElementByID exercises the pipeline the div#app demand
// case motivates: resolving a tag-qualified id anchor through a real git
// index, nested inside another element, alongside a sibling void element
// whose own trailing content tree-sitter-html's external scanner is
// measured absorbing -- proving that quirk never crosses into a
// neighbour's own staged bytes.
func TestStage_HTMLElementByID(t *testing.T) {
	t.Parallel()
	head := "<!DOCTYPE html>\n<html>\n<body>\n<div id=\"app\">\n  <section id=\"content\">v1</section>\n  <input id=\"field\" type=\"text\">\n</div>\n</body>\n</html>\n"

	t.Run("nested element by id", func(t *testing.T) {
		t.Parallel()
		dir, repo := gittest.RepoWithFile(t, "index.html", head, "chore: initial index.html")

		work := strings.Replace(head, "v1", "v2", 1)
		gittest.Write(t, dir, "index.html", work)

		mustStage(t, repo, dir, synth.AnchorTarget("index.html", "section#content"))

		got := indexBlob(t, repo, "index.html")
		qt.Assert(t, qt.Equals(got, work))
	})

	t.Run("void element boundary is not absorbed into a neighbour", func(t *testing.T) {
		t.Parallel()
		dir, repo := gittest.RepoWithFile(t, "index.html", head, "chore: initial index.html")

		// Only the void input's own attribute changes; section#content's
		// own "v1" text must survive untouched even though input#field's
		// own node, measured directly, absorbs the trailing "\n" up to
		// </div> -- left alone as the grammar's own
		// honest boundary, the same way TOML's trailing-blank-line
		// absorption already is, rather than trimmed.
		work := strings.Replace(head, `type="text"`, `type="email"`, 1)
		gittest.Write(t, dir, "index.html", work)

		mustStage(t, repo, dir, synth.AnchorTarget("index.html", "input#field"))

		got := indexBlob(t, repo, "index.html")
		qt.Assert(t, qt.Equals(got, work))
		qt.Assert(t, qt.StringContains(got, ">v1<"))
	})
}

// TestPlanStage_PreambleRowsAppearInResults guards against a --dry-run
// undercount: TestStage_NewFileCarriesHeaderAndImports already proves the
// @header/@imports preamble is staged for a new file, and plan.Results
// must carry a row for it too -- not merely announce it on stderr's
// [notice] line -- or a --dry-run preview (which prints nothing but
// plan.Results) reports only the named symbol's own count, silently
// dropping the preamble's.
//
// The row-level counts here DO sum to git's raw numstat total for the whole
// file: @header and @imports each absorb their own mandatory trailing
// separator (gofmt always leaves exactly one blank line after Go's package
// clause and after its import block), so the blank line between two regions
// belongs to whichever one precedes it rather than to nobody. Hello, being
// last, needs no such absorption -- its extent has no trailing newline of
// its own, and countLines' "no newline at end of file" convention already
// credits it the one line that gap would otherwise be.
func TestPlanStage_PreambleRowsAppearInResults(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "seed.go", "package main\n\nfunc Seed() {}\n", "chore: seed")
	gittest.Write(t, dir, "new.go", "package main\n\nimport \"fmt\"\n\nfunc Hello() {\n\tfmt.Println(\"hi\")\n}\n")

	plan, err := synth.PlanStage(context.Background(), repo, dir, []synth.Target{synth.AnchorTarget("new.go", "Hello")})
	qt.Assert(t, qt.IsNil(err))

	results := plan.Results
	qt.Assert(t, qt.Equals(len(results), 3))

	labels := make([]string, len(results))
	totalAdded := 0
	for i, r := range results {
		labels[i] = r.Target.Symbol.Path + ":" + r.Target.Symbol.Anchor
		qt.Assert(t, qt.Equals(r.Outcome, synth.Staged))
		qt.Assert(t, qt.Equals(r.Deleted, 0))
		totalAdded += r.Added
	}
	// @header and @imports sort ahead of the named symbol -- same ordering
	// rule as any other anchor in this file (sortResults: path, then
	// position, then name), and both pseudos happen to share position 0 in a
	// brand new file, same as the named symbol does.
	qt.Assert(t, qt.DeepEquals(labels, []string{"new.go:@header", "new.go:@imports", "new.go:Hello"}))
	// @header and @imports must be visible here too, not merely staged
	// (proven by TestStage_NewFileCarriesHeaderAndImports) while invisible
	// to the caller.
	qt.Assert(t, qt.Equals(results[0].Added, 2)) // "package main" + its absorbed blank line
	qt.Assert(t, qt.Equals(results[1].Added, 2)) // `import "fmt"` + its absorbed blank line
	qt.Assert(t, qt.Equals(results[2].Added, 3)) // Hello's own 3-line body

	qt.Assert(t, qt.IsNil(plan.Apply(context.Background(), repo, dir)))
	got := indexBlob(t, repo, "new.go")
	mustParseGo(t, "preamble rows reflect what was actually staged", got)
	qt.Assert(t, qt.Equals(got, "package main\n\nimport \"fmt\"\n\nfunc Hello() {\n\tfmt.Println(\"hi\")\n}\n"))

	numstat := gittest.Git(t, dir, "diff", "--staged", "--numstat", "--", "new.go")
	fields := strings.Fields(strings.TrimSpace(numstat))
	qt.Assert(t, qt.Equals(len(fields), 3))
	fileAdded, err := strconv.Atoi(fields[0])
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(totalAdded, fileAdded))
}

// TestPlanStage_PreambleDoesNotAbsorbSeparatorForLanguagesThatDontOwnOne is
// the negative case for whether a language's own formatting convention
// deterministically inserts a blank line after @imports: neither TypeScript
// nor Python inserts a blank line after the import block the way gofmt does
// after Go's, so @imports' own row must stay exactly its own text -- not
// the file's true total. That gap is accepted: separator ownership beyond
// @header/@imports was considered and deferred, not built -- and not
// silently guessed away.
func TestPlanStage_PreambleDoesNotAbsorbSeparatorForLanguagesThatDontOwnOne(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "seed.ts", "export const seed = 1;\n", "chore: seed")
	gittest.Write(t, dir, "new.ts", "import { z } from \"./z\";\n\nexport function hello() {}\n")

	plan, err := synth.PlanStage(context.Background(), repo, dir, []synth.Target{synth.AnchorTarget("new.ts", "hello")})
	qt.Assert(t, qt.IsNil(err))

	results := plan.Results
	qt.Assert(t, qt.Equals(len(results), 2))
	qt.Assert(t, qt.Equals(results[0].Target.Symbol.Anchor, "@imports"))
	// `import { z } from "./z";` alone -- one line, no absorbed blank line.
	qt.Assert(t, qt.Equals(results[0].Added, 1))
	qt.Assert(t, qt.Equals(results[1].Target.Symbol.Anchor, "hello"))
}

// stageTargets runs synth's two steps back to back. Production always keeps
// them apart -- rgit commit resolves first so --dry-run and the exit-11
// "nothing to commit" check can decide before anything is written.
func stageTargets(ctx context.Context, repo *gitx.Repo, root string, targets []synth.Target) error {
	plan, err := synth.PlanStage(ctx, repo, root, targets)
	if err != nil {
		return err
	}
	return plan.Apply(ctx, repo, root)
}

// TestStage_FirstContainerMemberLandsInsideTheContainer pins the case the
// sibling walk cannot reach on its own. Source order lists a container ahead
// of everything it contains, so for a container HEAD already has but whose
// members it lacks, the nearest sibling found walking backwards is the
// container itself — and its extent ends after the closing delimiter. Splicing
// there puts the member outside the thing it belongs to and produces a blob
// that does not parse, at exit 0.
func TestStage_FirstContainerMemberLandsInsideTheContainer(t *testing.T) {
	t.Parallel()

	t.Run("go empty struct", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "p.go", "package main\n\ntype Point struct {\n}\n", "chore: initial p.go")

		work := "package main\n\ntype Point struct {\n\tZ int\n}\n"
		gittest.Write(t, dir, "p.go", work)

		mustStage(t, repo, dir, synth.AnchorTarget("p.go", "Point.Z"))

		got := indexBlob(t, repo, "p.go")
		mustParseGo(t, "first field of an empty struct", got)
		qt.Assert(t, qt.Equals(got, work))
	})

	t.Run("go member ahead of every member HEAD has", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "q.go", "package main\n\ntype Q struct {\n\tY int\n}\n", "chore: initial q.go")

		work := "package main\n\ntype Q struct {\n\tZ int\n\tY int\n}\n"
		gittest.Write(t, dir, "q.go", work)

		mustStage(t, repo, dir, synth.AnchorTarget("q.go", "Q.Z"))

		got := indexBlob(t, repo, "q.go")
		mustParseGo(t, "field ahead of the existing one", got)
		qt.Assert(t, qt.Equals(got, work))
	})

	t.Run("typescript empty class", func(t *testing.T) {
		dir, repo := gittest.RepoWithFile(t, "svc.ts", "export class Svc {\n}\n", "chore: initial svc.ts")

		work := "export class Svc {\n  hello(): number { return 1; }\n}\n"
		gittest.Write(t, dir, "svc.ts", work)

		mustStage(t, repo, dir, synth.AnchorTarget("svc.ts", "Svc.hello"))

		qt.Assert(t, qt.Equals(indexBlob(t, repo, "svc.ts"), work))
	})
}
