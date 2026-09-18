package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

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
