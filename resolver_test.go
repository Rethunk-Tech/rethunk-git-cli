// Resolver coverage, per CONTRIBUTING.md's three-file test budget. Byte
// offsets below were pinned by running the resolver against each fixture
// and reading back real tree-sitter output (fixture-first discipline,
// CONTRIBUTING.md § Tests); they are not derived from grammar docs.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
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
	// The locked decision: a blank line between a free-floating comment and the next symbol
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
	// internal/synth's nearest-existing-sibling insertion (specs/design.md
	// § Blob synthesis) walks the worktree's declaration order to find a
	// sibling also present in HEAD, skipping past any that are themselves
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

	// @header must also stop before @imports start if @imports exists without declarations.
	importsOnly := []byte(`package p

import "fmt"
`)
	impHeader := mustResolve(t, importsOnly, "@header")
	qt.Assert(t, qt.Equals(string(importsOnly[impHeader.Extent.Start:impHeader.Extent.End]), "package p"))
	impBlock := mustResolve(t, importsOnly, "@imports")
	qt.Assert(t, qt.IsTrue(impHeader.Extent.End <= impBlock.Extent.Start))
}

func TestResolve_TypeScriptAndPython(t *testing.T) {
	// The two grammars whose node shapes differ from Go in ways that fail
	// silently rather than loudly. Values below were pinned by running the
	// resolver against these fixtures and reading back real output.

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
	// declarationFor used to handle only five node kinds; everything else in
	// this fixture exited 3 unresolved. One fixture exercises every
	// newly-addressable shape at once rather than one test per kind.
	src := []byte(`enum Color { Red, Blue }
abstract class Base { run() { return 1 } }
function* gen() { yield 1 }
var legacy = 1
namespace N { export function inner() { return 1 } }
export default function () { return 2 }
class Ok { hello() { return 3 } }
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Color"), "enum Color { Red, Blue }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Base"),
		"abstract class Base { run() { return 1 } }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Base.run"), "run() { return 1 }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "gen"), "function* gen() { yield 1 }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "legacy"), "var legacy = 1"))
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
		[]string{"Color", "Base", "Base.run", "gen", "legacy", "N", "N.inner", "Ok", "Ok.hello"}))
}

func TestResolve_ImportsSpanInteriorComments(t *testing.T) {
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

func TestResolve_GoContainerMembers(t *testing.T) {
	// Struct fields and interface methods were both exit 3 before this; one
	// fixture exercises the addressable and the deliberately-unaddressable
	// shapes together, including a member reached inside a grouped
	// `type ( ... )` block.
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
	// A symbol anchor on a file whose language has no v1 grammar is the
	// caller's exit 9 (docs/ANCHORS.md § Language support); the resolver's
	// contribution is just reporting the extension unclaimed.
	_, ok := resolve.ForExtension(".rs")
	qt.Assert(t, qt.IsFalse(ok))
}

// --- Language-server cross-check (specs/design.md § Symbol resolution) ---
//
// Coverage here is split by what can prove it: the union-shape decode and
// the anchor-normalization/comparison logic run against an in-process mock
// server, since a mock cannot catch a change in real language-server range
// semantics but can exercise every line of rgit's own decode and compare
// code cheaply and deterministically. The one thing only a real server can
// prove -- that gopls's actual reported ranges still match tree-sitter's
// normalized extent -- gets exactly one live case, skipped cleanly when
// gopls is absent or -short is set (CONTRIBUTING.md § Tests).

// lspReadFrame reads one LSP header-framed JSON-RPC message
// (Content-Length, blank line, JSON body -- go.lsp.dev/jsonrpc2's
// NewStream framing) off r. ok=false at a clean EOF.
func lspReadFrame(r *bufio.Reader) (msg map[string]any, ok bool, err error) {
	length := -1
	for {
		line, rerr := r.ReadString('\n')
		if rerr != nil {
			return nil, false, nil //nolint:nilerr // EOF between frames is the normal shutdown path
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if after, found := strings.CutPrefix(line, "Content-Length:"); found {
			n, convErr := strconv.Atoi(strings.TrimSpace(after))
			if convErr != nil {
				return nil, false, fmt.Errorf("mock lsp server: bad Content-Length %q: %w", line, convErr)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, false, fmt.Errorf("mock lsp server: frame missing Content-Length")
	}
	body := make([]byte, length)
	if _, rerr := io.ReadFull(r, body); rerr != nil {
		return nil, false, fmt.Errorf("mock lsp server: read body: %w", rerr)
	}
	if uErr := json.Unmarshal(body, &msg); uErr != nil {
		return nil, false, fmt.Errorf("mock lsp server: decode body: %w", uErr)
	}
	return msg, true, nil
}

func lspWriteFrame(w io.Writer, msg map[string]any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mock lsp server: encode: %w", err)
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

// runMockLSPServer serves one initialize/initialized/didOpen/documentSymbol
// exchange over conn, answering documentSymbol with resultJSON verbatim --
// the raw union payload under test -- then returns. It never calls a *testing.T
// method: it runs on its own goroutine, and only Fatal-family calls are
// unsafe off the test goroutine.
func runMockLSPServer(conn io.ReadWriteCloser, resultJSON string) error {
	r := bufio.NewReader(conn)
	for {
		msg, ok, err := lspReadFrame(r)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}

		method, _ := msg["method"].(string)
		id, hasID := msg["id"]

		switch method {
		case "textDocument/documentSymbol":
			return lspWriteFrame(conn, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  json.RawMessage(resultJSON),
			})
		case "initialize":
			if err := lspWriteFrame(conn, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"capabilities": map[string]any{}},
			}); err != nil {
				return err
			}
		default:
			if hasID {
				if err := lspWriteFrame(conn, map[string]any{"jsonrpc": "2.0", "id": id, "result": nil}); err != nil {
					return err
				}
			}
			// Notifications (initialized, didOpen) get no reply.
		}
	}
}

func TestLSP_DocumentSymbolsDecodesBothUnionShapes(t *testing.T) {
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
			go func() { errCh <- runMockLSPServer(serverConn, tc.resultJSON) }()

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
	// first spawning a daemon without waiting for it (specs/design.md:
	// "never block on a cold server").
	attempt := func(r *resolve.Resolution) (bool, error) {
		sess := lsp.NewSession()
		defer sess.Close()
		return resolve.CrossCheckExtent(ctx, sess, lang, dir, path, src, r, false)
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

func TestResolve_MembersSharingANameAreOrdinal(t *testing.T) {
	// Ordinals used to be assigned only among symbols with no container, so
	// two members of one class produced the identical qualified name. The
	// index kept whichever came last, `Box.size` silently resolved to one of
	// the two, and the other became unaddressable -- with no ambiguity
	// reported, which is precisely what exit 4 exists to say. A TypeScript
	// get/set pair is the ordinary case, not a corner.
	src := []byte("export class Box {\n  get size(): number { return 1; }\n  set size(n: number) { }\n  only(): number { return 3; }\n}\n")

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Box.size#1"), "get size(): number { return 1; }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Box.size#2"), "set size(n: number) { }"))

	// A member whose name is unique in its container keeps the plain form.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Box.only"), "only(): number { return 3; }"))

	// The bare container-qualified name is ambiguous, not absent: the
	// remediation is "pick one of these two", not "did you mean".
	lang, ok := resolve.ForExtension(".ts")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "Box.size")
	var rerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &rerr))
	qt.Assert(t, qt.Equals(rerr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(rerr.Candidates, []string{"Box.size#1", "Box.size#2"}))
}

func TestResolve_SameNamedContainersDoNotMergeMembers(t *testing.T) {
	// Both classes are named Svc, so both sets of members carried the same
	// container and collided the same way.
	src := []byte("class Svc { run(): number { return 1; } }\nclass Svc { run(): number { return 2; } }\n")

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Svc.run#1"), "run(): number { return 1; }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".ts", src, "Svc.run#2"), "run(): number { return 2; }"))
}

func TestResolve_TypeScriptMultiDeclarator(t *testing.T) {
	// A multi-declarator statement -- `const a = 1, b = 2` -- used to
	// address only the first declarator. A change to b reported as a
	// change to a: a label that validates while naming a symbol nobody
	// touched, the same defect goSpecDeclarations closed for Go's grouped
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
	// TypeScript has no package clause or other "this precedes code"
	// marker; a licence/copyright block at the top of a file with no
	// shebang is an ordinary "comment" node like any other, so @header
	// resolved to nothing before this -- HeaderKinds() named only
	// hash_bang_line. Matching Python's "comment" header kind fixes it.
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
	// the lede). Before this fix the lede was reachable only by naming the
	// path.
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

	// A setext heading is addressable by its own slug, but -- measured
	// against a compiled parse tree -- unlike an atx heading it never opens
	// its own section, so its extent is the heading line alone, not a
	// header-plus-body span: the following paragraph belongs to the
	// enclosing "diff-scope" section instead.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".md", src2, "diff-scope.setext-title"),
		"Setext Title\n============\n"))

	// rgit always emits the slug, but accepts a heading's own raw text on
	// input, the same way it accepts gopls's "(*A).Get" spelling -- whether
	// or not that text contains a space. A single-word heading's raw text
	// ("Install") must resolve exactly like a multi-word one ("Diff Scope");
	// gating the fallback on a literal space made the single-word case
	// unresolvable for no reason a caller could act on.
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

func TestResolve_YAML(t *testing.T) {
	// A realistic GitHub Actions workflow: several jobs, nested steps, a
	// block scalar `run: |`, a flow sequence, and a comment sitting between
	// the end of a nested job and the next, more shallowly indented one --
	// byte extents pinned by running the resolver against this exact
	// fixture, not derived from the grammar's docs (CONTRIBUTING.md §
	// Tests).
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
	// comment's own written column (lang_yaml.go's trimTrailingComment) --
	// measured directly, not assumed. "jobs.build" excludes it (trimmed off
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

	// Naming a bare sequence item (no such anchor exists to type in the
	// first place) refuses honestly rather than silently matching something
	// else.
	lang, ok := resolve.ForExtension(".yml")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "build.steps.0")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// .yaml is claimed too.
	_, ok = resolve.ForExtension(".yaml")
	qt.Assert(t, qt.IsTrue(ok))
}

func TestResolve_YAMLGrammarEdgeShapes(t *testing.T) {
	// A second fixture isolates shapes the realistic workflow above never
	// exercises: a quoted key, an anchor/alias pair riding along verbatim
	// inside whatever key contains them, a flow-style mapping value left
	// undescended, and two containers of the same name at different
	// grandparents colliding the same way lang_markdown.go's two "Options"
	// headings under different parents already do.
	src := []byte(`defaults: &defaults
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
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", src, "defaults"),
		"defaults: &defaults\n  adapter: postgres"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", src, "development.<<"), "<<: *defaults"))

	// A quoted key's surrounding quote byte is stripped from Bare -- a
	// best-effort unwrap, not full YAML unescaping.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", src, "development.quoted key"), `"quoted key": ok`))

	// A flow-style mapping value is a leaf: "development.flow" claims the
	// whole `{ a: 1, b: 2 }`, but there is no "development.flow.a" to
	// address -- flow style is never descended into, at any depth.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", src, "development.flow"), "flow: { a: 1, b: 2 }"))
	_, err := resolve.Resolve(mustYAMLLang(t), src, "development.flow.a")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// "a.common" and "b.common" are different containers, so "common.port"
	// under each collides in qualified name even though the two are nowhere
	// near each other in the tree -- ordinal disambiguation, not a merge.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", src, "common.port#1"), "port: 1"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", src, "common.port#2"), "port: 2"))

	_, err = resolve.Resolve(mustYAMLLang(t), src, "common.port")
	var ambigErr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &ambigErr))
	qt.Assert(t, qt.Equals(ambigErr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(ambigErr.Candidates, []string{"common.port#1", "common.port#2"}))
}

