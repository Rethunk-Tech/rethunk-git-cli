package resolve

import ts "github.com/tree-sitter/go-tree-sitter"

// tomlLanguage adapts the tree-sitter TOML grammar.
//
// Like CSS and YAML, this grammar declares no fields at all -- "document",
// "pair", "table", and "table_array_element" all report an empty "fields"
// object in node-types.json -- every shape below is read by node kind and
// position.
type tomlLanguage struct {
	defaultLanguage
	lang *ts.Language
}

func newTOMLLanguage() *tomlLanguage {
	return &tomlLanguage{lang: tomlGrammar()}
}

func (l *tomlLanguage) Name() string { return "toml" }

func (l *tomlLanguage) Extensions() []string { return []string{".toml"} }

func (l *tomlLanguage) TSLanguage() *ts.Language { return l.lang }

// StructuredData is true: `rgit commit` refuses a FILE:SYMBOL anchor
// against TOML (StructuredDataLanguage's own doc comment, lang.go).
func (l *tomlLanguage) StructuredData() bool { return true }

// IsComment: TOML comments ("# ...") parse as a real "comment" node kind --
// unlike JSON, which has no comment syntax at all.
func (l *tomlLanguage) IsComment(kind string) bool { return kind == "comment" }

// HeaderKinds is "comment" alone, the same as every other comment-fronted
// grammar in this resolver. A leading "# ..." run is the only thing @header
// can claim -- TOML has no shebang or package-clause equivalent.
func (l *tomlLanguage) HeaderKinds() []string { return []string{"comment"} }

// ImportKinds is inherited from defaultLanguage: TOML has no include/import
// directive of any kind, the same degraded-but-not-an-error answer
// Markdown, YAML, and JSON also give.

// OwnsTrailingSeparator is false: no TOML formatting convention -- there is
// no widely-adopted formatter for this grammar analogous to gofmt -- inserts
// a deterministic blank line after a leading comment run, so whatever blank
// line, if any, follows one is the author's own spacing, not a structural
// part of @header.
func (l *tomlLanguage) OwnsTrailingSeparator() bool { return false }

// MembersSitFlush is false: no TOML formatting convention enforces either a
// flush or a spaced boundary between table entries deterministically, the
// same "no tool-backed convention to match" reasoning YAML and JSON answer
// false for, so a newly spliced-in table member is treated as an ordinary
// top-level-shaped boundary.
func (l *tomlLanguage) MembersSitFlush() bool { return false }

// AllowsRawHeadingFallback is inherited from defaultLanguage: TOML has no
// heading concept for the fallback to apply to.

// Declarations walks the document root's own named children. A bare
// top-level pair (no enclosing "[table]") gets an empty Container; a
// "table"/"table_array_element" is itself addressable by its own header key
// -- naming it claims everything nested, the same convention a Markdown
// heading or a YAML mapping key already follows -- and its member pairs are
// qualified by that same header text, read verbatim (see tomlKeyName).
//
// An inline table (`{ a = 1 }`) and an array (`[1, 2, 3]`) are both left
// undescended, at any depth: an array element has no name to address it by,
// the same reasoning lang_yaml.go's block_sequence_item is refused for, and
// there is no measured demand in the config files this grammar targets for
// exploding an inline table into its own container the way a bracketed
// "[table]" is.
func (l *tomlLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	var decls []Declaration
	for _, child := range namedChildren(root) {
		node := child
		switch node.Kind() {
		case "pair":
			if d, ok := tomlPairDeclaration(src, &node, ""); ok {
				decls = append(decls, d)
			}
		case "table", "table_array_element":
			decls = append(decls, tomlTableDeclarations(src, &node)...)
		}
	}
	return decls
}

// tomlTableDeclarations reports a "table"/"table_array_element" node's own
// header as a Declaration (Bare is the header key's own text, Container
// empty since a table is only ever a root-level construct in this grammar
// -- document's own children are exactly pair/table/table_array_element,
// with no nesting), then walks its remaining named
// children -- "pair" entries qualified by that header text, "comment"
// skipped -- to address its members. A second table sharing one header's
// literal text (two "[[servers]]" elements is the ordinary case, not a
// corner) collides the same way two same-named Go functions do: the
// existing "#N" ordinal machinery (index.go) disambiguates both the table's
// own anchor and its members', with nothing TOML-specific required.
func tomlTableDeclarations(src []byte, table *ts.Node) []Declaration {
	children := namedChildren(table)
	if len(children) == 0 {
		return nil
	}
	header := &children[0]
	container, ok := tomlKeyName(src, header)
	if !ok {
		return nil
	}
	decls := []Declaration{{Node: table, Bare: container}}
	for i := 1; i < len(children); i++ {
		member := children[i]
		if member.Kind() != "pair" {
			continue
		}
		if d, ok := tomlPairDeclaration(src, &member, container); ok {
			decls = append(decls, d)
		}
	}
	return decls
}

// tomlPairDeclaration reports a "pair" node's own key as Bare, unqualified
// beyond whatever container the caller already supplies. A pair whose own
// key is a "dotted_key" (`a.b = 1`, legal directly at the document root or
// inside a table) is not decomposed into its own container/bare split --
// tomlKeyName reads its full dotted spelling as one Bare string, the same
// simplification lang_css.go's comma-joined ".a, .b" selector list makes:
// one predictable rule rather than a second qualification scheme maintained
// in parallel with the "[table]"-header one.
func tomlPairDeclaration(src []byte, pair *ts.Node, container string) (Declaration, bool) {
	if pair.NamedChildCount() == 0 {
		return Declaration{}, false
	}
	bare, ok := tomlKeyName(src, pair.NamedChild(0))
	if !ok {
		return Declaration{}, false
	}
	return Declaration{Node: pair, Bare: bare, Container: container}, true
}

// tomlKeyName reads a key node -- "bare_key", "dotted_key", or
// "quoted_key", the three kinds a pair's or a table header's own first
// named child can be -- as a Bare-ready string. A quoted key's surrounding
// quote byte is stripped, a best-effort unwrap matching lang_yaml.go's own
// quoted-key handling, not full TOML string-escape decoding.
func tomlKeyName(src []byte, key *ts.Node) (string, bool) {
	switch key.Kind() {
	case "bare_key", "dotted_key":
		return nodeText(src, key), true
	case "quoted_key":
		return stripQuotes(nodeText(src, key))
	default:
		return "", false
	}
}
