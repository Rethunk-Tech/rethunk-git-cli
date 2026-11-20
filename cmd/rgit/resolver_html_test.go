package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/resolve"
)

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

func TestResolve_HTMLIDContainingDot(t *testing.T) {
	t.Parallel()
	src := []byte(`<a id="toc-mod.io" class="widget">item</a>` + "\n")
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".html", src, "a#toc-mod.io"), `<a id="toc-mod.io" class="widget">item</a>`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".html", src, "toc-mod.io"), `<a id="toc-mod.io" class="widget">item</a>`))

	lang, ok := resolve.ForExtension(".html")
	qt.Assert(t, qt.IsTrue(ok))
	order, err := resolve.DeclOrder(lang, src)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(order, []string{"a#toc-mod.io"}))
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

// TestResolve_HTMLVoidElementDeclOnlyTrimmed pins that a void element followed by inline
// text with no enclosing tag or sibling element to stop it (the grammar absorbs that text
// into the element's node) is trimmed back to its start_tag's end, both in the staged
// Extent and in DeclOnly, the extent the LSP cross-check compares.
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
	qt.Assert(t, qt.Equals(full, declOnly),
		qt.Commentf("the staged extent stops at the void element's tag, not at the text after it"))

	// An element with a real end_tag is unaffected: DeclOnly and Extent
	// agree, nothing to trim.
	pRes, err := resolve.Resolve(lang, src, "p#after")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(pRes.DeclOnly, pRes.Extent))
}
