// Resolver coverage, per CONTRIBUTING.md's three-file test budget. This
// wave covers the Go grammar adapter only — TypeScript and Python fixtures
// land once their adapters do. Byte offsets below were pinned by running
// the resolver against each fixture and reading back the real tree-sitter
// output (fixture-first discipline, CONTRIBUTING.md § Tests); they are not
// derived from grammar documentation.
package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

func resolverGoLang(t *testing.T) resolve.Language {
	t.Helper()
	lang, ok := resolve.ForExtension(".go")
	if !ok {
		t.Fatal("resolve: no adapter registered for .go")
	}
	return lang
}

func mustResolve(t *testing.T, src []byte, anchor string) *resolve.Resolution {
	t.Helper()
	res, err := resolve.Resolve(resolverGoLang(t), src, anchor)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", anchor, err)
	}
	return res
}

func TestResolve_ExtentAndDocAttribution(t *testing.T) {
	// Ported from spike/test_basic.go's HEAD fixture: a doc comment
	// directly above ValidateToken, no blank line, must be part of its
	// extent; the sibling helper() must be untouched by resolving
	// ValidateToken alone.
	src := []byte(`package p

import "fmt"

// ValidateToken checks the JWT.
// Returns ErrExpired if stale.
func ValidateToken(t string) error {
	return nil
}

// helper does a thing.
func helper() int { return 1 }

func untouched() {}
`)

	res := mustResolve(t, src, "ValidateToken")
	// Observed: full extent starts at "// ValidateToken", declOnly starts
	// at "func" — the doc-comment prefix the LSP cross-check must strip.
	qt.Assert(t, qt.Equals(string(src[res.Extent.Start:res.Extent.End]),
		"// ValidateToken checks the JWT.\n// Returns ErrExpired if stale.\nfunc ValidateToken(t string) error {\n\treturn nil\n}"))
	qt.Assert(t, qt.Equals(string(src[res.DeclOnly.Start:res.DeclOnly.End]),
		"func ValidateToken(t string) error {\n\treturn nil\n}"))
	qt.Assert(t, qt.Equals(res.Anchor, "ValidateToken"))
	qt.Assert(t, qt.IsFalse(res.Pseudo))

	// helper's own doc comment must not have bled into ValidateToken's
	// extent, and vice versa.
	helperRes := mustResolve(t, src, "helper")
	qt.Assert(t, qt.Equals(string(src[helperRes.Extent.Start:helperRes.Extent.End]),
		"// helper does a thing.\nfunc helper() int { return 1 }"))
}

func TestResolve_BlankLineBreaksDocAttribution(t *testing.T) {
	// Ported from spike/adversarial.py section A (the locked decision): a
	// blank line between a free-floating comment and the next symbol
	// breaks attribution; a comment with no blank line before the symbol
	// attaches.
	src := []byte(`package p

func A() {}

// free-floating note about the module

// Doc for B.
func B() {}
`)

	a := mustResolve(t, src, "A")
	qt.Assert(t, qt.Equals(string(src[a.Extent.Start:a.Extent.End]), "func A() {}"))

	b := mustResolve(t, src, "B")
	bText := string(src[b.Extent.Start:b.Extent.End])
	qt.Assert(t, qt.Equals(bText, "// Doc for B.\nfunc B() {}"))
	qt.Assert(t, qt.Not(qt.StringContains(bText, "free-floating")))
}

func TestResolve_ContiguousCommentsAttach(t *testing.T) {
	// Ported from spike/adversarial.py section B: three consecutive
	// comment lines with no blank line between them all attach to the
	// symbol below.
	src := []byte(`package p

// line one
// line two
// line three
func C() {}
`)

	c := mustResolve(t, src, "C")
	cText := string(src[c.Extent.Start:c.Extent.End])
	// Observed: full extent is exactly the three comment lines plus the
	// declaration — nothing dropped, nothing from outside pulled in.
	qt.Assert(t, qt.Equals(cText, "// line one\n// line two\n// line three\nfunc C() {}"))
}

func TestResolve_ReceiverQualifiedDisambiguation(t *testing.T) {
	// Ported from spike/adversarial.py section D: same-named methods on
	// different receivers resolve independently when qualified, and the
	// bare name is ambiguous rather than silently picking one.
	src := []byte(`package p

type A struct{}
type B struct{}

// Get returns A's value.
func (a *A) Get() int { return 1 }

// Get returns B's value.
func (b *B) Get() int { return 2 }
`)

	aGet := mustResolve(t, src, "A.Get")
	qt.Assert(t, qt.Equals(string(src[aGet.Extent.Start:aGet.Extent.End]),
		"// Get returns A's value.\nfunc (a *A) Get() int { return 1 }"))

	bGet := mustResolve(t, src, "B.Get")
	qt.Assert(t, qt.Equals(string(src[bGet.Extent.Start:bGet.Extent.End]),
		"// Get returns B's value.\nfunc (b *B) Get() int { return 2 }"))

	// gopls spells the receiver "(*A).Get"; rgit accepts that on input but
	// always emits "A.Get" (docs/ANCHORS.md).
	glosplGet := mustResolve(t, src, "(*A).Get")
	qt.Assert(t, qt.Equals(glosplGet.Anchor, "A.Get"))

	_, err := resolve.Resolve(resolverGoLang(t), src, "Get")
	qt.Assert(t, qt.IsNotNil(err))
	var rerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &rerr))
	qt.Assert(t, qt.Equals(rerr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(rerr.Candidates, []string{"A.Get", "B.Get"}))
}

