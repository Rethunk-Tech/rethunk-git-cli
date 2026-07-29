package resolve

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

func init() { register(newCSSLanguage()) }

// cssLanguage adapts the tree-sitter CSS grammar. Every node shape below was
// measured against a compiled parse tree, not read from grammar.js -- the
// same discipline lang_shell.go and lang_yaml.go already follow.
//
// tree-sitter-css declares no fields at all (measured against
// src/node-types.json: rule_set, at_rule, media_statement, declaration,
// import_statement all report an empty "fields" object), unlike Go, Python,
// or JSON -- every shape below is read by node kind and position, the same
// way lang_shell.go and lang_yaml.go's block_mapping_pair positional lookups
// already are for their own field-less constructs.
type cssLanguage struct {
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
// unambiguous node kind in this grammar (measured: "@import \"foo.css\";"
// parses as import_statement, distinct from every other statement kind), so
// -- unlike shell's `source` command, which shares its node kind with every
// other command -- CSS needs no ImportMatcher seam. @imports is therefore
// meaningful here, not a degraded nil the way it is for JSON, TOML, YAML, and
// Markdown, which have no import concept at all.
func (c *cssLanguage) ImportKinds() []string { return []string{"import_statement"} }

// Declarations walks the stylesheet root's own named children. "comment" and
// "import_statement" are deliberately excluded here even though a leading
// run of the latter is real content: an @import is reachable only through
// @imports (ImportKinds above), the same way Go's import_declaration is
// never itself a Declaration -- toplevelExtent's own doc comment says a
// Language's Declarations must never return entries for header or import
// material, or @header/@imports/@toplevel would claim overlapping bytes.
//
// Nested rule sets inside an @media/@supports/@keyframes block are not
// descended into and get no anchor of their own: the same "named nested
// declarations do not clear the bar" reasoning specs/design.md § Grammar
// scope already applied to Go/TypeScript/Python's anonymous function
// literals and closed as not worth building for v1.
func (c *cssLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	var decls []Declaration
	for _, child := range namedChildren(root) {
		node := child
		if d, ok := c.declarationFor(src, &node); ok {
			decls = append(decls, d)
		}
	}
	return decls
}

func (c *cssLanguage) declarationFor(src []byte, node *ts.Node) (Declaration, bool) {
	switch node.Kind() {
	case "rule_set":
		return c.ruleSetDeclaration(src, node)
	case "media_statement", "supports_statement", "keyframes_statement",
		"at_rule", "charset_statement", "namespace_statement", "scope_statement":
		return Declaration{Node: node, Bare: cssAtRuleName(src, node)}, true
	default:
		// "import_statement" (see ImportKinds above) and the exotic
		// top-level "declaration" node (a bare property with no rule
		// around it -- invalid CSS the grammar nonetheless tolerates) both
		// fall through here: neither has a name a caller could usefully
		// type as an anchor.
		return Declaration{}, false
	}
}

// ruleSetDeclaration reads a rule_set's own "selectors" child as Bare,
// verbatim -- ".button-primary", "#app", "div", or a comma list like
// ".a, .b" all stage as written, per the brief's own instruction that a
// selector's bare name is its selector text. rule_set has no fields
// (measured), so "selectors" is found by scanning for that node kind rather
// than a field lookup, the same way cssAtRuleName below locates the body of
// an at-rule without one.
func (c *cssLanguage) ruleSetDeclaration(src []byte, node *ts.Node) (Declaration, bool) {
	for _, child := range namedChildren(node) {
		if child.Kind() == "selectors" {
			return Declaration{Node: node, Bare: nodeText(src, &child)}, true
		}
	}
	return Declaration{}, false
}

// cssAtRuleName names an at-rule-shaped statement by its full prelude --
// keyword through whatever precedes its body -- rather than the bare
// keyword alone. The bare keyword was rejected: measured against a file with
// two "@media (...)" blocks, "@media" alone would collide on every at-rule
// of the same kind in one file, where the prelude ("@media (max-width:
// 600px)") is what actually distinguishes them; a generic at_rule with no
// prelude at all (bare "@font-face") still degrades to the keyword alone,
// which is exactly the shape a caller would expect to type.
//
// The last named child is the body when it is one of "block" (media_
// statement, supports_statement, at_rule, scope_statement -- measured: all
// four keep it as their own final named child) or "keyframe_block_list"
// (keyframes_statement's distinct body kind, measured separately since it is
// not itself called "block"); the name is everything before that child's own
// start byte. A body-less statement (charset_statement, import_statement,
// namespace_statement -- none of these ever have a block, measured) instead
// ends at the statement's own end byte, which includes the trailing ";" the
// trailing TrimRight below strips along with the whitespace either shape can
// leave behind.
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
