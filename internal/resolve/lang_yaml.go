package resolve

import (
	ts "github.com/tree-sitter/go-tree-sitter"
)

func init() { register(newYAMLLanguage()) }

// yamlLanguage adapts the tree-sitter YAML grammar.
//
// YAML is whitespace-sensitive in a way no other grammar here is: a
// mis-spliced block silently changes the
// document's meaning rather than failing to parse, and a block scalar's
// (`|`, `>`) body text carries leading whitespace that is part of its
// value, not incidental formatting. Two facts make staging safe here
// regardless: a block_mapping_pair's own extent starts at its key's first
// byte, never at the line's indentation -- the
// same convention every other adapter's container members already use, so
// internal/synth's existing lineStart/insertionText machinery (classify.go)
// handles YAML's indentation with no YAML-specific code -- and a
// block_scalar is one opaque leaf node whose byte range already includes
// every line of its body verbatim, so staging the pair that contains one
// never requires reasoning about the scalar's own internal indentation at
// all.
type yamlLanguage struct {
	defaultLanguage
	lang *ts.Language
}

func newYAMLLanguage() *yamlLanguage {
	return &yamlLanguage{lang: yamlGrammar()}
}

func (y *yamlLanguage) Name() string { return "yaml" }

func (y *yamlLanguage) Extensions() []string { return []string{".yaml", ".yml"} }

func (y *yamlLanguage) TSLanguage() *ts.Language { return y.lang }

// StructuredData is true: `rgit commit` refuses a FILE:SYMBOL anchor
// against YAML (StructuredDataLanguage's own doc comment, lang.go) --
// whitespace-sensitivity makes a wrongly-spliced blob especially easy to
// produce here, per this file's own package doc comment above.
func (y *yamlLanguage) StructuredData() bool { return true }

func (y *yamlLanguage) IsComment(kind string) bool { return kind == "comment" }

// trimTrailingComment implements the trailingCommentTrimmer seam
// (extent.go). tree-sitter-yaml's external scanner grafts a comment sitting
// between a nested value's end and the next, shallower sibling onto the
// deepest block still open when it consumed the token -- even a comment at
// the SAME column as the following key nests inside the previous key's last
// list item. Left alone, a container-qualified extent preceding such a
// comment absorbs content written to describe its successor.
//
// This walks node's "last named child" spine -- the descendants sharing its
// EndByte() -- until the spine runs out (nothing to trim: an anonymous token
// like a flow sequence's "]" accounts for the true end) or reaches a level
// whose trailing named children are comments, which are excluded along with
// the blank/indentation bytes before them.
//
// This can also exclude a comment that genuinely ended a container with
// nothing shallower after it; the tree cannot distinguish the two, and
// excluding is the safe direction -- it ends an anchor one comment short of
// the raw parse (still reachable via the enclosing container, @toplevel, or
// the whole file) rather than grafting one key's edit onto its neighbour.
// trimDeclOnlyEnd implements declOnlyEndTrimmer with the same scan the staged
// extent already uses. A block mapping's node runs to the start of the next
// sibling key, so it swallows the blank line and the whole comment block that
// introduces that next key -- documentation for something else. The staged
// extent trims it; the declaration-only extent did not, so the cross-check
// compared a range no language server would ever report and blamed the server
// for the difference.
func (y *yamlLanguage) trimDeclOnlyEnd(src []byte, node *ts.Node) uint {
	return y.trimTrailingComment(src, node)
}

// trimTrailingCommentLines drops whole trailing lines that are blank or hold
// nothing but a comment, stopping at the last line carrying real content and
// never crossing start.
//
// This is the end-of-file half of the same defect trimTrailingCommentSpine
// handles between siblings. A block mapping runs to wherever the next sibling
// key begins, and at end of file there is no next key, so a comment block
// closing the file falls inside the mapping's node without being one of its
// children -- the spine walk cannot see it. A line's first non-blank byte
// decides: a "#" inside a value is preceded by the "-" or quote that starts
// the value, so a real value is never mistaken for a comment.
func trimTrailingCommentLines(src []byte, start, end uint) uint {
	for end > start {
		// The last byte inside the extent, and the start of the line holding
		// it. Working from the last byte rather than from end keeps a final
		// newline attached to the line it terminates: an extent ending just
		// past one is that line ending, not an empty line after it, and
		// dropping it would normalize a file's EOF newline away.
		last := end - 1
		lineStart := last
		for lineStart > start && src[lineStart-1] != '\n' {
			lineStart--
		}
		i := lineStart
		for i <= last && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r') {
			i++
		}
		if i <= last && src[i] != '#' {
			return end
		}
		end = lineStart
	}
	return end
}

// trimTrailingComment removes a mapping's trailing comments from both the
// staged extent and the declaration-only one, whether they sit on the node's
// own child spine or close the file behind it.
func (y *yamlLanguage) trimTrailingComment(src []byte, node *ts.Node) uint {
	return trimTrailingCommentLines(src, node.StartByte(), y.trimTrailingCommentSpine(src, node))
}

