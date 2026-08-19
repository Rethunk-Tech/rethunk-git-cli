package resolve

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

func init() { register(newMarkdownLanguage()) }

// mdLanguage adapts the tree-sitter Markdown grammar's BLOCK half only.
// Headings and their nesting into sections are entirely a block-level
// concern; the separate inline grammar (emphasis, links, code spans) is
// never loaded (grammars.go), so a heading's own title surfaces as raw,
// unparsed "inline" node text rather than a further parse tree — see
// headingText.
type mdLanguage struct {
	defaultLanguage
	lang *ts.Language
}

func newMarkdownLanguage() *mdLanguage {
	return &mdLanguage{lang: markdownGrammar()}
}

func (m *mdLanguage) Name() string { return "markdown" }

func (m *mdLanguage) Extensions() []string { return []string{".md", ".markdown"} }

func (m *mdLanguage) TSLanguage() *ts.Language { return m.lang }

// IsComment is always false: the grammar has no dedicated comment node — an
// HTML comment is ordinary html_block content — so no doc-attribution rule
// (the blank-line comment merge every other adapter uses) applies to a
// heading the way one applies to a Go func or a TS class.
func (m *mdLanguage) IsComment(kind string) bool { return false }

// ImportKinds is inherited from defaultLanguage: markdown has nothing that
// plays the role of an import, so @imports resolves to nothing — the same
// degraded-but-not-an-error result @header already gives a TypeScript file
// with no shebang (docs/ANCHORS.md). A richer ImportMatcher contract, the
// way lang_shell.go implements one, is what a later shell-generation
// grammar will need; it is deliberately not added here.

// HeaderKinds is frontmatter only. Document's own children are exactly
// minus_metadata (YAML "---" fencing), plus_metadata (TOML "+++" fencing),
// and section — a file opens with at most one of the two metadata kinds,
// never both.
func (m *mdLanguage) HeaderKinds() []string { return []string{"minus_metadata", "plus_metadata"} }

// OwnsTrailingSeparator is false: no markdown formatter this resolver
// treats as authoritative enforces a deterministic blank line after
// frontmatter, so whatever separates it from the lede is the author's own
// spacing, not a structural part of @header.
func (m *mdLanguage) OwnsTrailingSeparator() bool { return false }

// MembersSitFlush is false: no markdown convention enforces either a flush
// or a spaced boundary between sibling headings deterministically, the same
// "no tool-backed convention to match" reasoning YAML, JSON, and TOML
// answer false for, so a newly spliced-in section is treated as an
// ordinary top-level-shaped boundary.
func (m *mdLanguage) MembersSitFlush() bool { return false }

// AllowsRawHeadingFallback is true: a caller may paste a heading's own raw
// title -- copied straight out of the rendered document or an editor
// outline -- rather than rgit's own emitted slug, and rawHeadingFallback
// (index.go) exists specifically to accept that. Markdown is the only
// grammar with a heading concept at all, so it is the only adapter that
// answers true.
func (m *mdLanguage) AllowsRawHeadingFallback() bool { return true }

// Declarations walks document's top-level "section" children. Every
// heading, and all of its content down to arbitrary depth, lives inside
// some section — document itself never
// holds a bare heading or paragraph as a direct child. A leading run of
// content with no heading above it (the lede) is itself wrapped in a
// section, never a sibling of one: an atx_heading always opens a brand new
// section rather than being absorbed into whatever content came before it,
// so a headerless section can only ever be the first document child, and
// contributes no Declaration of its own — it has no heading to name it by.
func (m *mdLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	var out []Declaration
	for _, child := range namedChildren(root) {
		if child.Kind() == "section" {
			out = append(out, sectionDeclarations(&child, "", src)...)
		}
	}
	return out
}

// sectionDeclarations names sec's own heading, if it has one, then
// descends exactly one structural level for qualification: a direct child
// "section" — an atx-opened subsection, at whatever depth this recursion
// actually reaches — is container-qualified by sec's own slug, not by the
// full ancestor chain. That is the same one-level ceiling every other
// container in this resolver already uses (a Go struct's fields, a
// TypeScript class's methods): `install.options` names the nearest
// ancestor, never `docs.install.options`.
//
// sec's extent (Node) is the whole section — heading plus everything under
// it, including nested subsections — so naming a heading claims its whole
// subtree, the same as naming a class claims its members (docs/ANCHORS.md).
// A direct child setext_heading is named the same way but see its case
// below for why its own extent cannot include what follows it.
func sectionDeclarations(sec *ts.Node, container string, src []byte) []Declaration {
	var out []Declaration
	slug := ""
	if heading := atxHeadingOf(sec); heading != nil {
		if s := slugify(headingText(src, heading)); s != "" {
			slug = s
			out = append(out, Declaration{Node: sec, Bare: slug, Container: container})
		}
	}
	for _, child := range namedChildren(sec) {
		switch child.Kind() {
		case "section":
			out = append(out, sectionDeclarations(&child, slug, src)...)
		case "setext_heading":
			// Unlike an atx_heading, a setext_heading never opens its own
			// nested section — whatever text follows it stays a further
			// sibling
			// inside the SAME enclosing section, not grouped under the
			// heading the way an atx-opened section groups its body.
			// Reproducing that grouping by hand would be exactly the
			// level-tracking walk this grammar was chosen to avoid
			// (specs/design.md § Dependencies), so a setext heading is
			// addressable by its own slug, but its extent is the heading
			// line alone — a declaration with no body, not a section.
			if s := slugify(headingText(src, &child)); s != "" {
				out = append(out, Declaration{Node: &child, Bare: s, Container: slug})
			}
		}
	}
	return out
}

