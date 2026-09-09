// Resolver coverage, per CONTRIBUTING.md's three-file test budget. Byte
// offsets below reflect real tree-sitter output against each fixture, not
// grammar docs.
package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsptest"
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

// mustResolveExt is the cross-language form: it picks the adapter by file
// extension, exercising registration as well as resolution.
func mustResolveExt(t *testing.T, ext string, src []byte, anchor string) string {
	t.Helper()
	lang, ok := resolve.ForExtension(ext)
	if !ok {
		t.Fatalf("resolve: no adapter registered for %s", ext)
	}
	res, err := resolve.Resolve(lang, src, anchor)
	if err != nil {
		t.Fatalf("Resolve(%s, %q): %v", ext, anchor, err)
	}
	return string(src[res.Extent.Start:res.Extent.End])
}

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

func TestResolve_TypeScriptAndPython(t *testing.T) {
	t.Parallel()
	// The two grammars whose node shapes differ from Go in ways that fail
	// silently rather than loudly.

	ts := []byte(`import {a} from 'a'
import b from 'b'

/** Doc for F. */
export function F(): number { return 1 }

export const G = (x: number) => x + 1
`)

	// An exported symbol is an export_statement wrapping the declaration,
	// and the extent is the outermost node: naming F means the statement
	// including its export keyword, not the function buried inside it.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", ts, "F"),
		"/** Doc for F. */\nexport function F(): number { return 1 }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", ts, "G"),
		"export const G = (x: number) => x + 1"))

	// @imports spans N nodes here where Go has exactly one. A Go-shaped
	// implementation stages only the first import and silently drops the
	// rest.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", ts, "@imports"),
		"import {a} from 'a'\nimport b from 'b'"))

	// TSX is a separate grammar, not a mode: parsed as TypeScript, the JSX
	// below would yield ERROR nodes rather than resolving.
	tsx := []byte(`import React from 'react'

export function App() { return <div/> }
`)
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".tsx", tsx, "App"),
		"export function App() { return <div/> }"))

	py := []byte(`import os
from sys import path

@dec
def g():
    return 2
`)

	// The extent is the decorated_definition, not the function_definition
	// inside it — decorators are part of the symbol (docs/ANCHORS.md).
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".py", py, "g"),
		"@dec\ndef g():\n    return 2"))

	// "from x import y" is import_from_statement, a different kind from
	// import_statement; matching only the latter drops every from-import.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".py", py, "@imports"),
		"import os\nfrom sys import path"))
}

