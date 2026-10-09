package resolve

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// scssLanguage adapts the tree-sitter SCSS grammar. It shares CSS's rule_set,
// at-rule and selector shapes (the grammar descends from CSS's), so rule sets
// and at-rules resolve through cssLanguage's own helpers; what SCSS adds is
// `//` comments (js_comment), `$variable` declarations, @mixin and @function,
// and @use/@forward alongside @import.
type scssLanguage struct {
	defaultLanguage
	lang *ts.Language
	css  cssLanguage
}

func newSCSSLanguage() *scssLanguage {
	return &scssLanguage{lang: scssGrammar()}
}

func (s *scssLanguage) Name() string { return "scss" }

func (s *scssLanguage) Extensions() []string { return []string{".scss"} }

func (s *scssLanguage) TSLanguage() *ts.Language { return s.lang }

func (s *scssLanguage) IsComment(kind string) bool { return kind == "comment" || kind == "js_comment" }

func (s *scssLanguage) HeaderKinds() []string { return []string{"comment", "js_comment"} }

// ImportKinds spans @import, @use and @forward: all three are the file's
// module-loading statements.
func (s *scssLanguage) ImportKinds() []string {
	return []string{"import_statement", "use_statement", "forward_statement"}
}

// OwnsTrailingSeparator and MembersSitFlush are false for the reasons CSS
// gives (lang_css.go): no formatter fixes the blank lines around a header or
// a nested rule.
func (s *scssLanguage) OwnsTrailingSeparator() bool { return false }

func (s *scssLanguage) MembersSitFlush() bool { return false }

// Declarations indexes rule sets (with natively nested ones, qualified by the
// parent selector and a space), at-rules by their prelude, mixins and
// functions by identifier, and top-level `$variable` declarations. Control flow
// (@if, @each, @for, @while), @include and @extend carry no name of their
// own and are not addressable.
func (s *scssLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	var decls []Declaration
	for _, child := range namedChildren(root) {
		node := child
		switch node.Kind() {
		case "rule_set":
			decls = append(decls, s.css.ruleSetDeclarations(src, &node, "")...)
		case "mixin_statement", "function_statement":
			if d, ok := s.callableDeclaration(src, &node); ok {
				decls = append(decls, d)
			}
		case "declaration":
			if d, ok := s.variableDeclaration(src, &node); ok {
				decls = append(decls, d)
			}
		default:
			if d, ok := s.css.declarationFor(src, &node); ok {
				decls = append(decls, d)
			}
		}
	}
	return decls
}

// callableDeclaration names a mixin or function by its bare identifier, the
// spelling vscode-css-language-server reports it under. A mixin and a function
// sharing an identifier disambiguate by ordinal like any repeated name.
func (s *scssLanguage) callableDeclaration(src []byte, node *ts.Node) (Declaration, bool) {
	return namedDecl(src, node, node)
}

// variableDeclaration addresses a top-level `$name: value;`, which the
// grammar parses as an ordinary declaration whose property_name is `$name`.
// Any other top-level declaration is invalid outside a rule and unnamed.
func (s *scssLanguage) variableDeclaration(src []byte, node *ts.Node) (Declaration, bool) {
	if node.NamedChildCount() == 0 {
		return Declaration{}, false
	}
	prop := node.NamedChild(0)
	if prop.Kind() != "property_name" {
		return Declaration{}, false
	}
	name := strings.TrimSpace(nodeText(src, prop))
	if !strings.HasPrefix(name, "$") {
		return Declaration{}, false
	}
	return Declaration{Node: node, Bare: name}, true
}
