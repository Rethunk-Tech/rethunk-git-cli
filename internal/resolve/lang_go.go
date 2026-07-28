package resolve

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

func init() { register(newGoLanguage()) }

type goLanguage struct {
	lang *ts.Language
}

func newGoLanguage() *goLanguage {
	return &goLanguage{lang: goGrammar()}
}

func (g *goLanguage) Name() string { return "go" }

func (g *goLanguage) Extensions() []string { return []string{".go"} }

func (g *goLanguage) TSLanguage() *ts.Language { return g.lang }

func (g *goLanguage) IsComment(kind string) bool { return kind == "comment" }

// goTopLevelKinds are the addressable top-level node kinds: functions
// and methods by their own "name" field,
// const/var/type declarations by the name field of the spec they wrap.
var goTopLevelKinds = map[string]bool{
	"function_declaration": true,
	"method_declaration":   true,
	"const_declaration":    true,
	"var_declaration":      true,
	"type_declaration":     true,
}

func (g *goLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	children := namedChildren(root)
	var out []Declaration
	for i := range children {
		c := &children[i]
		if !goTopLevelKinds[c.Kind()] {
			continue
		}
		out = append(out, goDeclarations(c, src)...)
	}
	return out
}

func goDeclarations(node *ts.Node, src []byte) []Declaration {
	switch node.Kind() {
	case "method_declaration":
		if d, ok := goMethodDeclaration(node, src); ok {
			return []Declaration{d}
		}
	case "function_declaration":
		if d, ok := namedDecl(src, node, node); ok {
			return []Declaration{d}
		}
	case "const_declaration":
		return goSpecDeclarations(node, src)
	case "var_declaration":
		return goSpecDeclarations(node, src)
	case "type_declaration":
		return goSpecDeclarations(node, src)
	}
	return nil
}

// goMethodDeclaration disambiguates by receiver container. It must read the
// "name" and "receiver" fields, never positional children:
// method_declaration has two parameter_list children (receiver, then
// parameters), and its name node is a field_identifier, not an identifier —
// positional indexing silently picks the wrong node.
func goMethodDeclaration(node *ts.Node, src []byte) (Declaration, bool) {
	name := node.ChildByFieldName("name")
	recv := node.ChildByFieldName("receiver")
	if name == nil || recv == nil {
		return Declaration{}, false
	}
	container := goReceiverContainer(recv, src)
	if container == "" {
		return Declaration{}, false
	}
	return Declaration{
		Node:      node,
		Bare:      nodeText(src, name),
		Container: container,
	}, true
}

// goReceiverContainer normalizes a method's receiver field — "(r *R)" or
// "(r R)" — to the bare container name "R", which is what rgit always
// emits even though gopls itself spells the anchor "(*R).M"
// (docs/ANCHORS.md).
func goReceiverContainer(recv *ts.Node, src []byte) string {
	for _, param := range namedChildren(recv) {
		typ := param.ChildByFieldName("type")
		if typ == nil {
			continue
		}
		if typ.Kind() == "pointer_type" {
			if inner := typ.NamedChild(0); inner != nil {
				return nodeText(src, inner)
			}
			continue
		}
		return nodeText(src, typ)
	}
	return ""
}

// goSpecDeclarations addresses each spec of a const/var/type declaration
// separately when the declaration groups several, and the whole declaration
// when it holds one. The name lives on the spec node (const_spec, var_spec,
// type_spec, type_alias), never on the declaration itself.
//
// Resolving a grouped block to its first spec alone would report an edit to
// Beta in `const ( Alpha = 1; Beta = 2 )` as a change to Alpha: a label that
// validates while naming a symbol the caller never touched.
//
// A single-spec declaration keeps the whole node so its extent covers the
// keyword; a grouped spec cannot, since the keyword and parentheses belong
// to the block rather than to any one spec.
func goSpecDeclarations(node *ts.Node, src []byte) []Declaration {
	specs := goSpecs(node)
	if len(specs) == 0 {
		return nil
	}
	if len(specs) == 1 {
		d, ok := namedDecl(src, node, specs[0])
		if !ok {
			return nil
		}
		return []Declaration{d}
	}

	out := make([]Declaration, 0, len(specs))
	for _, spec := range specs {
		if d, ok := namedDecl(src, spec, spec); ok {
			out = append(out, d)
		}
	}
	return out
}

// goSpecs collects a declaration's specs. The nesting is not uniform across
// kinds: const and type hold their specs directly, while a grouped var wraps
// them in a var_spec_list, so the walk has to descend through any *_spec_list
// rather than assuming one shape.
func goSpecs(node *ts.Node) []*ts.Node {
	var out []*ts.Node
	for _, child := range namedChildren(node) {
		switch {
		case strings.HasSuffix(child.Kind(), "_spec_list"):
			out = append(out, goSpecs(&child)...)
		case strings.HasSuffix(child.Kind(), "_spec"):
			out = append(out, &child)
		}
	}
	return out
}

var goImportKinds = []string{"import_declaration"}

func (g *goLanguage) ImportKinds() []string { return goImportKinds }

var goHeaderKinds = []string{"comment", "package_clause"}

func (g *goLanguage) HeaderKinds() []string { return goHeaderKinds }
