package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

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
