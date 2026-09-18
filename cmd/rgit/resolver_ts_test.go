package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

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