func (y *yamlLanguage) trimTrailingCommentSpine(src []byte, node *ts.Node) uint {
	end := node.EndByte()
	cur := node
	for {
		children := namedChildren(cur)
		if len(children) == 0 {
			return end
		}
		last := &children[len(children)-1]
		if last.EndByte() != cur.EndByte() {
			// The last named child does not reach cur's own end -- an
			// anonymous trailing token accounts for the rest, so there is
			// no comment on this spine to find.
			return end
		}
		if last.Kind() != "comment" {
			cur = last
			continue
		}
		i := len(children) - 1
		for i >= 0 && children[i].Kind() == "comment" {
			i--
		}
		if i >= 0 {
			end = children[i].EndByte()
		} else {
			end = cur.StartByte()
		}
		for end > node.StartByte() {
			switch src[end-1] {
			case ' ', '\t', '\n', '\r':
				end--
				continue
			}
			return end
		}
		return end
	}
}

// ImportKinds is inherited from defaultLanguage: YAML has nothing that
// plays the role of an import -- there is no include/source directive in
// the grammar itself -- so @imports resolves to nothing, the same
// degraded-but-not-an-error answer Markdown already gives
// (lang_markdown.go).

// HeaderKinds is "comment" alone. A leading top-of-file comment run parses
// as one or more "comment" nodes that are siblings of "document" directly
// under the root "stream" node -- never nested inside "document" the way a
// %YAML or %TAG directive is -- so it is visible to headerExtent's walk
// over root's own children with nothing YAML-specific to add. Directives
// and the "---" document-start marker sit
// one level down, inside "document" itself, and are not part of @header;
// they are still preserved byte-for-byte as part of whichever pseudo- or
// real anchor's extent happens to contain them (@toplevel's widening reaches
// the enclosing "document" node, which starts at the first directive or
// "---" when either is present).
func (y *yamlLanguage) HeaderKinds() []string { return []string{"comment"} }

// OwnsTrailingSeparator is false: unlike gofmt for Go, no YAML formatter
// enforces a deterministic blank line after a header comment run -- the
// same "author's own blank lines are preserved, not normalized" reasoning
// that makes Prettier and Black answer false too -- so there is no
// tool-enforced separator to claim as structurally part of @header.
func (y *yamlLanguage) OwnsTrailingSeparator() bool { return false }

// MembersSitFlush is false: unlike gofmt or Prettier, no YAML formatter
// enforces either convention deterministically, so there is no tool-backed
// flush convention to match the way there is for Go and TypeScript -- the
// same reasoning Python answers false for, though for a different
// underlying cause (Python has an enforced convention, just the opposite
// one; YAML has none at all). This only governs a brand-new nested key
// being inserted, not the byte-identical replace/delete path a committed
// key already takes.
func (y *yamlLanguage) MembersSitFlush() bool { return false }

// AllowsRawHeadingFallback is inherited from defaultLanguage: YAML has no
// heading concept for the fallback to apply to.

// Declarations addresses top-level keys of a single-document YAML stream,
// container-qualified one level in for a nested mapping -- the same
// nearest-ancestor-only rule lang_markdown.go's sectionDeclarations already
// uses for nested headings, chosen for the same reason: Declaration carries
// one Container field, not a path, and Markdown already established that
// two symbols sharing a name-and-immediate-parent (but not the same
// grandparent) disambiguate with rgit's existing "#N" ordinal rather than
// inventing a second qualification scheme.
//
// A multi-document stream (more than one "---"-separated "document" under
// "stream") returns nil -- nothing addressable -- rather than guessing which
// document a bare key path means, or inventing a document-index qualifier
// nothing else in this resolver has a syntax for. Each "---" starts a new
// "document" node, sibling to the one before it, so detecting more than one
// is a direct child-kind count, not a heuristic.
func (y *yamlLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	doc, ok := soleDocument(root)
	if !ok {
		return nil
	}
	mapping, ok := topBlockMapping(doc)
	if !ok {
		return nil
	}
	return mappingDeclarations(mapping, "", src)
}

// soleDocument returns root's ("stream") one and only "document" child, or
// ok=false when there are zero (an empty file) or more than one (a
// multi-document stream, deliberately unaddressable -- see Declarations).
func soleDocument(root *ts.Node) (*ts.Node, bool) {
	var doc *ts.Node
	for _, child := range namedChildren(root) {
		if child.Kind() != "document" {
			continue
		}
		if doc != nil {
			return nil, false // a second document -- multi-document stream
		}
		c := child
		doc = &c
	}
	if doc == nil {
		return nil, false
	}
	return doc, true
}

// topBlockMapping finds doc's own top-level "block_mapping", skipping past
// any directive nodes (%YAML, %TAG) and an anchor/tag modifier that can
// precede it. An anchored top-level mapping (`&all\nkey: value`) parses as
// a "block_node" whose named children are the "anchor" node and the
// "block_mapping" node as siblings, in that order -- the anchor is never a
// wrapper around the mapping, so this only ever needs to look one level for
// a "block_mapping" among a "block_node"'s own children, never recurse.
//
// A document whose top-level content is a scalar ("flow_node"), a sequence
// ("block_sequence"), or a flow-style mapping ("flow_node" wrapping
// "flow_mapping") returns ok=false -- flow style is never descended into
// anywhere in this adapter (see mappingDeclarations), and a bare top-level
// scalar or sequence has no key to address by definition.
func topBlockMapping(doc *ts.Node) (*ts.Node, bool) {
	for _, child := range namedChildren(doc) {
		if child.Kind() != "block_node" {
			continue
		}
		return blockMappingIn(&child)
	}
	return nil, false
}

