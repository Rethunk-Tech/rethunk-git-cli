package resolve

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

func init() { register(newHTMLLanguage()) }

// htmlLanguage adapts the tree-sitter HTML grammar. tree-sitter-html
// declares no fields at all -- element, start_tag, self_closing_tag,
// attribute, and every other node kind report an empty "fields" object in
// node-types.json, the same field-less, positional shape lang_css.go and
// lang_yaml.go's own constructs already have -- so every shape below is read
// by node kind and position.
//
// docs/ANCHORS.md and specs/design.md § Grammar scope record the anchor
// syntax this adapter deliberately stops at: element + id only ("div#app",
// that section's own canonical example), never a class, an nth-of-type, or
// a descendant combinator. rgit resolves anchors; it is not a CSS selector
// engine.
type htmlLanguage struct {
	defaultLanguage
	lang *ts.Language
}

func newHTMLLanguage() *htmlLanguage {
	return &htmlLanguage{lang: htmlGrammar()}
}

func (h *htmlLanguage) Name() string { return "html" }

// FlatContainer is true: Declaration.Container above is always the
// element's own tag, never an enclosing ancestor's name, so
// internal/synth's escalateToContainer must never resolve it as one
// (FlatContainerLanguage's own doc comment, lang.go).
func (h *htmlLanguage) FlatContainer() bool { return true }

func (h *htmlLanguage) Extensions() []string { return []string{".html", ".htm"} }

func (h *htmlLanguage) TSLanguage() *ts.Language { return h.lang }

func (h *htmlLanguage) IsComment(kind string) bool { return kind == "comment" }

// HeaderKinds is "doctype" and "comment": a leading "<!DOCTYPE html>" is a
// real, distinct node kind in this grammar, and any comment immediately
// preceding or following it (no blank line, headerExtent's own rule) is part
// of the same leading run -- the same "comment is a header kind too" shape
// Go and Python's own HeaderKinds already have, bounded the same way so it
// can never swallow a declaration's own doc comment (headerExtent's limit
// parameter).
func (h *htmlLanguage) HeaderKinds() []string { return []string{"doctype", "comment"} }

// ImportKinds is nil, inherited from defaultLanguage: tree-sitter-html has no
// node kind that plays the role of an import/include statement. A `<link
// rel="stylesheet">` or a `<script src="...">` is semantically import-shaped,
// but neither is a distinct grammar node -- both parse as an ordinary
// "element"/"script_element", indistinguishable by kind from any other tag,
// the same shape shell's `source` command has (ImportMatcher,
// lang_shell.go). Unlike shell, no ImportMatcher is built for it here:
// deciding which elements count (rel="stylesheet" but not rel="icon"?
// script[src] but not an inline <script>? a <base href>?) has no measured
// demand behind it -- specs/design.md § Grammar scope's own HTML entry
// names only div#app as the demand signal -- and is exactly the kind of
// widened scope element+id was deliberately kept narrow to avoid.
// @imports therefore degrades the same way it does for JSON, TOML, YAML,
// and Markdown: unresolvable, not an error.

// OwnsTrailingSeparator is false: no HTML formatter -- Prettier's own HTML
// printer included -- deterministically inserts a blank line after a leading
// doctype/comment run the way gofmt does after Go's package clause, so
// whatever spacing follows one is the author's own, not a structural part of
// @header.
func (h *htmlLanguage) OwnsTrailingSeparator() bool { return false }

// MembersSitFlush is false, the same reasoning lang_css.go gives CSS
// Nesting: no HTML formatting convention enforces either a flush or a spaced
// boundary between a newly nested element and its siblings deterministically,
// so a spliced-in nested element (Declarations below) keeps whatever
// blank-line spacing the author already used rather than one this adapter
// invents.
func (h *htmlLanguage) MembersSitFlush() bool { return false }

// AllowsRawHeadingFallback is inherited from defaultLanguage: HTML has no
// heading concept for the fallback to apply to.

// Declarations walks the document root's own named children, recursing into
// every element/script_element/style_element regardless of nesting depth.
// Unlike lang_css.go's one-level rule_set-inside-rule_set nesting, HTML's own
// structure has no natural depth limit, and a caller's div#app is exactly as
// likely to sit five levels deep -- a component root mounted inside a full
// page shell, the same shape specs/design.md § Grammar scope measured as
// this grammar's own component-root/mount-point demand case -- as at the
// top.
func (h *htmlLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	return h.elementDeclarations(src, root)
}