func TestResolve_UnresolvableAnchorSuggestsCandidate(t *testing.T) {
	src := []byte(`package p

func Foo() {}
`)

	_, err := resolve.Resolve(resolverGoLang(t), src, "Fooo")
	qt.Assert(t, qt.IsNotNil(err))
	var rerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &rerr))
	qt.Assert(t, qt.Equals(rerr.Code, exitcode.AnchorUnresolvable))
	qt.Assert(t, qt.DeepEquals(rerr.Candidates, []string{"Foo"}))
}

func TestResolve_NestedFuncLiteralNotTopLevel(t *testing.T) {
	// Ported from spike/adversarial.py section G: a function literal
	// nested in Outer's body is not itself a top-level symbol — it comes
	// along as part of Outer's own extent — and it does not fracture the
	// file into extra unaddressable siblings.
	src := []byte(`package p

func Outer() func() int {
	return func() int { return 1 }
}

func After() {}
`)

	outer := mustResolve(t, src, "Outer")
	qt.Assert(t, qt.Equals(string(src[outer.Extent.Start:outer.Extent.End]),
		"func Outer() func() int {\n\treturn func() int { return 1 }\n}"))

	after := mustResolve(t, src, "After")
	qt.Assert(t, qt.Equals(string(src[after.Extent.Start:after.Extent.End]), "func After() {}"))

	// The nested literal has no name of its own to resolve by.
	_, err := resolve.Resolve(resolverGoLang(t), src, "func")
	qt.Assert(t, qt.IsNotNil(err))
}

func TestResolve_OrdinalDisambiguatesRepeatedBareName(t *testing.T) {
	// Go permits multiple func init() in one file; with no receiver to
	// qualify them, the bare name is ambiguous and the ordinal form is
	// the last resort (docs/ANCHORS.md).
	src := []byte(`package p

func init() { println(1) }

func init() { println(2) }
`)

	first := mustResolve(t, src, "init#1")
	qt.Assert(t, qt.StringContains(string(src[first.Extent.Start:first.Extent.End]), "println(1)"))

	second := mustResolve(t, src, "init#2")
	qt.Assert(t, qt.StringContains(string(src[second.Extent.Start:second.Extent.End]), "println(2)"))

	_, err := resolve.Resolve(resolverGoLang(t), src, "init")
	qt.Assert(t, qt.IsNotNil(err))
	var rerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &rerr))
	qt.Assert(t, qt.Equals(rerr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(rerr.Candidates, []string{"init#1", "init#2"}))
}

func TestResolve_PseudoAnchors(t *testing.T) {
	// Ported from spike/pseudo.py: @header, @imports, and @toplevel are
	// all addressable. @header spans build tag through package_clause
	// even though a blank line separates them — Go requires that blank
	// line, and both still belong to the header (docs/ANCHORS.md).
	src := []byte(`//go:build linux

// Package p does things.
package p

import (
	"fmt"
	"os"
)

const Version = "1.0"

var logger *os.Logger

type Config struct{ Name string }

func Run() { fmt.Println(Version) }
`)

	header := mustResolve(t, src, "@header")
	qt.Assert(t, qt.IsTrue(header.Pseudo))
	qt.Assert(t, qt.Equals(string(src[header.Extent.Start:header.Extent.End]),
		"//go:build linux\n\n// Package p does things.\npackage p"))

	imports := mustResolve(t, src, "@imports")
	qt.Assert(t, qt.Equals(string(src[imports.Extent.Start:imports.Extent.End]),
		"import (\n\t\"fmt\"\n\t\"os\"\n)"))

	toplevel := mustResolve(t, src, "@toplevel")
	toplevelText := string(src[toplevel.Extent.Start:toplevel.Extent.End])
	qt.Assert(t, qt.StringContains(toplevelText, "const Version"))
	qt.Assert(t, qt.StringContains(toplevelText, "var logger"))
	qt.Assert(t, qt.StringContains(toplevelText, "type Config"))
	qt.Assert(t, qt.StringContains(toplevelText, "func Run()"))
	qt.Assert(t, qt.Not(qt.StringContains(toplevelText, "import")))
	qt.Assert(t, qt.Not(qt.StringContains(toplevelText, "go:build")))

	// @header must stop at the first declaration's extent, not at the first
	// non-header node. Comments are a header kind, so a doc comment on the
	// first declaration is otherwise swallowed: @header and that symbol
	// would claim the same bytes, and staging @header alone would commit a
	// comment nobody named. The fixture above cannot catch this because
	// its first declaration is undocumented.
	documented := []byte(`//go:build linux

// Package p does things.
package p

// Doc for A.
func A() {}
`)

	docHeader := mustResolve(t, documented, "@header")
	qt.Assert(t, qt.Equals(string(documented[docHeader.Extent.Start:docHeader.Extent.End]),
		"//go:build linux\n\n// Package p does things.\npackage p"))

	docA := mustResolve(t, documented, "A")
	qt.Assert(t, qt.Equals(string(documented[docA.Extent.Start:docA.Extent.End]),
		"// Doc for A.\nfunc A() {}"))
	qt.Assert(t, qt.IsTrue(docHeader.Extent.End <= docA.Extent.Start))
}

func TestResolve_UnsupportedLanguage(t *testing.T) {
	// A symbol anchor on a file whose language has no v1 grammar is the
	// caller's exit 9 (docs/ANCHORS.md § Language support); the resolver's
	// contribution is just reporting the extension unclaimed.
	_, ok := resolve.ForExtension(".rs")
	qt.Assert(t, qt.IsFalse(ok))
}