func mustYAMLLang(t *testing.T) resolve.Language {
	t.Helper()
	lang, ok := resolve.ForExtension(".yaml")
	if !ok {
		t.Fatal("resolve: no adapter registered for .yaml")
	}
	return lang
}

// TestResolve_YAMLMultiDocumentUnaddressable pins the deliberate refusal: a
// "---"-separated multi-document stream has no addressable key at all,
// rather than guessing which document a bare key path means.
func TestResolve_YAMLMultiDocumentUnaddressable(t *testing.T) {
	src := []byte("name: A\n---\nname: B\n")

	lang, ok := resolve.ForExtension(".yml")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "name")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))
}

// TestResolve_YAMLNoTopLevelMapping covers the other shapes topBlockMapping
// refuses: a document whose only content is a bare scalar, or a sequence
// with no mapping anywhere in it, both leave nothing addressable -- there is
// no key to qualify a Declaration with in either case.
func TestResolve_YAMLNoTopLevelMapping(t *testing.T) {
	lang, ok := resolve.ForExtension(".yml")
	qt.Assert(t, qt.IsTrue(ok))

	for _, src := range []string{
		"just a scalar\n",
		"- one\n- two\n",
		"# only a comment, no document at all\n",
	} {
		_, err := resolve.Resolve(lang, []byte(src), "one")
		var unresolvable *resolve.ResolveError
		qt.Assert(t, qt.ErrorAs(err, &unresolvable), qt.Commentf("src %q", src))
		qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))
	}

	// The comment-only file still has no document at all (soleDocument's
	// doc==nil case), but @header does not depend on there being one.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", []byte("# only a comment, no document at all\n"), "@header"),
		"# only a comment, no document at all"))
}