func TestResolve_TypeScriptGrammarHoles(t *testing.T) {
	t.Parallel()
	// One fixture exercises every declarationFor node kind at once rather
	// than one test per kind: enum, abstract class, generator function, var
	// declaration, namespace, default export, and an ordinary class method
	// must all resolve, not exit 3 unresolved.
	src := []byte(`enum Color { Red, Blue }
abstract class Base { run() { return 1 } }
function* gen() { yield 1 }
var varDecl = 1
namespace N { export function inner() { return 1 } }
export default function () { return 2 }
class Ok { hello() { return 3 } }
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Color"), "enum Color { Red, Blue }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Base"),
		"abstract class Base { run() { return 1 } }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Base.run"), "run() { return 1 }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "gen"), "function* gen() { yield 1 }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "varDecl"), "var varDecl = 1"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "N"),
		"namespace N { export function inner() { return 1 } }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "N.inner"),
		"export function inner() { return 1 }"))

	// The anonymous default export (export_statement wrapping a nameless
	// function_expression) must not appear in the index under any spelling —
	// it is deliberately unaddressable, not merely absent under a wrong
	// guess.
	lang, ok := resolve.ForExtension(".ts")
	qt.Assert(t, qt.IsTrue(ok))
	order, err := resolve.DeclOrder(lang, src)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(order,
		[]string{"Color", "Base", "Base.run", "gen", "varDecl", "N", "N.inner", "Ok", "Ok.hello"}))
}

func TestResolve_ImportsSpanInteriorComments(t *testing.T) {
	t.Parallel()
	// A grouping comment between two imports is an ordinary named sibling
	// in TypeScript and Python, and @imports spans it.
	py := []byte(`import os

# stdlib extras
import sys


def f():
    pass
`)
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".py", py, "@imports"),
		"import os\n\n# stdlib extras\nimport sys"))

	ts := []byte(`import a from 'a'

// external utils
import b from 'b'

export function f() {}
`)
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", ts, "@imports"),
		"import a from 'a'\n\n// external utils\nimport b from 'b'"))

	// A comment after the last import belongs to what follows, not to the
	// import block: end advances only on an import, so it stays outside.
	trailing := []byte(`import os

# note about f
def f():
    pass
`)
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".py", trailing, "@imports"), "import os"))
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

func TestResolve_UnsupportedLanguage(t *testing.T) {
	t.Parallel()
	// A symbol anchor on a file whose language has no grammar is the
	// caller's exit 9 (docs/ANCHORS.md § Language support); the resolver's
	// contribution is just reporting the extension unclaimed.
	_, ok := resolve.ForExtension(".rs")
	qt.Assert(t, qt.IsFalse(ok))

	// internal/diff/run.go's validateSym constructs exactly this shape --
	// Code set, no Candidates -- for the identical failure reached through
	// `rgit diff --sym`. Error() must actually say what docs/CODES.md's
	// exit-9 row promises ("Unsupported / deferred language for a symbol
	// anchor"), not silently fall through to the same "unresolved" label
	// exit 3 (AnchorUnresolvable) uses -- the code is right, the message
	// must not contradict it.
	err := &resolve.ResolveError{Code: exitcode.UnsupportedLanguage, Anchor: "main"}
	qt.Assert(t, qt.Equals(err.Error(), `resolve: "main": unsupported language`))
}

// --- Language-server cross-check -------------------------------------
//
// Coverage here is split by what can prove it: the union-shape decode and
// the anchor-normalization/comparison logic run against an in-process mock
// server, since a mock cannot catch a change in real language-server range
// semantics but can exercise every line of rgit's own decode and compare
// code cheaply and deterministically. The one thing only a real server can
// prove -- that gopls's actual reported ranges still match tree-sitter's
// normalized extent -- gets exactly one live case, skipped cleanly when
// gopls is absent or -short is set (CONTRIBUTING.md § Tests).

func TestLSP_DocumentSymbolsDecodesBothUnionShapes(t *testing.T) {
	t.Parallel()
	// go.lsp.dev/protocol's DocumentSymbolResult is a sealed union over
	// DocumentSymbolSlice (a tree, via Children -- gopls's hierarchical
	// mode) and SymbolInformationSlice (flat, with a Location and an
	// optional containerName). AGENTS.md: a client that assumes one shape
	// decodes the other wrongly, so both are exercised here against a real
	// (if hand-framed) wire exchange, not a stubbed union value.
	cases := []struct {
		name       string
		resultJSON string
		want       []lsp.Symbol
	}{
		{
			name: "DocumentSymbolSlice (tree, hierarchical)",
			resultJSON: `[{"name":"A","kind":6,"range":{"start":{"line":1,"character":0},"end":{"line":5,"character":1}},` +
				`"selectionRange":{"start":{"line":1,"character":6},"end":{"line":1,"character":7}},` +
				`"children":[{"name":"Get","kind":6,"range":{"start":{"line":3,"character":1},"end":{"line":3,"character":20}},` +
				`"selectionRange":{"start":{"line":3,"character":1},"end":{"line":3,"character":4}}}]}]`,
			want: []lsp.Symbol{
				{Name: "A", Container: "", StartLine: 1, EndLine: 5},
				{Name: "Get", Container: "A", StartLine: 3, EndLine: 3},
			},
		},
		{
			name: "SymbolInformationSlice (flat, with containerName)",
			resultJSON: `[{"name":"Get","kind":6,"containerName":"A",` +
				`"location":{"uri":"file:///tmp/a.go","range":{"start":{"line":3,"character":1},"end":{"line":3,"character":20}}}}]`,
			want: []lsp.Symbol{
				{Name: "Get", Container: "A", StartLine: 3, EndLine: 3},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serverConn, clientConn := net.Pipe()
			errCh := make(chan error, 1)
			go func() { errCh <- lsptest.ServeMockLSP(serverConn, tc.resultJSON, lsptest.MockServerHooks{}) }()

			client, err := lsp.NewClient(context.Background(), clientConn, t.TempDir())
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			defer func() { _ = client.Close() }()

			got, err := client.DocumentSymbols(context.Background(), "/tmp/a.go", []byte("package p\n"))
			if err != nil {
				t.Fatalf("DocumentSymbols: %v", err)
			}
			if srvErr := <-errCh; srvErr != nil {
				t.Fatalf("mock server: %v", srvErr)
			}
			qt.Assert(t, qt.DeepEquals(got, tc.want))
		})
	}
}

func TestResolve_CrossCheckMatchAndCompare(t *testing.T) {
	t.Parallel()
	src := []byte(`package p

// ValidateToken checks the JWT.
func ValidateToken(t string) error {
	return nil
}
`)
	res := mustResolve(t, src, "ValidateToken")

	t.Run("matching range confirms clean", func(t *testing.T) {
		found, err := resolve.MatchAndCompare(src, res, []lsp.Symbol{
			{Name: "ValidateToken", StartLine: 3, EndLine: 5},
		})
		qt.Assert(t, qt.IsTrue(found))
		qt.Assert(t, qt.IsNil(err))
	})

	t.Run("disagreement is exit 6 with both ranges attached", func(t *testing.T) {
		found, err := resolve.MatchAndCompare(src, res, []lsp.Symbol{
			{Name: "ValidateToken", StartLine: 3, EndLine: 6},
		})
		qt.Assert(t, qt.IsTrue(found))
		qt.Assert(t, qt.IsNotNil(err))
		var rerr *resolve.ResolveError
		qt.Assert(t, qt.ErrorAs(err, &rerr))
		qt.Assert(t, qt.Equals(rerr.Code, exitcode.ExtentMismatch))
		qt.Assert(t, qt.Equals(rerr.TreeSitterRange, "L4..L6"))
		qt.Assert(t, qt.Equals(rerr.LSPRange, "L4..L7"))
	})

	t.Run("server outline not naming the anchor degrades, is not an error", func(t *testing.T) {
		found, err := resolve.MatchAndCompare(src, res, nil)
		qt.Assert(t, qt.IsFalse(found))
		qt.Assert(t, qt.IsNil(err))
	})

	t.Run("gopls receiver spelling normalizes for comparison", func(t *testing.T) {
		methodSrc := []byte(`package p

type A struct{}

func (a *A) Get() int { return 1 }
`)
		getRes := mustResolve(t, methodSrc, "A.Get")
		// gopls reports no containerName for methods (docs/ANCHORS.md); the
		// receiver lives in the name string itself.
		found, err := resolve.MatchAndCompare(methodSrc, getRes, []lsp.Symbol{
			{Name: "(*A).Get", StartLine: 4, EndLine: 4},
		})
		qt.Assert(t, qt.IsTrue(found))
		qt.Assert(t, qt.IsNil(err))
	})

	t.Run("ordinal anchor matches the Nth same-named symbol in order", func(t *testing.T) {
		initSrc := []byte(`package p

func init() { println(1) }

func init() { println(2) }
`)
		second := mustResolve(t, initSrc, "init#2")
		found, err := resolve.MatchAndCompare(initSrc, second, []lsp.Symbol{
			{Name: "init", StartLine: 2, EndLine: 2},
			{Name: "init", StartLine: 4, EndLine: 4},
		})
		qt.Assert(t, qt.IsTrue(found))
		qt.Assert(t, qt.IsNil(err))
	})
}

func TestResolve_CrossCheckLiveGopls(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("live language-server cross-check skipped under -short")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := []byte(`package p

// ValidateToken checks the JWT.
// Returns ErrExpired if stale.
func ValidateToken(t string) error {
	return nil
}
`)
	path := filepath.Join(dir, "auth.go")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}

	res := mustResolve(t, src, "ValidateToken")
	lang := resolverGoLang(t)
	ctx := context.Background()

	// A fresh session per attempt, deliberately: a session remembers a
	// language that degraded and will not redial it, so reusing one here
	// would pin the first cold result forever. Separate sessions model
	// what this actually simulates -- successive rgit invocations, the
	// first spawning a daemon without waiting for it, since rgit never
	// blocks on a cold server.
	attempt := func(r *resolve.Resolution) (bool, error) {
		sess := lsp.NewSession()
		defer sess.Close()
		return crossCheckOne(ctx, sess, lang, dir, path, src, r)
	}

	var (
		degraded = true
		err      error
	)
	for deadline := time.Now().Add(3 * time.Second); ; {
		degraded, err = attempt(res)
		if !degraded || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if degraded {
		t.Skip("gopls daemon did not come up within the test's budget -- degraded, not a failure")
	}
	qt.Assert(t, qt.IsNil(err))

	// Corrupting DeclOnly.End forces a genuine disagreement, proving exit 6
	// fires against a real server's range, not only the mock-driven table
	// test above.
	mismatched := *res
	mismatched.DeclOnly.End -= 5
	_, mismatchErr := attempt(&mismatched)
	qt.Assert(t, qt.IsNotNil(mismatchErr))
	var rerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(mismatchErr, &rerr))
	qt.Assert(t, qt.Equals(rerr.Code, exitcode.ExtentMismatch))
}

// TestResolve_CrossCheckLiveHTML proves the wiring end to end against the
// real installed vscode-html-language-server, the same "real dependency
// over a double" bar TestResolve_CrossCheckLiveGopls holds gopls to
// (CONTRIBUTING.md § Tests). Unlike gopls, HTML has no daemon to warm up --
// every non-Go server here is spawned fresh per query and live on its first
// invocation (docs/INSTALL.md § Language servers) -- so this needs no
// polling loop, one attempt is the real behaviour.
func TestResolve_CrossCheckLiveHTML(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("live language-server cross-check skipped under -short")
	}
	if _, err := exec.LookPath("vscode-html-language-server"); err != nil {
		t.Skip("vscode-html-language-server not on PATH")
	}

	dir := t.TempDir()
	src := []byte(`<!DOCTYPE html>
<html>
<body>
<div id="app">
<input id="name" type="text">   trailing text with no sibling to stop it
<p id="after">next</p>
</div>
</body>
</html>
`)
	path := filepath.Join(dir, "fixture.html")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}

	lang, ok := resolve.ForExtension(".html")
	qt.Assert(t, qt.IsTrue(ok))
	ctx := context.Background()

	attempt := func(anchor string) (degraded bool, err error) {
		res, rerr := resolve.Resolve(lang, src, anchor)
		qt.Assert(t, qt.IsNil(rerr))
		sess := lsp.NewSession()
		defer sess.Close()
		return crossCheckOne(ctx, sess, lang, dir, path, src, res)
	}

	// An id-bearing element, including the void-element case declOnlyExtent's
	// own trim seam exists for, cross-checks clean against the real server --
	// not merely a mock's idea of one.
	for _, anchor := range []string{"div#app", "input#name", "p#after"} {
		degraded, err := attempt(anchor)
		qt.Assert(t, qt.IsFalse(degraded), qt.Commentf("anchor %q", anchor))
		qt.Assert(t, qt.IsNil(err), qt.Commentf("anchor %q", anchor))
	}

	// Class-bearing elements cross-check once the server's ".class…" suffix is
	// stripped from its Name; staged anchors stay tag#id.
	classSrc := []byte(`<div id="widget" class="foo bar">x</div>` + "\n")
	classPath := filepath.Join(dir, "class.html")
	if err := os.WriteFile(classPath, classSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	classRes, err := resolve.Resolve(lang, classSrc, "div#widget")
	qt.Assert(t, qt.IsNil(err))
	sess := lsp.NewSession()
	defer sess.Close()
	degraded, cerr := crossCheckOne(ctx, sess, lang, dir, classPath, classSrc, classRes)
	qt.Assert(t, qt.IsFalse(degraded))
	qt.Assert(t, qt.IsNil(cerr))
}

func TestResolve_MembersSharingANameAreOrdinal(t *testing.T) {
	t.Parallel()
	// TestResolve_MembersSharingANameAreOrdinal guards against two members
	// of one class producing the identical qualified name: without an
	// ordinal assigned among a container's own members, `Box.size` would
	// resolve silently to whichever one the index kept last, and the other
	// would become unaddressable with no ambiguity reported -- exactly what
	// exit 4 exists to say. A TypeScript get/set pair is the ordinary case,
	// not a corner.
	src := []byte("export class Box {\n  get size(): number { return 1; }\n  set size(n: number) { }\n  only(): number { return 3; }\n}\n")

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Box.size#1"), "get size(): number { return 1; }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Box.size#2"), "set size(n: number) { }"))

	// A member whose name is unique in its container keeps the plain form.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Box.only"), "only(): number { return 3; }"))

	// The container-qualified name without an ordinal is ambiguous, not
	// absent -- exitcode.AnchorAmbiguous plus a Candidates list rather than
	// a "did you mean". That error's own shape is already pinned generically
	// by TestResolve_OrdinalDisambiguatesRepeatedBareName's bare-name case;
	// this test's job is only the container-member half above, that the
	// ordinal actually resolves each member.
}

func TestResolve_SameNamedContainersDoNotMergeMembers(t *testing.T) {
	t.Parallel()
	// Both classes are named Svc, so both sets of members carried the same
	// container and collided the same way.
	src := []byte("class Svc { run(): number { return 1; } }\nclass Svc { run(): number { return 2; } }\n")

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Svc.run#1"), "run(): number { return 1; }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Svc.run#2"), "run(): number { return 2; }"))
}

func TestResolve_TypeScriptMultiDeclarator(t *testing.T) {
	t.Parallel()
	// Each declarator in a multi-declarator statement -- `const a = 1, b =
	// 2` -- must resolve to its own extent, not just the first: resolving
	// only the first declarator would report a change to b as a change to
	// a, a label that validates while naming a symbol nobody touched --
	// the same defect goSpecDeclarations guards against for Go's grouped
	// const/var/type blocks.
	src := []byte(`const a = 1, b = 2;
var m = 1, n = 2;
const {x, y} = obj;
const [p, q] = arr;
export const c = 1, d = 2;
`)

	// Each declarator in a grouped statement is addressed by its own extent
	// -- the declarator alone, not the keyword or the comma joining it to
	// its siblings, the same rule goSpecDeclarations applies to a grouped
	// Go spec.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "a"), "a = 1"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "b"), "b = 2"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "m"), "m = 1"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "n"), "n = 2"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "c"), "c = 1"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "d"), "d = 2"))

	// A destructuring declarator's "name" field is object_pattern or
	// array_pattern, not identifier -- there is no way to give x its own
	// extent without y's (and obj's) text coming along too, the same
	// principle as Go's shared "A, B int" field line. Reporting it
	// unanchorable is deliberate, not an oversight; none of the four names
	// below ever resolves.
	lang, ok := resolve.ForExtension(".ts")
	qt.Assert(t, qt.IsTrue(ok))
	for _, name := range []string{"x", "y", "p", "q"} {
		_, err := resolve.Resolve(lang, src, name)
		qt.Assert(t, qt.IsNotNil(err))
	}

	// The destructuring declarators never enter the index at all -- not
	// merely unresolvable under their own name, but absent from source
	// order too.
	order, err := resolve.DeclOrder(lang, src)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(order, []string{"a", "b", "m", "n", "c", "d"}))
}

