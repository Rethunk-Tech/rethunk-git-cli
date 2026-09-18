package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

func TestResolve_ExtentAndDocAttribution(t *testing.T) {
	t.Parallel()
	// A doc comment directly above ValidateToken, with no blank line,
	// must be part of its
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
	t.Parallel()
	// The locked decision: a blank line between a free-floating comment
	// and the next symbol breaks attribution; a comment with no blank line
	// before the symbol attaches.
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
	t.Parallel()
	// Three consecutive
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
	t.Parallel()
	// Same-named methods on
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
	// The remediation is a definite instruction, not a guess: qualify with
	// one of the listed candidates, never "did you mean" (exit 3's own
	// framing) for a name that already resolves, just ambiguously.
	qt.Assert(t, qt.StringContains(rerr.Error(), "qualify with one of: A.Get, B.Get"))
}

func TestResolve_UnresolvableAnchorSuggestsCandidate(t *testing.T) {
	t.Parallel()
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

// TestResolve_UnresolvableAnchorSuggestsContainerQualifiedCandidate pins the
// docs/ANCHORS.md qualification invariant on exit 3, not just exit 4: a
// bare typo close to a name that exists only inside a container must still
// surface a candidate, and the candidate must be the qualified form that
// actually resolves ("A.Get"), not the bare "Get" that would only bounce
// back into ambiguity or non-existence. Distance is measured against the
// bare name ("Gett" vs "Get"), which is what makes the match close at all --
// measuring against the qualified string "A.Get" would push the distance
// past suggest's own maxDistance and hide it.
func TestResolve_UnresolvableAnchorSuggestsContainerQualifiedCandidate(t *testing.T) {
	t.Parallel()
	src := []byte(`package p

type A struct{}

func (a *A) Get() int { return 1 }
`)

	_, err := resolve.Resolve(resolverGoLang(t), src, "Gett")
	qt.Assert(t, qt.IsNotNil(err))
	var rerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &rerr))
	qt.Assert(t, qt.Equals(rerr.Code, exitcode.AnchorUnresolvable))
	qt.Assert(t, qt.DeepEquals(rerr.Candidates, []string{"A.Get"}))
}

func TestResolve_NestedFuncLiteralNotTopLevel(t *testing.T) {
	t.Parallel()
	// A function literal
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

func TestResolve_ConsecutiveNewSymbolsEachResolveIndependently(t *testing.T) {
	t.Parallel()
	// internal/synth's nearest-existing-sibling insertion walks the
	// worktree's declaration order to find a sibling also present in HEAD,
	// skipping past any that are themselves
	// new -- for W = [A, XNew, YNew, C], staging YNew must insert after A.
	// That walk depends on this resolver giving each consecutive new
	// symbol its own correct extent and preserving their source order;
	// this is the resolve-level guarantee synth's insertion logic relies
	// on.
	src := []byte(`package p

func A() {}

func XNew() {}

func YNew() {}

func C() {}
`)

	a := mustResolve(t, src, "A")
	qt.Assert(t, qt.Equals(string(src[a.Extent.Start:a.Extent.End]), "func A() {}"))

	xNew := mustResolve(t, src, "XNew")
	qt.Assert(t, qt.Equals(string(src[xNew.Extent.Start:xNew.Extent.End]), "func XNew() {}"))

	yNew := mustResolve(t, src, "YNew")
	qt.Assert(t, qt.Equals(string(src[yNew.Extent.Start:yNew.Extent.End]), "func YNew() {}"))

	c := mustResolve(t, src, "C")
	qt.Assert(t, qt.Equals(string(src[c.Extent.Start:c.Extent.End]), "func C() {}"))

	// Source order must hold even though XNew and YNew are consecutive
	// and both "new" -- this is what lets a nearest-sibling walk skip
	// past XNew to find A.
	qt.Assert(t, qt.IsTrue(a.Extent.Start < xNew.Extent.Start))
	qt.Assert(t, qt.IsTrue(xNew.Extent.Start < yNew.Extent.Start))
	qt.Assert(t, qt.IsTrue(yNew.Extent.Start < c.Extent.Start))
}

func TestResolve_OrdinalDisambiguatesRepeatedBareName(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	// @header, @imports, and @toplevel are
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

	// @header must also stop before @imports starts, even when @imports
	// exists with no declarations after it.
	importsOnly := []byte(`package p

import "fmt"
`)
	impHeader := mustResolve(t, importsOnly, "@header")
	qt.Assert(t, qt.Equals(string(importsOnly[impHeader.Extent.Start:impHeader.Extent.End]), "package p"))
	impBlock := mustResolve(t, importsOnly, "@imports")
	qt.Assert(t, qt.IsTrue(impHeader.Extent.End <= impBlock.Extent.Start))
}

func TestResolve_GroupedDeclarationsAddressEachSpec(t *testing.T) {
	t.Parallel()
	// A grouped block that resolved to its first spec alone reported the
	// wrong symbol: editing Beta showed up as a change to Alpha, and the
	// anchor round-trip still passed because Alpha does resolve. A label
	// that validates while naming a symbol nobody touched is worse than no
	// label at all.
	src := []byte(`package p

// Numbers we care about.
const (
	Alpha = 1
	Beta  = 2
)

const Solo = 3

var (
	One = "one"
	Two = "two"
)

func F() int { return Alpha }
`)

	// Every spec is addressable on its own, in source order.
	order, err := resolve.DeclOrder(resolverGoLang(t), src)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(order, []string{"Alpha", "Beta", "Solo", "One", "Two", "F"}))

	// A grouped spec's extent is the spec alone — the keyword and parens
	// belong to the block, not to any one of its specs.
	qt.Assert(t, qt.Equals(string(src[mustResolve(t, src, "Beta").Extent.Start:mustResolve(t, src, "Beta").Extent.End]),
		"Beta  = 2"))
	qt.Assert(t, qt.Equals(string(src[mustResolve(t, src, "Two").Extent.Start:mustResolve(t, src, "Two").Extent.End]),
		`Two = "two"`))

	// A single-spec declaration keeps the whole node, so its extent still
	// covers the const keyword.
	solo := mustResolve(t, src, "Solo")
	qt.Assert(t, qt.Equals(string(src[solo.Extent.Start:solo.Extent.End]), "const Solo = 3"))

	// The block's own doc comment belongs to @toplevel, and @header must
	// stop before it. Both are bounded by the same point, so neither can
	// claim bytes the other already owns.
	header := mustResolve(t, src, "@header")
	toplevel := mustResolve(t, src, "@toplevel")
	qt.Assert(t, qt.Equals(string(src[header.Extent.Start:header.Extent.End]), "package p"))
	qt.Assert(t, qt.IsTrue(header.Extent.End <= toplevel.Extent.Start))
	toplevelText := string(src[toplevel.Extent.Start:toplevel.Extent.End])
	qt.Assert(t, qt.StringContains(toplevelText, "// Numbers we care about."))
	qt.Assert(t, qt.StringContains(toplevelText, "const ("))
	qt.Assert(t, qt.StringContains(toplevelText, "func F()"))
}