// TestResolve_YAMLKeyShapes pins the key spellings yamlKeyName does and does
// not turn into a Bare name: both quote styles, and an explicit "?" key
// whose own value is a nested block rather than a scalar -- left
// unaddressable rather than resolved to an invented spelling, the same
// reasoning Go's shared "A, B int" field line is refused for.
func TestResolve_YAMLKeyShapes(t *testing.T) {
	// The explicit "?" key's own value is a nested block mapping ("a: 1\n
	// b: 2"), not a scalar -- measured against a compiled parse tree: its
	// key field is a "block_node", not the ordinary "flow_node" every plain
	// or quoted key parses as, so it is left unaddressable rather than
	// resolved to an invented spelling. "plain" is unaffected by its
	// refused sibling.
	src := []byte("'single quoted': ok\n" +
		"?\n  a: 1\n  b: 2\n: value\n" +
		"plain: fine\n")

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "single quoted"), "'single quoted': ok"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "plain"), "plain: fine"))

	lang, ok := resolve.ForExtension(".yml")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "a")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))
}

func TestResolve_Shell(t *testing.T) {
	// Both function forms, a top-level var, source lines in two spellings, a
	// heredoc whose body only looks like a function definition, and a
	// redefinition -- byte extents pinned by running the resolver against
	// this exact fixture, not derived from the grammar's docs
	// (CONTRIBUTING.md § Tests).
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
	// same #N ordinal every other language already uses -- nothing shell-
	// specific was added for it.
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

func TestResolve_ForPathShebangFallback(t *testing.T) {
	// A recognized extension is authoritative and never even looks at
	// content: passing shebang-shaped bytes that would map to a different
	// language must not steer a ".go" file anywhere else.
	lang, ok := resolve.ForPath("main.go", []byte("#!/usr/bin/env python3\n"))
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(lang.Name(), "go"))

	// Both forms of both shells named in the spec resolve to "shell", for an
	// extensionless path.
	for _, shebang := range []string{
		"#!/bin/bash\n", "#!/usr/bin/env bash\n",
		"#!/bin/sh\n", "#!/usr/bin/env sh\n",
	} {
		lang, ok := resolve.ForPath("hooks/pre-commit", []byte(shebang+"foo() {\n  echo hi\n}\n"))
		qt.Assert(t, qt.IsTrue(ok), qt.Commentf("shebang %q", shebang))
		qt.Assert(t, qt.Equals(lang.Name(), "shell"))
	}

	// python3 falls out cleanly too: the same adapter Python's own
	// extension resolves to, with no special-casing for how it was reached.
	lang, ok = resolve.ForPath("bin/tool", []byte("#!/usr/bin/env python3\n\ndef foo():\n    return 1\n"))
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
		_, ok := resolve.ForPath(c.path, []byte(c.content))
		qt.Assert(t, qt.IsFalse(ok), qt.Commentf("content %q", c.content))
	}
}