func TestResolve_TypeScriptHeaderWithoutShebang(t *testing.T) {
	t.Parallel()
	// TypeScript has no package clause or other "this precedes code"
	// marker; a licence/copyright block at the top of a file with no
	// shebang is an ordinary "comment" node like any other. TypeScript's
	// HeaderKinds() therefore names "comment" alongside hash_bang_line, as
	// Python's does, or @header would resolve to nothing in such a file.
	src := []byte(`// Copyright 2026 Example Corp.
// SPDX-License-Identifier: MIT

/** Doc for F. */
export function F(): number { return 1 }
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "@header"),
		"// Copyright 2026 Example Corp.\n// SPDX-License-Identifier: MIT"))

	// The blank line between the licence block and F's own doc comment
	// keeps the two from merging into one comment run (docs/ANCHORS.md);
	// @header must not steal F's doc comment, and F must keep it.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "F"),
		"/** Doc for F. */\nexport function F(): number { return 1 }"))
}

func TestResolve_MarkdownSections(t *testing.T) {
	t.Parallel()
	// A section's extent is the whole subtree -- heading plus everything
	// under it, including nested subsections -- so naming a heading claims
	// its whole subtree, the same as naming a class claims its members.
	// Two "## Options" headings sharing a name but nesting under different
	// parents are not the same symbol and do not collide; two sharing both
	// a name and a parent do, and disambiguate with rgit's existing "#N"
	// ordinal rather than GitHub's "-1"/"-2" slug-dedupe suffix.
	src := []byte(`---
title: Doc
---

Lede paragraph before any heading.

# Install

Install content.

## Options

Install-specific options.

# Usage

Usage content.

## Options

First Usage options.

## Options

Second Usage options.
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".md", src, "install"),
		"# Install\n\nInstall content.\n\n## Options\n\nInstall-specific options.\n\n"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".md", src, "install.options"),
		"## Options\n\nInstall-specific options.\n\n"))

	// Same name, different parent: qualification is the nearest ancestor
	// heading only, so these are two distinct, unambiguous anchors.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".md", src, "usage.options#1"),
		"## Options\n\nFirst Usage options.\n\n"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".md", src, "usage.options#2"),
		"## Options\n\nSecond Usage options.\n"))

	lang, ok := resolve.ForExtension(".md")
	qt.Assert(t, qt.IsTrue(ok))

	_, err := resolve.Resolve(lang, src, "usage.options")
	var rerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &rerr))
	qt.Assert(t, qt.Equals(rerr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(rerr.Candidates, []string{"usage.options#1", "usage.options#2"}))

	// @header is frontmatter alone -- the lede paragraph that follows it is
	// not part of @header, even though both precede the first heading.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".md", src, "@header"), "---\ntitle: Doc\n---\n"))

	// @toplevel is the lede -- content between @header and the first
	// heading -- not "first heading through end of document", which is what
	// the shared declaration-span formula every other language uses would
	// otherwise compute here (sectionDeclarations names no declaration for
	// the lede).
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".md", src, "@toplevel"),
		"\nLede paragraph before any heading.\n\n"))

	// A second fixture isolates the shapes above from a fenced code block
	// containing a line that looks like a heading, and a setext heading.
	src2 := []byte("# Diff Scope\n\nSome intro.\n\n```bash\n# not a heading, inside a fence\necho hi\n```\n\nSetext Title\n============\n\nBody after the setext heading.\n")

	// The "#" inside the fence must never be read as a heading: naming the
	// whole section is the only way to touch it.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".md", src2, "diff-scope"),
		"# Diff Scope\n\nSome intro.\n\n```bash\n# not a heading, inside a fence\necho hi\n```\n\n"+
			"Setext Title\n============\n\nBody after the setext heading.\n"))

	// A setext heading is addressable by its own slug, but unlike an atx
	// heading it never opens its own section, so its extent is the heading
	// line alone, not a header-plus-body span: the following paragraph
	// belongs to the enclosing "diff-scope" section instead.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".md", src2, "diff-scope.setext-title"),
		"Setext Title\n============\n"))

	// rgit always emits the slug, but accepts a heading's own raw text on
	// input, the same way it accepts gopls's "(*A).Get" spelling -- whether
	// or not that text contains a space. A single-word heading's raw text
	// ("Install") must resolve exactly like a multi-word one ("Diff Scope");
	// gating the fallback on a literal space would make the single-word
	// case unresolvable for no reason a caller could act on.
	lang2, ok := resolve.ForExtension(".md")
	qt.Assert(t, qt.IsTrue(ok))
	res, err := resolve.Resolve(lang2, src2, "Diff Scope")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(res.Anchor, "diff-scope"))

	res2, err := resolve.Resolve(lang2, src, "Install")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(res2.Anchor, "install"))

	// A single-word raw heading that names several headings at once must
	// still be ambiguous (exit 4), never silently resolve one of them: three
	// "Options" headings exist in src (install.options, usage.options#1,
	// usage.options#2), so raw text "Options" collides the same way its
	// slug does.
	_, err = resolve.Resolve(lang2, src, "Options")
	var ambigErr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &ambigErr))
	qt.Assert(t, qt.Equals(ambigErr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(ambigErr.Candidates,
		[]string{"install.options", "usage.options#1", "usage.options#2"}))
}

