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

// goTopLevelKinds are the addressable top-level node kinds pinned by
// contracts-waveB.md: functions and methods by their own "name" field,
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
		if d, ok := goNamedDeclaration(node, src, "function"); ok {
			return []Declaration{d}
		}
	case "const_declaration":
		return goSpecDeclarations(node, src, "const")
	case "var_declaration":
		return goSpecDeclarations(node, src, "var")
	case "type_declaration":
		return goSpecDeclarations(node, src, "type")
	}
	return nil
}

func goNamedDeclaration(node *ts.Node, src []byte, kind string) (Declaration, bool) {
	name := node.ChildByFieldName("name")
	if name == nil {
		return Declaration{}, false
	}
	return Declaration{Node: node, Bare: nodeText(src, name), Kind: kind}, true
}

// goMethodDeclaration disambiguates by receiver container. It must read the
// "name" and "receiver" fields, never positional children:
// method_declaration has two parameter_list children (receiver, then
// parameters), and its name node is a field_identifier, not an identifier —
// positional indexing silently picks the wrong node (contracts-waveB.md).
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
		Kind:      "method",
	}, true
}

// goReceiverContainer normalizes a method's receiver field — "(r *R)" or
// "(r R)" — to the bare container name "R", which is what rgit always
// emits even though gopls itself spells the anchor "(*R).M"
// (docs/ANCHORS.md).
func goReceiverContainer(recv *ts.Node, src []byte) string {
	for _, param := range namedChildren(recv) {
		param := param
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

// goSpecWrappedDeclaration handles const/var/type declarations, none of
// which carry a "name" field of their own — the name lives on the spec
// node(s) they wrap (const_spec, var_spec or var_spec_list, type_spec or
// type_alias). A grouped block ("const a = 1\nb = 2") takes the first
// spec's name, matching spike/synth.py's same first-spec shortcut for
// multi-name specs.
// goSpecDeclarations addresses each spec of a const/var/type declaration
// separately when the declaration groups several, and the whole declaration
// when it holds one.
//
// A grouped block that resolved to its first spec alone was measured
// reporting the wrong symbol: editing Beta in `const ( Alpha = 1; Beta = 2 )`
// showed up as a change to Alpha, and the anchor round-trip still passed
// because Alpha does resolve. A label that validates while naming a symbol
// the caller did not touch is worse than no label.
//
// A single-spec declaration keeps the whole node so its extent covers the
// `const`/`var`/`type` keyword; a grouped spec cannot, since the keyword and
// parentheses belong to the block rather than to any one spec.
func goSpecDeclarations(node *ts.Node, src []byte, kind string) []Declaration {
	specs := goSpecs(node)
	if len(specs) == 0 {
		return nil
	}
	if len(specs) == 1 {
		name := specs[0].ChildByFieldName("name")
		if name == nil {
			return nil
		}
		return []Declaration{{Node: node, Bare: nodeText(src, name), Kind: kind}}
	}

	out := make([]Declaration, 0, len(specs))
	for _, spec := range specs {
		name := spec.ChildByFieldName("name")
		if name == nil {
			continue
		}
		out = append(out, Declaration{Node: spec, Bare: nodeText(src, name), Kind: kind})
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
		child := child
		switch {
		case strings.HasSuffix(child.Kind(), "_spec_list"):
			out = append(out, goSpecs(&child)...)
		case strings.HasSuffix(child.Kind(), "_spec"):
			out = append(out, &child)
		}
	}
	return out
}

func nodeText(src []byte, n *ts.Node) string {
	return string(src[n.StartByte():n.EndByte()])
}

var goImportKinds = []string{"import_declaration"}

func (g *goLanguage) ImportKinds() []string { return goImportKinds }

var goHeaderKinds = []string{"comment", "package_clause"}

func (g *goLanguage) HeaderKinds() []string { return goHeaderKinds }
