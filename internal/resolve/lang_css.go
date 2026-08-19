package resolve

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

func init() { register(newCSSLanguage()) }

// cssLanguage adapts the tree-sitter CSS grammar. tree-sitter-css declares
// no fields at all -- rule_set, at_rule, media_statement, declaration, and
// import_statement all report an empty "fields" object in node-types.json,
// unlike Go, Python, or JSON -- so every shape below is read by node kind
// and position, the same way lang_shell.go and lang_yaml.go's
// block_mapping_pair positional lookups already are for their own
// field-less constructs.
type cssLanguage struct {
	defaultLanguage
	lang *ts.Language
}

func newCSSLanguage() *cssLanguage {
	return &cssLanguage{lang: cssGrammar()}
}

func (c *cssLanguage) Name() string { return "css" }

func (c *cssLanguage) Extensions() []string { return []string{".css"} }

func (c *cssLanguage) TSLanguage() *ts.Language { return c.lang }

func (c *cssLanguage) IsComment(kind string) bool { return kind == "comment" }

// HeaderKinds is "comment" alone -- CSS has no shebang or package clause, so
// a leading /* ... */ block is the only thing @header can ever claim, the
// same "comment" kind headerExtent already walks for every other language.
func (c *cssLanguage) HeaderKinds() []string { return []string{"comment"} }

// ImportKinds names "import_statement" directly: @import is a real,
// unambiguous node kind in this grammar ("@import \"foo.css\";" parses as
// import_statement, distinct from every other statement kind), so -- unlike
// shell's `source` command, which shares its node kind with every
// other command -- CSS needs no ImportMatcher seam. @imports is therefore
// meaningful here, not a degraded nil the way it is for JSON, TOML, YAML, and
// Markdown, which have no import concept at all.
func (c *cssLanguage) ImportKinds() []string { return []string{"import_statement"} }

// OwnsTrailingSeparator is false: no CSS formatting convention -- Prettier's
// CSS printer included -- inserts a deterministic blank line after a
// leading comment block or an @import run the way gofmt does for Go, so
// whatever blank line, if any, follows one is the author's own spacing, not
// a structural part of @header/@imports.
func (c *cssLanguage) OwnsTrailingSeparator() bool { return false }

// MembersSitFlush is false: no CSS formatting convention enforces either a
// flush or a spaced boundary between a nested rule and its siblings
// deterministically -- the same "no tool-backed convention to match"
// reasoning YAML answers false for -- so a newly spliced-in nested rule
// (Declarations below) is treated as an ordinary top-level-shaped boundary,
// keeping whatever blank line the author would have written by hand.
func (c *cssLanguage) MembersSitFlush() bool { return false }

// AllowsRawHeadingFallback is inherited from defaultLanguage: CSS has no
// heading concept for the fallback to apply to.

// Declarations walks the stylesheet root's own named children. "comment" and
// "import_statement" are deliberately excluded here even though a leading
// run of the latter is real content: an @import is reachable only through
// @imports (ImportKinds above), the same way Go's import_declaration is
// never itself a Declaration -- toplevelExtent's own doc comment says a
// Language's Declarations must never return entries for header or import
// material, or @header/@imports/@toplevel would claim overlapping bytes.
//
// A rule_set's block is descended into for natively nested rule_sets
// (ruleSetDeclarations below): `.parent { .child {} }` parses .child as a
// direct named child of .parent's "block". A rule_set inside an
// @media/@supports/@keyframes block is not, and gets no anchor -- a separate
// non-descent rule (docs/ANCHORS.md). An at-rule is never walked for nested
// rule_sets wherever it appears, including inside another rule_set's block
// (`.a { @media (...) { .b {} } }` leaves .b as undescended as any other
// at-rule content).
func (c *cssLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	var decls []Declaration
	for _, child := range namedChildren(root) {
		node := child
		if node.Kind() == "rule_set" {
			decls = append(decls, c.ruleSetDeclarations(src, &node, "")...)
			continue
		}
		if d, ok := c.declarationFor(src, &node); ok {
			decls = append(decls, d)
		}
	}
	return decls
}