// blockMappingIn returns blockNode's own "block_mapping" child, skipping a
// sibling "anchor" or "tag" node when present.
func blockMappingIn(blockNode *ts.Node) (*ts.Node, bool) {
	for _, child := range namedChildren(blockNode) {
		if child.Kind() == "block_mapping" {
			c := child
			return &c, true
		}
	}
	return nil, false
}

// mappingDeclarations walks mapping's "block_mapping_pair" children.
// "comment" nodes are real siblings at this level and are skipped, carrying
// no key of their own; the shared docStart machinery (extent.go) still
// attaches one to the pair below it when no blank line separates them.
//
// A pair whose value is a nested "block_mapping" recurses one level,
// qualified by the pair's own bare key -- the nearest-ancestor-only rule
// sectionDeclarations uses. A "block_sequence" value is not descended into:
// a sequence item has no name to address it by (the same shape as Go's
// shared "A, B int" field line, left unaddressable rather than invented a
// spelling for), so `jobs.build.steps` addresses the whole list. Flow-style
// values (`{a: 1}`, `[1, 2]`) are leaves at any depth: no measured demand in
// the CI/compose files this grammar targets, and one rule beats a second
// recursion maintained in parallel with the block-style one.
func mappingDeclarations(mapping *ts.Node, container string, src []byte) []Declaration {
	var out []Declaration
	for _, child := range namedChildren(mapping) {
		if child.Kind() != "block_mapping_pair" {
			continue
		}
		pair := child
		bare, ok := yamlKeyName(src, pair.ChildByFieldName("key"))
		if !ok {
			continue
		}
		out = append(out, Declaration{Node: &pair, Bare: bare, Container: container})
		if nested, ok := nestedMapping(pair.ChildByFieldName("value")); ok {
			out = append(out, mappingDeclarations(nested, bare, src)...)
		}
	}
	return out
}

// nestedMapping reports the "block_mapping" a pair's own value node holds,
// when it holds one at all. value is nil for a placeholder key with no
// value (`foo:` alone); its own value node is "flow_node" (a scalar, or a
// flow-style mapping/sequence -- neither descended into, see
// mappingDeclarations); or it is "block_node" wrapping, as siblings, an
// optional "anchor"/"tag" modifier and one of "block_mapping",
// "block_sequence", or "block_scalar" -- only the first of those three is a
// container this resolver descends into.
func nestedMapping(value *ts.Node) (*ts.Node, bool) {
	if value == nil || value.Kind() != "block_node" {
		return nil, false
	}
	return blockMappingIn(value)
}

// yamlKeyName reads a block_mapping_pair's own key field as a bare name, or
// reports ok=false for a key shape this resolver does not turn into a
// symbol. key is nil for the rare explicit-key form's own edge cases and is
// nil-checked defensively.
//
// The ordinary case is key being a "flow_node" wrapping exactly one further
// named child: "plain_scalar" for an unquoted key (`jobs`, `on`, even a
// YAML-1.1-boolean-looking bare word like `on` -- the grammar never
// resolves it to a boolean node kind, only its literal text), or
// "double_quote_scalar"/"single_quote_scalar" for a quoted one, whose
// surrounding quote byte is stripped from Bare -- a best-effort unwrap, not
// full YAML unescaping, since an escape sequence in a mapping key is not a
// shape this resolver has real demand for. Anything else -- a key that is
// itself a "block_node" (an explicit `?`
// key whose own value is a multi-line mapping or sequence, rather than a
// plain scalar), or a flow_node with more than one named child (an
// anchor/tag decorating a key, essentially unseen in practice) -- is left
// unaddressable rather than resolved to a spelling this resolver invented.
func yamlKeyName(src []byte, key *ts.Node) (string, bool) {
	if key == nil || key.Kind() != "flow_node" {
		return "", false
	}
	children := namedChildren(key)
	if len(children) != 1 {
		return "", false
	}
	switch children[0].Kind() {
	case "plain_scalar":
		// Reading the named child directly, not key (the outer flow_node):
		// identical bytes today, since flow_node wraps exactly this one
		// child with nothing else inside it, but reading from the node the
		// comment above actually identifies is what stays correct if a
		// future grammar version ever puts trivia inside flow_node itself.
		return nodeText(src, &children[0]), true
	case "double_quote_scalar", "single_quote_scalar":
		// Same reasoning as plain_scalar just above, and for the same
		// reason must not read key (the outer flow_node) instead: doing so
		// here stayed byte-identical only because flow_node wraps nothing
		// else today, the exact coincidence that comment warns against
		// trusting.
		return stripQuotes(nodeText(src, &children[0]))
	default:
		return "", false
	}
}