// TestResolve_MDXHeadingsUnaffectedByJSX pins the one thing .mdx registration
// rests on: MDX's own additions carry no heading, so they land in an ordinary
// paragraph or html_block and a heading extent is byte-identical to what the
// same content would resolve to in plain Markdown.
func TestResolve_MDXHeadingsUnaffectedByJSX(t *testing.T) {
	t.Parallel()

	src := []byte(`import { Callout } from "@/components/callout"

# Install

<Callout type="warn">Read this first.</Callout>

## Options

Body {frontmatter.title} text.

# Usage

Usage content.
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".mdx", src, "install"),
		"# Install\n\n<Callout type=\"warn\">Read this first.</Callout>\n\n## Options\n\nBody {frontmatter.title} text.\n\n"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".mdx", src, "install.options"),
		"## Options\n\nBody {frontmatter.title} text.\n\n"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".mdx", src, "usage"),
		"# Usage\n\nUsage content.\n"))

	// The leading ESM import is not an @imports anchor: markdown inherits
	// defaultLanguage's empty ImportKinds, so it stays part of @toplevel.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".mdx", src, "@toplevel"),
		"import { Callout } from \"@/components/callout\"\n\n"))

	mdx, ok := resolve.ForExtension(".mdx")
	qt.Assert(t, qt.IsTrue(ok))
	md, ok := resolve.ForExtension(".md")
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(mdx.Name(), md.Name()))
}

// TestResolve_YAML covers the grammar's own shapes in one pass: a realistic
// workflow fixture for nesting/qualification/comment-grafting/pseudo-anchors,
// then edge shapes a realistic fixture never exercises on its own (anchors/
// aliases, quoted keys, flow-style leaves, ordinal disambiguation across
// different grandparents, the no-addressable-mapping refusals, and the key
// spellings yamlKeyName does and does not turn into a Bare name).
func TestResolve_YAML(t *testing.T) {
	t.Parallel()
	// A realistic GitHub Actions workflow: several jobs, nested steps, a
	// block scalar `run: |`, a flow sequence, and a comment sitting between
	// the end of a nested job and the next, more shallowly indented one.
	src := []byte(`# leading header comment

name: CI

on:
  push:
    branches: [main]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Build
        run: |
          go build ./...
          go vet ./...

  # a comment between build and test
  test:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "@header"), "# leading header comment"))

	// "jobs.build" is the nearest-ancestor qualification the target spelling
	// asks for: the whole job's subtree, block scalar included verbatim.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "jobs.build"),
		"build:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n"+
			"      - name: Build\n        run: |\n          go build ./...\n          go vet ./..."))

	// A third level of nesting is qualified by its own immediate parent
	// only, "build.runs-on", never the accumulated "jobs.build.runs-on" --
	// Declaration carries one Container field, not a path, the same
	// nearest-ancestor rule lang_markdown.go's sectionDeclarations already
	// uses for nested headings.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "build.runs-on"), "runs-on: ubuntu-latest"))

	// A sequence value is addressable as a whole -- "build.steps" claims the
	// entire list -- but never per item: there is no name to address one by,
	// the same reasoning Go's shared "A, B int" field line is left
	// unaddressable for.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "build.steps"),
		"steps:\n      - uses: actions/checkout@v4\n      - name: Build\n        run: |\n"+
			"          go build ./...\n          go vet ./..."))

	// The comment between "build"'s last nested line and "test:" is not
	// "test:"'s leading trivia: tree-sitter-yaml's own external scanner
	// grafts a comment preceding a multi-level dedent onto whichever block
	// was still open when it consumed the comment token, regardless of the
	// comment's own written column (lang_yaml.go's trimTrailingComment).
	// "jobs.build" excludes it (trimmed off
	// its trailing edge, so an edit to "build" alone never silently carries
	// a comment written for its neighbour), and "jobs.test" never had it as
	// a real sibling to begin with, so neither key's own extent claims it.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "jobs.test"),
		"test:\n    needs: build\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"))

	// @toplevel still reaches the orphaned comment: its widening bounds to
	// the enclosing document node (pseudo.go's shared "first through last
	// declaration" formula), which always spans to the document's own true
	// end regardless of where any interior comment got grafted.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "@toplevel"),
		"name: CI\n\non:\n  push:\n    branches: [main]\n\njobs:\n  build:\n    runs-on: ubuntu-latest\n"+
			"    steps:\n      - uses: actions/checkout@v4\n      - name: Build\n        run: |\n"+
			"          go build ./...\n          go vet ./...\n\n"+
			"  # a comment between build and test\n  test:\n    needs: build\n"+
			"    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"))

	lang, ok := resolve.ForExtension(".yml")
	qt.Assert(t, qt.IsTrue(ok))

	// Naming a bare sequence item (no such anchor exists to type in the
	// first place) refuses honestly rather than silently matching something
	// else.
	_, err := resolve.Resolve(lang, src, "build.steps.0")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// .yaml is claimed too.
	_, ok = resolve.ForExtension(".yaml")
	qt.Assert(t, qt.IsTrue(ok))

	// A second fixture isolates shapes the realistic workflow above never
	// exercises: a quoted key, an anchor/alias pair riding along verbatim
	// inside whatever key contains them, a flow-style mapping value left
	// undescended, and two containers of the same name at different
	// grandparents colliding the same way lang_markdown.go's two "Options"
	// headings under different parents already do.
	edgeShapes := []byte(`defaults: &defaults
  adapter: postgres

development:
  <<: *defaults
  "quoted key": ok
  flow: { a: 1, b: 2 }

a:
  common:
    port: 1
b:
  common:
    port: 2
`)

	// The anchor and its later alias are never split around: naming
	// "defaults" claims the "&defaults" marker as part of its own value,
	// and "<<: *defaults" is an ordinary key ("<<") whose value is the
	// alias, preserved byte-for-byte.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", edgeShapes, "defaults"),
		"defaults: &defaults\n  adapter: postgres"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", edgeShapes, "development.<<"), "<<: *defaults"))

	// A quoted key's surrounding quote byte is stripped from Bare -- a
	// best-effort unwrap, not full YAML unescaping.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", edgeShapes, "development.quoted key"), `"quoted key": ok`))

	// A flow-style mapping value is a leaf: "development.flow" claims the
	// whole `{ a: 1, b: 2 }`, but there is no "development.flow.a" to
	// address -- flow style is never descended into, at any depth.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", edgeShapes, "development.flow"), "flow: { a: 1, b: 2 }"))
	_, err = resolve.Resolve(lang, edgeShapes, "development.flow.a")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// "a.common" and "b.common" are different containers, so "common.port"
	// under each collides in qualified name even though the two are nowhere
	// near each other in the tree -- ordinal disambiguation, not a merge.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", edgeShapes, "common.port#1"), "port: 1"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", edgeShapes, "common.port#2"), "port: 2"))

	_, err = resolve.Resolve(lang, edgeShapes, "common.port")
	var ambigErr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &ambigErr))
	qt.Assert(t, qt.Equals(ambigErr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(ambigErr.Candidates, []string{"common.port#1", "common.port#2"}))

	// A "---"-separated multi-document stream has no addressable key at all,
	// rather than guessing which document a bare key path means.
	multiDoc := []byte("name: A\n---\nname: B\n")
	_, err = resolve.Resolve(lang, multiDoc, "name")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// topBlockMapping refuses two more shapes with nothing addressable at
	// all: a document whose only content is a bare scalar, or a sequence
	// with no mapping anywhere in it -- neither has a key to qualify a
	// Declaration with.
	for _, noMapping := range []string{
		"just a scalar\n",
		"- one\n- two\n",
		"# only a comment, no document at all\n",
	} {
		_, err = resolve.Resolve(lang, []byte(noMapping), "one")
		qt.Assert(t, qt.ErrorAs(err, &unresolvable), qt.Commentf("src %q", noMapping))
		qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))
	}

	// The comment-only file still has no document at all (soleDocument's
	// doc==nil case), but @header does not depend on there being one.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", []byte("# only a comment, no document at all\n"), "@header"),
		"# only a comment, no document at all"))

	// The key spellings yamlKeyName does and does not turn into a Bare
	// name: both quote styles, and an explicit "?" key whose own value is
	// a nested block rather than a scalar -- its key field is a
	// "block_node", not the ordinary "flow_node" every plain or quoted key
	// parses as, so it is left unaddressable rather than resolved to an
	// invented spelling. "plain" is unaffected by its refused sibling.
	keyShapes := []byte("'single quoted': ok\n" +
		"?\n  a: 1\n  b: 2\n: value\n" +
		"plain: fine\n")

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", keyShapes, "single quoted"), "'single quoted': ok"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", keyShapes, "plain"), "plain: fine"))

	_, err = resolve.Resolve(lang, keyShapes, "a")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))
}