func (c *cssLanguage) declarationFor(src []byte, node *ts.Node) (Declaration, bool) {
	switch node.Kind() {
	case "media_statement", "supports_statement", "keyframes_statement",
		"at_rule", "charset_statement", "namespace_statement", "scope_statement":
		return Declaration{Node: node, Bare: cssAtRuleName(src, node)}, true
	default:
		// "import_statement" (see ImportKinds above) and the exotic
		// top-level "declaration" node (a bare property with no rule
		// around it -- invalid CSS the grammar nonetheless tolerates) both
		// fall through here: neither has a name a caller could usefully
		// type as an anchor. "rule_set" is handled by Declarations directly
		// (ruleSetDeclarations), not here, since it alone needs to recurse.
		return Declaration{}, false
	}
}

// ruleSetDeclarations reports node's own Declaration -- container-qualified
// by the immediately enclosing rule_set's own selector text, empty at the
// top level -- plus, recursively, one Declaration per rule_set natively
// nested directly in its own block, to whatever depth the worktree actually
// nests them. Qualification is one level only, the same nearest-ancestor
// rule lang_yaml.go's mappingDeclarations and lang_json.go's
// objectDeclarations already use for their own nested containers:
// Declaration carries one Container field, not a full path, so a rule
// nested three deep is qualified by its immediate parent's own bare
// selector text alone.
//
// Sep is set to " " (Declaration.Sep's own doc comment): CSS Nesting
// flattens via the descendant combinator, a literal space -- `.parent {
// .child {} }` means what `.parent .child { }` means -- so the qualified
// anchor a caller types back is ".parent .child", not the dot-joined
// "parent.child" every other adapter's own convention produces.
func (c *cssLanguage) ruleSetDeclarations(src []byte, node *ts.Node, container string) []Declaration {
	d, ok := c.ruleSetDeclaration(src, node, container)
	if !ok {
		return nil
	}
	out := []Declaration{d}
	if block := cssBodyChild(node); block != nil {
		for _, child := range namedChildren(block) {
			if child.Kind() != "rule_set" {
				continue
			}
			nested := child
			out = append(out, c.ruleSetDeclarations(src, &nested, d.Bare)...)
		}
	}
	return out
}

// ruleSetDeclaration reads a rule_set's own "selectors" child as Bare,
// verbatim -- ".button-primary", "#app", "div", or a comma list like
// ".a, .b" all stage as written, since a selector's bare name is its
// selector text. rule_set has no fields, so "selectors" is found by
// scanning for that node kind rather than a field lookup, the same way
// cssAtRuleName below locates the body of an at-rule without one.
func (c *cssLanguage) ruleSetDeclaration(src []byte, node *ts.Node, container string) (Declaration, bool) {
	for _, child := range namedChildren(node) {
		if child.Kind() == "selectors" {
			sep := ""
			if container != "" {
				sep = " "
			}
			return Declaration{Node: node, Bare: nodeText(src, &child), Container: container, Sep: sep}, true
		}
	}
	return Declaration{}, false
}

// cssAtRuleName names an at-rule-shaped statement by its full prelude --
// keyword through whatever precedes its body -- rather than the bare
// keyword alone. The bare keyword was rejected: with two "@media (...)"
// blocks in one file, "@media" alone would collide on every at-rule of the
// same kind, where the prelude ("@media (max-width: 600px)") is what
// actually distinguishes them; a generic at_rule with no prelude at all
// (bare "@font-face") still degrades to the keyword alone, which is exactly
// the shape a caller would expect to type.
//
// The last named child is the body when it is "block" (media_statement,
// supports_statement, at_rule and scope_statement all end with one) or
// "keyframe_block_list" (keyframes_statement's distinct body kind); the name
// is everything before that child's start byte. A body-less statement
// (charset_statement, import_statement, namespace_statement) ends at the
// statement's own end byte, including the trailing ";" the TrimRight below
// strips along with any whitespace.
func cssAtRuleName(src []byte, node *ts.Node) string {
	end := node.EndByte()
	if body := cssBodyChild(node); body != nil {
		end = body.StartByte()
	}
	return strings.TrimRight(string(src[node.StartByte():end]), " \t\n\r;")
}

func cssBodyChild(node *ts.Node) *ts.Node {
	count := node.NamedChildCount()
	if count == 0 {
		return nil
	}
	last := node.NamedChild(count - 1)
	switch last.Kind() {
	case "block", "keyframe_block_list":
		return last
	default:
		return nil
	}
}