func TestResolve_GoInlineMultiNameConstVarBothResolve(t *testing.T) {
	t.Parallel()
	// "const a, b = 1, 2" is one const_spec with two names sharing one
	// value list -- unlike TypeScript's grouped declarators, the grammar
	// gives no sub-range naming b alone, so goSpecNameDeclarations shares
	// the whole spec's extent between both names rather than resolving
	// only the first and leaving b unresolvable, the "silent
	// drag-a-sibling" defect TypeScript's own lexicalDeclarations fix
	// (TestResolve_TypeScriptMultiDeclarator) already guards against for
	// its own grammar.
	src := []byte(`package p

const a, b = 1, 2

var x, y = 3, 4

func F() int { return a + b + x + y }
`)

	order, err := resolve.DeclOrder(resolverGoLang(t), src)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(order, []string{"a", "b", "x", "y", "F"}))

	// Both names resolve, to the identical shared extent -- staging either
	// one splices the same line, honestly reflecting that there is no way
	// to give b its own bytes without a's (and the keyword's) coming along
	// too.
	qt.Assert(t, qt.Equals(string(src[mustResolve(t, src, "a").Extent.Start:mustResolve(t, src, "a").Extent.End]),
		"const a, b = 1, 2"))
	qt.Assert(t, qt.Equals(string(src[mustResolve(t, src, "b").Extent.Start:mustResolve(t, src, "b").Extent.End]),
		"const a, b = 1, 2"))
	qt.Assert(t, qt.Equals(string(src[mustResolve(t, src, "x").Extent.Start:mustResolve(t, src, "x").Extent.End]),
		"var x, y = 3, 4"))
	qt.Assert(t, qt.Equals(string(src[mustResolve(t, src, "y").Extent.Start:mustResolve(t, src, "y").Extent.End]),
		"var x, y = 3, 4"))
}

func TestResolve_GoContainerMembers(t *testing.T) {
	t.Parallel()
	// Struct fields and interface methods are addressable one level in.
	// One fixture exercises the addressable and the deliberately-
	// unaddressable shapes together, including a member reached inside a
	// grouped `type ( ... )` block.
	src := []byte(`package p

type S struct {
	Field int
	A, B  int
	Anon
}

type I interface {
	Do()
}

type (
	Grouped struct {
		X int
	}
)
`)
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".go", src, "S.Field"), "Field int"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".go", src, "I.Do"), "Do()"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".go", src, "Grouped.X"), "X int"))

	lang := resolverGoLang(t)

	// "A, B int" shares one field_declaration for both names -- neither gets
	// its own anchor, since giving A one would silently drag B's text along.
	_, err := resolve.Resolve(lang, src, "S.A")
	qt.Assert(t, qt.IsNotNil(err))
	var rerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &rerr))
	qt.Assert(t, qt.Equals(rerr.Code, exitcode.AnchorUnresolvable))

	// An embedded/anonymous field has no name of its own to address it by.
	_, err = resolve.Resolve(lang, src, "S.Anon")
	qt.Assert(t, qt.IsNotNil(err))

	// A struct field and a method sharing both a receiver/container and a
	// name collide the same way two methods do: ambiguous (exit 4), not a
	// silent pick of one over the other.
	collideSrc := []byte(`package p

type Collide struct {
	Get int
}

func (c *Collide) Get() int { return c.Get }
`)
	_, err = resolve.Resolve(lang, collideSrc, "Collide.Get")
	qt.Assert(t, qt.IsNotNil(err))
	var crerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &crerr))
	qt.Assert(t, qt.Equals(crerr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(crerr.Candidates, []string{"Collide.Get#1", "Collide.Get#2"}))
}