func TestResolve_Shell(t *testing.T) {
	t.Parallel()
	// Both function forms, a top-level var, source lines in two spellings, a
	// heredoc whose body only looks like a function definition, and a
	// redefinition.
	src := []byte(`#!/usr/bin/env bash
source ./lib.sh
. ./other.sh

TOP_VAR=1

foo() {
  echo "foo"
}

function bar {
  echo "bar"
}

cat <<'EOF2'
function fake_in_heredoc() {
  echo "not real"
}
EOF2

foo() {
  echo "redefined foo"
}
`)

	// A shebang parses as an ordinary comment (no dedicated node), so
	// @header inherits Python's semantics with nothing new to add.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sh", src, "@header"), "#!/usr/bin/env bash"))

	// @imports spans both source forms and nothing either side of them --
	// not TOP_VAR ahead, and not the heredoc's look-alike function behind.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sh", src, "@imports"),
		"source ./lib.sh\n. ./other.sh"))

	// Both surface forms of a function definition are addressable by bare
	// name, and the flat namespace disambiguates a redefinition with the
	// same #N ordinal every other language uses -- no shell-specific
	// disambiguation of its own.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sh", src, "foo#1"), "foo() {\n  echo \"foo\"\n}"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sh", src, "foo#2"), "foo() {\n  echo \"redefined foo\"\n}"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sh", src, "bar"), "function bar {\n  echo \"bar\"\n}"))

	lang, ok := resolve.ForExtension(".sh")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "foo")
	var ambigErr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &ambigErr))
	qt.Assert(t, qt.Equals(ambigErr.Code, exitcode.AnchorAmbiguous))

	// A bare top-level assignment is addressable.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sh", src, "TOP_VAR"), "TOP_VAR=1"))

	// The heredoc body is a real node the parser never mistakes for a
	// sibling declaration: "fake_in_heredoc" names nothing.
	_, err = resolve.Resolve(lang, src, "fake_in_heredoc")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// .bash is claimed too; .zsh deliberately is not (lang_shell.go) --
	// tree-sitter-bash mis-parses zsh-only syntax.
	_, ok = resolve.ForExtension(".bash")
	qt.Assert(t, qt.IsTrue(ok))
	_, ok = resolve.ForExtension(".zsh")
	qt.Assert(t, qt.IsFalse(ok))
}

