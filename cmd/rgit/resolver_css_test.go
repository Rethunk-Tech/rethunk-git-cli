package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

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

	// A grouped selector is indexed one anchor per selector, each staging
	// the whole rule. The list itself is not an anchor: written across
	// lines, as real stylesheets write it, its text carries newlines and
	// broke both `rgit symbols`' one-per-line output and every completion
	// script parsing it.
	commaList := []byte(`.a, .b {
  color: red;
}

.a {
  color: blue;
}
`)

	// ".b" belongs to the group alone, so it is unambiguous and stages the
	// whole grouped rule -- both selectors included.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", commaList, ".b"),
		".a, .b {\n  color: red;\n}"))

	// ".a" now names two real rules -- the group and the standalone one --
	// so it is ambiguous rather than silently picking one, the same
	// collision behaviour every other language gets, with ordinals to
	// disambiguate.
	_, err = resolve.Resolve(lang, commaList, ".a")
	var cssAmbiguous *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &cssAmbiguous))
	qt.Assert(t, qt.Equals(cssAmbiguous.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", commaList, ".a#1"),
		".a, .b {\n  color: red;\n}"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", commaList, ".a#2"),
		".a {\n  color: blue;\n}"))

	// The whole list is no longer a name anything answers to.
	_, err = resolve.Resolve(lang, commaList, ".a, .b")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// A comma inside a pseudo-class argument is not a list separator: the
	// split follows the grammar's own selector children, never the ",".
	isList := []byte(`h1:is(h2, h3) {
  color: red;
}
`)
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".css", isList, "h1:is(h2, h3)"),
		"h1:is(h2, h3) {\n  color: red;\n}"))

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