// atxHeadingOf returns sec's own opening heading, or nil for the headerless
// lede section. Whenever a section has a heading at all, it is always
// exactly the section's first named child — an atx_heading is what caused
// the section to begin in the first place, so it can never appear anywhere
// else in one.
func atxHeadingOf(sec *ts.Node) *ts.Node {
	if sec.NamedChildCount() == 0 {
		return nil
	}
	first := sec.NamedChild(0)
	if first != nil && first.Kind() == "atx_heading" {
		return first
	}
	return nil
}

// headingText reads an atx_heading's or setext_heading's own title text.
// Both carry it under a "heading_content" field, but at a different depth:
// atx_heading's field is the "inline" node directly, while setext_heading's
// is a "paragraph" wrapping
// one (a setext heading's title is grammatically a whole paragraph line,
// not its own node kind). Only the block grammar is loaded (grammars.go),
// so "inline" is an opaque leaf here — there is no emphasis/link/code-span
// structure to descend through, just its own raw text.
func headingText(src []byte, heading *ts.Node) string {
	content := heading.ChildByFieldName("heading_content")
	if content == nil {
		return ""
	}
	inline := content
	if content.Kind() == "paragraph" {
		inline = nil
		for _, c := range namedChildren(content) {
			if c.Kind() == "inline" {
				inline = &c
				break
			}
		}
		if inline == nil {
			return ""
		}
	}
	return strings.TrimSpace(nodeText(src, inline))
}

// toplevelExtent is markdown's own @toplevel: the lede — any content between
// @header (frontmatter) and the first heading — rather than the span-of-
// declarations pseudo.go's shared toplevelExtent computes for every other
// language.
//
// The shared algorithm spans idx.order's first through last declaration,
// which for markdown is the opposite region: Declarations never names the
// lede (no heading to name it by, see atxHeadingOf), so "first declaration"
// is the first actual heading and the shared formula computes everything the
// lede is not. pseudo.go dispatches here via a type assertion on
// *mdLanguage, keeping every other grammar on the shared path.
//
// found is false when there is no lede to stage: a document with no
// content before its first heading (or none at all after frontmatter) has
// nothing here for @toplevel to claim, the same "degraded, not an error"
// answer @imports already gives every markdown file.
func (m *mdLanguage) toplevelExtent(src []byte, root *ts.Node, idx *index) (Extent, bool) {
	children := namedChildren(root)
	if len(children) == 0 {
		return Extent{}, false
	}

	// @header (frontmatter) is always root's first child when present
	// (HeaderKinds' own doc comment), so the lede starts right after it.
	start := uint(0)
	if header := kindSet(m.HeaderKinds()); header[children[0].Kind()] {
		start = children[0].EndByte()
	}

	// The lede ends where the first heading section begins. idx.order is in
	// document order (Declarations walks depth-first, naming a section
	// before descending into it), so its first entry is always the
	// document's very first heading, at whatever nesting depth that
	// happens to be. No heading anywhere means the whole post-frontmatter
	// document is the lede.
	end := uint(len(src))
	if len(idx.order) > 0 {
		end = idx.order[0].Full.Start
	}

	if end <= start {
		return Extent{}, false
	}
	return Extent{Start: start, End: end}, true
}

// slugify renders heading text the way rgit always emits an anchor:
// lowercase ASCII alphanumerics, any run of anything else collapsed to a
// single hyphen, no leading or trailing hyphen. A heading with no
// alphanumeric content at all (rare, but a bare "---" horizontal-rule-
// looking line can parse as an empty setext title in some inputs) slugifies
// to "" and is left unaddressable rather than staged under an empty name.
//
// This is deliberately not GitHub's slugger: no Unicode-aware casing, and
// duplicate headings disambiguate with rgit's existing "#N" ordinal
// (index.go's assignQualifiedNames), never GitHub's "-1"/"-2" slug-dedupe
// suffix — two competing disambiguation syntaxes in one tool would be worse
// than either alone (docs/ANCHORS.md).
func slugify(s string) string {
	var b strings.Builder
	pendingHyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if pendingHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingHyphen = false
			b.WriteRune(r)
		default:
			pendingHyphen = true
		}
	}
	return b.String()
}