// TestResolve_CSS covers the grammar's own shapes in one pass: a realistic
// stylesheet for selectors/at-rules/pseudo-anchors, then edge shapes a
// realistic fixture never exercises on its own (a comma-joined selector
// list staging as one anchor, native CSS Nesting, and a real at-rule
// spelled like a pseudo-anchor never resolving as itself).
func TestResolve_CSS(t *testing.T) {
	t.Parallel()
	// A realistic small stylesheet: a leading comment, three selector
	// shapes, an @import, an @media block whose nested rule is not itself
	// addressable, and a generic at-rule with no prelude.
	src := []byte(`/* Global styles */

@import "reset.css";

.btn {
  color: red;
}

#app {
  display: flex;
}

div {
  margin: 0;
}

@media (max-width: 600px) {
  .btn { color: blue; }
}

@font-face {
  font-family: "MyFont";
}
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", src, "@header"), "/* Global styles */"))

	// A selector's bare name is its own text as written -- the leading "."
	// or "#" included, not stripped the way a Go identifier never carries
	// punctuation to begin with.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", src, ".btn"), ".btn {\n  color: red;\n}"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", src, "#app"), "#app {\n  display: flex;\n}"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", src, "div"), "div {\n  margin: 0;\n}"))

	// @imports spans the whole import_statement, semicolon included -- the
	// pseudo-anchor's extent, not the trimmed name cssAtRuleName computes
	// for a bare-addressable at-rule.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", src, "@imports"), `@import "reset.css";`))

	// An @import is reachable only via @imports, never as a bare anchor of
	// its own name -- the same exclusion Go's import_declaration gets.
	lang, ok := resolve.ForExtension(".css")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, `@import "reset.css"`)
	qt.Assert(t, qt.IsNotNil(err))
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// An at-rule's bare name is its full prelude, not the bare keyword --
	// two "@media" blocks in one file would otherwise collide even though
	// their preludes differ. The nested ".btn" inside the block is not
	// itself addressable; naming "@media (max-width: 600px)" claims the
	// whole thing, block included.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", src, "@media (max-width: 600px)"),
		"@media (max-width: 600px) {\n  .btn { color: blue; }\n}"))

	// A generic at-rule with no prelude at all degrades to the bare
	// keyword.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", src, "@font-face"),
		"@font-face {\n  font-family: \"MyFont\";\n}"))

	// @toplevel spans every addressable rule, first through last --
	// excluding the leading comment (@header) and the @import (reachable
	// only via @imports).
	toplevel := mustResolveExt(t, ".css", src, "@toplevel")
	qt.Assert(t, qt.StringContains(toplevel, ".btn {\n  color: red;\n}"))
	qt.Assert(t, qt.StringContains(toplevel, "@font-face"))
	qt.Assert(t, qt.Not(qt.StringContains(toplevel, "Global styles")))
	qt.Assert(t, qt.Not(qt.StringContains(toplevel, "@import")))

	// .css is claimed; .scss and .sass deliberately are not -- no SCSS/SASS
	// tree-sitter grammar ships Go bindings.
	_, ok = resolve.ForExtension(".scss")
	qt.Assert(t, qt.IsFalse(ok))
	_, ok = resolve.ForExtension(".sass")
	qt.Assert(t, qt.IsFalse(ok))

	// docs/ANCHORS.md's claim that a comma-joined selector list stages as
	// one anchor, not two -- the fixture above only ever exercises single
	// selectors.
	commaList := []byte(`.a, .b {
  color: red;
}

.a {
  color: blue;
}
`)

	// The comma-joined selector's own bare name is its full text, verbatim,
	// not decomposed into ".a" and ".b" separately.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", commaList, ".a, .b"),
		".a, .b {\n  color: red;\n}"))

	// A lone ".a" elsewhere in the file is its own, unrelated rule -- the
	// comma list is not reachable through either of its own parts.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", commaList, ".a"), ".a {\n  color: blue;\n}"))

	_, err = resolve.Resolve(lang, commaList, ".b")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// Native CSS Nesting (tree-sitter-css v0.25.0): a rule_set directly
	// inside another rule_set's own block is now addressable, qualified by
	// its immediate parent's own selector text through a literal space --
	// the descendant combinator CSS itself would use to flatten the same
	// nesting -- not the dot every other adapter's own Container
	// convention joins with. This is a different construct from the
	// @media/@supports/@keyframes case above and does not change that: an
	// at-rule encountered while descending a rule_set's block (or a
	// rule_set found inside an at-rule's own block) stays exactly as
	// undescended as before.
	nested := []byte(`.parent {
  color: red;

  .child {
    color: blue;
  }
}

.outer {
  .mid {
    .inner {
      color: purple;
    }
  }
}

.wrap {
  @media (max-width: 600px) {
    .leaf {
      color: green;
    }
  }
}
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", nested, ".child"), ".child {\n    color: blue;\n  }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", nested, ".parent .child"), ".child {\n    color: blue;\n  }"))
	// Naming the parent still claims the nested rule along with it.
	qt.Assert(t, qt.StringContains(mustResolveExt(t, ".css", nested, ".parent"), ".child"))

	// Nesting three deep qualifies by the immediate parent only, the same
	// one-level rule lang_yaml.go and lang_json.go already apply: ".inner"
	// resolves unambiguously on its own, and its qualified form names ".mid",
	// never the full ".outer .mid .inner" chain.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", nested, ".inner"), ".inner {\n      color: purple;\n    }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", nested, ".mid .inner"), ".inner {\n      color: purple;\n    }"))
	_, err = resolve.Resolve(lang, nested, ".outer .mid .inner")
	qt.Assert(t, qt.IsNotNil(err))

	// A rule_set nested inside an @media block inside a rule_set is still
	// not addressable at all: at-rule non-descent applies regardless of
	// what encloses the at-rule, or what the at-rule itself encloses.
	_, err = resolve.Resolve(lang, nested, ".leaf")
	qt.Assert(t, qt.IsNotNil(err))
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// The deliberate sharp edge docs/ANCHORS.md documents: an at-rule
	// spelled like a pseudo-anchor ("@header") never resolves as itself,
	// because Resolve checks isPseudoAnchor before it ever consults the
	// symbol index (resolver.go). The colliding at-rule is not merely
	// deprioritized -- it is unreachable by that spelling under any
	// circumstance.
	pseudoShadow := []byte(`/* real header */

.btn {
  color: red;
}

@header {
  color: green;
}
`)

	res, err := resolve.Resolve(lang, pseudoShadow, "@header")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(res.Pseudo))
	qt.Assert(t, qt.Equals(string(pseudoShadow[res.Extent.Start:res.Extent.End]), "/* real header */"))

	// The colliding at-rule still exists in source order -- DeclOrder lists
	// it under its own qualified name "@header" -- but that spelling can
	// never resolve to it: isPseudoAnchor wins first, every time.
	order, err := resolve.DeclOrder(lang, pseudoShadow)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(order, []string{".btn", "@header"}))
}