func (h *htmlLanguage) elementDeclarations(src []byte, node *ts.Node) []Declaration {
	var out []Declaration
	for _, child := range namedChildren(node) {
		c := child
		switch c.Kind() {
		case "element", "script_element", "style_element":
			out = append(out, h.declarationsFor(src, &c)...)
		}
	}
	return out
}

// declarationsFor reports node's own Declaration -- tag-qualified by its own
// id attribute, Sep "#" (Declaration.Sep's own doc comment; this is what
// produces the "div#app" spelling docs/ANCHORS.md § Language support ships,
// with no extra join code) -- when it has one, plus, recursively, one
// Declaration per id-bearing element
// nested anywhere inside it, to whatever depth the worktree actually nests
// them.
//
// An element with no id attribute at all gets no Declaration of its own --
// it is still recursed through, but never itself addressable. This is
// deliberate, not an oversight: this resolver's index has no per-parent
// scoping the way a real DOM's getElementById has document-wide uniqueness
// (or the way lang_css.go's Container only reaches one level) -- indexing
// bare tag names too would make "div" (or any common tag) collide across
// nearly every real HTML document. The demand signal was always the
// component-root/mount-point case, where an id already exists; teaching the
// resolver to fall back to bare-tag-name or positional lookup would reopen
// exactly the "how far does the selector syntax go" question element+id was
// scoped to close (specs/design.md § Grammar scope, which records both).
func (h *htmlLanguage) declarationsFor(src []byte, node *ts.Node) []Declaration {
	var out []Declaration
	if tag, id, ok := htmlTagAndID(src, node); ok {
		out = append(out, Declaration{Node: node, Bare: id, Container: tag, Sep: "#"})
	}
	out = append(out, h.elementDeclarations(src, node)...)
	return out
}

// htmlTagAndID reads node's own start_tag/self_closing_tag for its tag_name
// and its first "id"-named attribute. Attribute names are ASCII
// case-insensitive per the WHATWG HTML spec, so "id", "ID", and "Id" are all
// recognized identically; the tag name and the id's own value are both taken
// verbatim, never case-folded, the same "exactly as written" convention
// lang_css.go's selector text already follows. A second "id" attribute on
// the same tag (invalid HTML the grammar nonetheless tolerates) is never
// reached, matching the browser's own first-occurrence-wins rule for a
// duplicate attribute; a boolean `id` (no "=") or an empty `id=""` both
// report ok=false, the same "no value to read" refusal lang_json.go gives an
// empty string key.
func htmlTagAndID(src []byte, node *ts.Node) (tag, id string, ok bool) {
	for _, child := range namedChildren(node) {
		if child.Kind() != "start_tag" && child.Kind() != "self_closing_tag" {
			continue
		}
		tagNode := child
		for _, c := range namedChildren(&tagNode) {
			if c.Kind() == "tag_name" {
				tag = nodeText(src, &c)
				break
			}
		}
		for _, attr := range namedChildren(&tagNode) {
			if attr.Kind() != "attribute" {
				continue
			}
			a := attr
			name, val := htmlAttributeNameValue(src, &a)
			if name == "" || !strings.EqualFold(name, "id") {
				continue
			}
			// The first "id"-named attribute found settles it, whether or
			// not it turns out to carry a usable value: a later duplicate
			// is never consulted, the same first-wins rule a browser
			// applies.
			return tag, val, val != ""
		}
		return tag, "", false
	}
	return "", "", false
}

// htmlAttributeNameValue reads one "attribute" node's name and value.
// value's own text lives one of two shapes deep: a bare, unquoted value
// (`id=app`) is attribute's own direct "attribute_value" child, while a
// quoted one (`id="app"`, `id='app'`) wraps that same "attribute_value" kind
// one level inside "quoted_attribute_value" -- measured directly against a
// compiled parse tree of both forms, not assumed from the grammar's own
// node-types.json, which declares no fields to read either shape by name. An
// empty quoted value (`id=""`) has a "quoted_attribute_value" wrapper with no
// "attribute_value" child inside it at all, and a boolean attribute (bare
// `id`, no "=") has neither shape present -- both leave value empty.
func htmlAttributeNameValue(src []byte, attr *ts.Node) (name, value string) {
	for _, child := range namedChildren(attr) {
		c := child
		switch c.Kind() {
		case "attribute_name":
			name = nodeText(src, &c)
		case "attribute_value":
			value = nodeText(src, &c)
		case "quoted_attribute_value":
			for _, inner := range namedChildren(&c) {
				in := inner
				if in.Kind() == "attribute_value" {
					value = nodeText(src, &in)
				}
			}
		}
	}
	return name, value
}