func TestResolve_JSON(t *testing.T) {
	t.Parallel()
	// A realistic config-shaped document: a nested object (container
	// qualification), an array (a leaf, never descended), and a top-level
	// scalar.
	src := []byte(`{
  "name": "example",
  "server": {
    "port": 8080,
    "host": "localhost"
  },
  "list": [1, 2, 3]
}
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".json", src, "name"), `"name": "example"`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".json", src, "server.port"), `"port": 8080`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".json", src, "server.host"), `"host": "localhost"`))

	// Naming the container claims the whole nested object.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".json", src, "server"),
		"\"server\": {\n    \"port\": 8080,\n    \"host\": \"localhost\"\n  }"))

	// An array is a leaf -- "list" addresses the whole array, but there is
	// no "list.0" to address one element by.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".json", src, "list"), `"list": [1, 2, 3]`))

	lang, ok := resolve.ForExtension(".json")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "list.0")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// JSON has no comment syntax, so @header and @imports both resolve to
	// nothing -- the same degraded-but-not-an-error result Markdown gives a
	// file with no shebang.
	_, err = resolve.Resolve(lang, src, "@header")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	_, err = resolve.Resolve(lang, src, "@imports")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))

	// @toplevel reaches the whole document -- there is no header or import
	// material to exclude, the same as YAML once its own @header is set
	// aside.
	toplevel := mustResolveExt(t, ".json", src, "@toplevel")
	qt.Assert(t, qt.StringContains(toplevel, `"name": "example"`))
	qt.Assert(t, qt.StringContains(toplevel, `"list": [1, 2, 3]`))

	// A bare top-level array has no key to address at all.
	_, ok = resolve.ForExtension(".json")
	qt.Assert(t, qt.IsTrue(ok))
	_, err = resolve.Resolve(lang, []byte("[1, 2, 3]\n"), "name")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
}

// TestResolve_TOML covers the grammar's own shapes in one pass: a realistic
// config file for tables/comments/array-of-tables ambiguity, then edge
// shapes it never exercises on its own (the key spellings tomlKeyName does
// and does not turn into a Bare name, and a dotted table header qualifying
// its members).
func TestResolve_TOML(t *testing.T) {
	t.Parallel()
	// A realistic config file: a leading comment, a bare top-level pair, a
	// "[table]" whose members include one separated from its neighbour by
	// an own-line comment, and an array of tables ("[[servers]]") whose two
	// elements share one header spelling.
	src := []byte(`# leading comment

title = "example"

[server]
port = 8080

# comment for host
host = "localhost"

[[servers]]
name = "a"

[[servers]]
name = "b"
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "@header"), "# leading comment"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "title"), `title = "example"`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "server.port"), "port = 8080"))

	// The comment sitting directly above "host" with no blank line between
	// them attaches to it -- the shared blank-line rule (docs/ANCHORS.md),
	// unmodified for TOML.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "server.host"),
		"# comment for host\nhost = \"localhost\""))

	// Naming a table claims the whole table, header through its last
	// member -- including the blank line before the next section header,
	// which the grammar attributes to the table node itself: "table"'s own
	// EndByte reaches the byte immediately before "[[servers]]" starts, not
	// the end of "host"'s own line.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "server"),
		"[server]\nport = 8080\n\n# comment for host\nhost = \"localhost\"\n\n"))

	// Two "[[servers]]" elements share one header spelling and collide the
	// same way two same-named Go functions do: ambiguous (exit 4), not a
	// silent pick of one, both for the table's own anchor and its member's.
	lang, ok := resolve.ForExtension(".toml")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "servers")
	var ambigErr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &ambigErr))
	qt.Assert(t, qt.Equals(ambigErr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(ambigErr.Candidates, []string{"servers#1", "servers#2"}))

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "servers#1"), "[[servers]]\nname = \"a\"\n\n"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "servers.name#1"), `name = "a"`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "servers.name#2"), `name = "b"`))

	// TOML has no include/import directive of any kind.
	_, err = resolve.Resolve(lang, src, "@imports")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))

	// .toml is claimed.
	_, ok = resolve.ForExtension(".toml")
	qt.Assert(t, qt.IsTrue(ok))

	// The key spellings tomlKeyName does and does not turn into a Bare
	// name, isolated from the realistic fixture above: a quoted key, a
	// dotted pair key left undecomposed, an inline table left undescended,
	// and an array left undescended.
	keyShapes := []byte(`"quoted key" = 1
inline = { a = 1, b = 2 }
arr = [1, 2, 3]
dotted.pair = 1
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", keyShapes, "quoted key"), `"quoted key" = 1`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", keyShapes, "inline"), "inline = { a = 1, b = 2 }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", keyShapes, "arr"), "arr = [1, 2, 3]"))

	// A dotted pair key is not decomposed into container.bare -- its Bare is
	// the full dotted spelling, one predictable rule rather than a second
	// qualification scheme layered on top of the "[table]"-header one.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", keyShapes, "dotted.pair"), "dotted.pair = 1"))

	_, err = resolve.Resolve(lang, keyShapes, "inline.a")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// docs/ANCHORS.md's claim that a dotted table header ("[server.tls]")
	// qualifies its members as "server.tls.<key>", not a further-nested
	// "server.tls.tls.<key>" path -- the fixtures above only exercise a
	// plain "[server]" header and a root-level dotted pair
	// ("dotted.pair"), so a header's own dotted spelling qualifying its
	// members was a documented guarantee with no test actually driving it.
	dottedHeader := []byte(`[server.tls]
cert = "a.pem"
`)

	// The header's own dotted spelling is the container verbatim -- not
	// decomposed into "server" containing a nested "tls".
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", dottedHeader, "server.tls"), "[server.tls]\ncert = \"a.pem\"\n"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", dottedHeader, "server.tls.cert"), `cert = "a.pem"`))
	// Unambiguous on its own, the bare member name resolves too.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", dottedHeader, "cert"), `cert = "a.pem"`))

	_, err = resolve.Resolve(lang, dottedHeader, "server.tls.tls.cert")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))
}

// TestResolve_HTML pins the deliberately narrow HTML anchor syntax:
// element + id only ("div#app"), nested to whatever depth the worktree
// actually nests it, with a doc comment attributed the same way every
// other language's is, and @header/@imports/@toplevel each given a
// considered answer -- including @imports, which does not apply and says
// so by degrading to unresolved rather than silently matching nothing
// useful.
func TestResolve_HTML(t *testing.T) {
	t.Parallel()
	src := []byte(`<!DOCTYPE html>
<html>
<head><title>T</title></head>
<body>
<!-- mount point -->
<div id="app" class="widget">
  <section id="content">
    <p>hi</p>
  </section>
  <input id="field" type="text">
</div>
</body>
</html>
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".html", src, "@header"), "<!DOCTYPE html>"))

	// tag#id resolves regardless of nesting depth, container-qualified by
	// its own tag name (Declaration.Sep "#" is what produces this exact
	// spelling, not a second join rule).
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".html", src, "section#content"),
		"<section id=\"content\">\n    <p>hi</p>\n  </section>"))

	// Naming the outer element claims everything nested inside it,
	// including the leading comment attributed to it as a doc comment (no
	// blank line separates them) -- the same attribution rule every other
	// language's fullExtent already applies.
	div := mustResolveExt(t, ".html", src, "div#app")
	qt.Assert(t, qt.StringContains(div, "<!-- mount point -->"))
	qt.Assert(t, qt.StringContains(div, "<section id=\"content\">"))
	qt.Assert(t, qt.StringContains(div, "<input id=\"field\""))

	// A void element resolves to its own tag alone, not the rest of the
	// file -- proof that tree-sitter-html's own void-element trailing-
	// content quirk never crosses into a sibling's bytes, only ever
	// absorbs harmless trailing whitespace.
	field := mustResolveExt(t, ".html", src, "input#field")
	qt.Assert(t, qt.StringContains(field, "<input id=\"field\" type=\"text\">"))
	qt.Assert(t, qt.Not(qt.StringContains(field, "</div>")))
	qt.Assert(t, qt.Not(qt.StringContains(field, "</section>")))

	// An element with no id is not addressable at all -- not even the sole
	// <title> or <body> in the document -- element+id was deliberately kept
	// this narrow rather than falling back to a bare tag name.
	lang, ok := resolve.ForExtension(".html")
	qt.Assert(t, qt.IsTrue(ok))
	for _, anchor := range []string{"title", "body", "p", "div"} {
		_, err := resolve.Resolve(lang, src, anchor)
		var unresolvable *resolve.ResolveError
		qt.Assert(t, qt.ErrorAs(err, &unresolvable), qt.Commentf("anchor %q", anchor))
		qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))
	}

	// @imports does not apply: no node kind in this grammar plays the role
	// of an import statement, so it degrades to unresolved rather than
	// matching <link>/<script src> guesswork (docs/ANCHORS.md).
	_, err := resolve.Resolve(lang, src, "@imports")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// @toplevel spans every addressable element -- here, the whole <html>
	// element, since div#app (the outermost declaration) sits inside it.
	toplevel := mustResolveExt(t, ".html", src, "@toplevel")
	qt.Assert(t, qt.StringContains(toplevel, "<html>"))
	qt.Assert(t, qt.StringContains(toplevel, "</html>"))
	qt.Assert(t, qt.Not(qt.StringContains(toplevel, "<!DOCTYPE")))

	// DeclOrder lists every id-bearing element in source order, and only
	// those -- confirming the id-less <html>/<head>/<title>/<body>/<p>
	// wrappers were walked through, not staged as declarations of their own.
	order, err := resolve.DeclOrder(lang, src)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(order, []string{"div#app", "section#content", "input#field"}))
}

// TestResolve_HTMLDuplicateIDIsAmbiguous pins the second HTML decision: an
// id is unique per document by spec but routinely is not in practice, and
// index.go's existing byBare/byQualified/ordinal machinery already
// reports that as exit 4 (AnchorAmbiguous) with no HTML-specific code at
// all -- the same mechanism two identical CSS selectors or two same-named
// Go functions already share.
func TestResolve_HTMLDuplicateIDIsAmbiguous(t *testing.T) {
	t.Parallel()
	src := []byte(`<div id="app"></div>
<span id="app"></span>
<div id="hero"></div>
<div id="hero"></div>
`)
	lang, ok := resolve.ForExtension(".html")
	qt.Assert(t, qt.IsTrue(ok))

	// Two different tags sharing one id: the bare id alone is ambiguous,
	// each tag-qualified spelling resolves unambiguously on its own.
	_, err := resolve.Resolve(lang, src, "app")
	var ambiguous *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &ambiguous))
	qt.Assert(t, qt.Equals(ambiguous.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(ambiguous.Candidates, []string{"div#app", "span#app"}))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".html", src, "div#app"), `<div id="app"></div>`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".html", src, "span#app"), `<span id="app"></span>`))

	// The same tag and id repeated: even the tag-qualified spelling
	// collides, and the existing ordinal fallback disambiguates it exactly
	// the way two same-named Go package-level functions already do.
	_, err = resolve.Resolve(lang, src, "hero")
	qt.Assert(t, qt.ErrorAs(err, &ambiguous))
	qt.Assert(t, qt.Equals(ambiguous.Code, exitcode.AnchorAmbiguous))
	_, err = resolve.Resolve(lang, src, "div#hero")
	qt.Assert(t, qt.ErrorAs(err, &ambiguous))
	qt.Assert(t, qt.Equals(ambiguous.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(ambiguous.Candidates, []string{"div#hero#1", "div#hero#2"}))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".html", src, "div#hero#1"), `<div id="hero"></div>`))
}

func TestResolve_HTMLDuplicateTagIDOrdinal(t *testing.T) {
	t.Parallel()
	src := []byte(`<div id="app">first</div>
<div id="app">second</div>
`)

	qt.Assert(t, qt.Equals(
		mustResolveExt(t, ".html", src, "div#app#2"),
		`<div id="app">second</div>`,
	))
}

// TestResolve_HTMLVoidElementDeclOnlyTrimmed pins the seam declOnlyExtent
// consults for HTML alone: a void element followed by inline text with no
// enclosing tag or sibling element to stop it (unlike TestResolve_HTML's own
// "<input ...>\n</div>" fixture, where the enclosing tag's own close already
// bounds it) absorbs that text into its own node's EndByte() -- left alone
// in the extent that gets staged (Extent, TestResolve_HTML's own coverage),
// but trimmed back to the start_tag's own end in DeclOnly, the extent the
// later LSP cross-check compares against a server that never reports that
// absorbed text as part of the element either.
func TestResolve_HTMLVoidElementDeclOnlyTrimmed(t *testing.T) {
	t.Parallel()
	src := []byte(`<div id="app">
<input id="name" type="text">   trailing text with no sibling to stop it
<p id="after">next</p>
</div>
`)
	lang, ok := resolve.ForExtension(".html")
	qt.Assert(t, qt.IsTrue(ok))

	res, err := resolve.Resolve(lang, src, "input#name")
	qt.Assert(t, qt.IsNil(err))

	declOnly := string(src[res.DeclOnly.Start:res.DeclOnly.End])
	qt.Assert(t, qt.Equals(declOnly, `<input id="name" type="text">`))

	full := string(src[res.Extent.Start:res.Extent.End])
	qt.Assert(t, qt.StringContains(full, "trailing text with no sibling to stop it"),
		qt.Commentf("Extent (what gets staged) keeps the grammar's own absorption -- only DeclOnly (cross-check) is trimmed"))

	// An element with a real end_tag is unaffected: DeclOnly and Extent
	// agree, nothing to trim.
	pRes, err := resolve.Resolve(lang, src, "p#after")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(pRes.DeclOnly, pRes.Extent))
}

// TestResolve_PythonMultiLineAssignmentDeclOnlyTrimmed pins the seam
// declOnlyExtent consults for Python assignments: pyright names the binding's
// own line where tree-sitter names the whole statement, so a multi-line
// literal was a hard extent mismatch (exit 6) and the anchor became
// unstageable the moment pyright was installed. DeclOnly ends with the first
// line; Extent -- what actually gets staged -- is still the whole statement.
//
// A function keeps DeclOnly == Extent, which is the reason this is scoped to
// expression_statement rather than applied to every declaration: a genuine
// one-line extent bug on a def must still fail.
func TestResolve_PythonMultiLineAssignmentDeclOnlyTrimmed(t *testing.T) {
	t.Parallel()
	src := []byte(`CONFIG = {
    "a": 1,
    "b": 2,
}

SINGLE = 1


def validate(tok):
    return bool(tok)
`)
	lang, ok := resolve.ForExtension(".py")
	qt.Assert(t, qt.IsTrue(ok))

	res, err := resolve.Resolve(lang, src, "CONFIG")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(string(src[res.DeclOnly.Start:res.DeclOnly.End]), "CONFIG = {"))
	qt.Assert(t, qt.Equals(string(src[res.Extent.Start:res.Extent.End]),
		"CONFIG = {\n    \"a\": 1,\n    \"b\": 2,\n}"),
		qt.Commentf("Extent (what gets staged) is still the whole statement"))

	// A one-line binding has nothing to trim, so the two agree already --
	// the trim is a no-op wherever tree-sitter and pyright never differed.
	single, err := resolve.Resolve(lang, src, "SINGLE")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(single.DeclOnly, single.Extent))

	// A multi-line def is deliberately untouched: its full body is still
	// what the cross-check compares.
	fn, err := resolve.Resolve(lang, src, "validate")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(fn.DeclOnly, fn.Extent))
	qt.Assert(t, qt.StringContains(string(src[fn.DeclOnly.Start:fn.DeclOnly.End]), "return bool(tok)"))
}

func TestResolve_ForPathShebangFallback(t *testing.T) {
	t.Parallel()
	// A recognized extension is authoritative and never even looks at
	// content: passing shebang-shaped bytes that would map to a different
	// language must not steer a ".go" file anywhere else.
	lang, ok := resolve.ForPathFolding("main.go", []byte("#!/usr/bin/env python3\n"), false)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(lang.Name(), "go"))

	// Both forms of both shells named in the spec resolve to "shell", for an
	// extensionless path.
	for _, shebang := range []string{
		"#!/bin/bash\n", "#!/usr/bin/env bash\n",
		"#!/bin/sh\n", "#!/usr/bin/env sh\n",
	} {
		lang, ok := resolve.ForPathFolding("hooks/pre-commit", []byte(shebang+"foo() {\n  echo hi\n}\n"), false)
		qt.Assert(t, qt.IsTrue(ok), qt.Commentf("shebang %q", shebang))
		qt.Assert(t, qt.Equals(lang.Name(), "shell"))
	}

	// python3 falls out cleanly too: the same adapter Python's own
	// extension resolves to, with no special-casing for how it was reached.
	lang, ok = resolve.ForPathFolding("bin/tool", []byte("#!/usr/bin/env python3\n\ndef foo():\n    return 1\n"), false)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(lang.Name(), "python"))

	// zsh is a real interpreter, not a typo, and is deliberately refused:
	// tree-sitter-bash would mis-parse zsh-only syntax rather than honestly
	// fail. An unrecognized interpreter (perl) and a leading line that is
	// an ordinary comment, not a shebang, both refuse the same way, as does
	// an extensionless path with no content at all to sniff.
	for _, c := range []struct {
		path    string
		content string
	}{
		{"hooks/pre-commit", "#!/bin/zsh\nfoo() {}\n"},
		{"hooks/pre-commit", "#!/usr/bin/env perl\n"},
		{"hooks/pre-commit", "# just a comment, not a shebang\nfoo() {}\n"},
		{"hooks/pre-commit", ""},
	} {
		_, ok := resolve.ForPathFolding(c.path, []byte(c.content), false)
		qt.Assert(t, qt.IsFalse(ok), qt.Commentf("content %q", c.content))
	}
}

// crossCheckOne drives resolve.CrossCheckExtents for a single resolution.
// The tests below assert per-anchor outcomes against a live server; the
// package itself only offers the batch form, since production never wants
// one round trip per anchor.
func crossCheckOne(ctx context.Context, sess *lsp.Session, lang resolve.Language, repoRoot, absPath string, src []byte, res *resolve.Resolution) (bool, error) {
	degraded, mismatches := resolve.CrossCheckExtents(ctx, sess, lang, repoRoot, absPath, src, []*resolve.Resolution{res})
	if len(mismatches) > 0 {
		return degraded, mismatches[0]
	}
	return degraded, nil
}
