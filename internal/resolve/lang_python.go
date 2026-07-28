package resolve

import ts "github.com/tree-sitter/go-tree-sitter"

func init() { register(newPythonLanguage()) }

// pythonLanguage adapts the tree-sitter Python grammar. The node shapes
// this file encodes were measured against a compiled parse tree, not
// guessed from documentation.
type pythonLanguage struct {
	lang *ts.Language
}

func newPythonLanguage() *pythonLanguage {
	return &pythonLanguage{lang: pythonGrammar()}
}

func (p *pythonLanguage) Name() string { return "python" }

func (p *pythonLanguage) Extensions() []string { return []string{".py", ".pyi"} }

func (p *pythonLanguage) TSLanguage() *ts.Language { return p.lang }

func (p *pythonLanguage) IsComment(kind string) bool { return kind == "comment" }

func (p *pythonLanguage) ImportKinds() []string {
	// from-imports are a distinct node kind, not a variant of import_statement;
	// omitting import_from_statement would silently drop every "from x import
	// y" from @imports.
	return []string{"import_statement", "import_from_statement"}
}

func (p *pythonLanguage) HeaderKinds() []string {
	// Python has no package_clause. A shebang or encoding declaration parses
	// as an ordinary "comment" node (verified by parsing a file starting with
	// "#!/usr/bin/env python3" and dumping the root's named children), so
	// "comment" is the header kind: the core's leading run from byte 0 stops
	// at the first non-comment node regardless, so this never swallows a doc
	// comment sitting elsewhere in the file.
	return []string{"comment"}
}

// Declarations walks only the root's named children — v1 addresses top-level
// symbols; class bodies are not descended into, matching the Go and
// TypeScript adapters.
func (p *pythonLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	var decls []Declaration
	count := root.NamedChildCount()
	for i := uint(0); i < count; i++ {
		if d, ok := p.declarationFor(src, root.NamedChild(i)); ok {
			decls = append(decls, d)
		}
	}
	return decls
}

func (p *pythonLanguage) declarationFor(src []byte, node *ts.Node) (Declaration, bool) {
	switch node.GrammarName() {
	case "function_definition":
		return namedDecl(src, node, node)
	case "class_definition":
		return namedDecl(src, node, node)
	case "decorated_definition":
		return p.decoratedDeclaration(src, node)
	case "expression_statement":
		return p.assignmentDeclaration(src, node)
	default:
		return Declaration{}, false
	}
}

// decoratedDeclaration reports the outer decorated_definition as the extent —
// docs/ANCHORS.md treats decorators as part of "this function" — while
// reading the name off the inner function_definition/class_definition, which
// is the only node carrying a "name" field.
func (p *pythonLanguage) decoratedDeclaration(src []byte, node *ts.Node) (Declaration, bool) {
	count := node.NamedChildCount()
	for i := uint(0); i < count; i++ {
		inner := node.NamedChild(i)
		switch inner.GrammarName() {
		case "function_definition":
			return namedDecl(src, node, inner)
		case "class_definition":
			return namedDecl(src, node, inner)
		}
	}
	return Declaration{}, false
}

// assignmentDeclaration reports a module-level "X = 1" as an addressable var.
// Subscripted (d["k"] = 1) and attribute (obj.attr = 1) targets name nothing
// addressable and are skipped rather than reported under a bogus symbol.
func (p *pythonLanguage) assignmentDeclaration(src []byte, node *ts.Node) (Declaration, bool) {
	if node.NamedChildCount() == 0 {
		return Declaration{}, false
	}
	assign := node.NamedChild(0)
	if assign.GrammarName() != "assignment" {
		return Declaration{}, false
	}
	left := assign.ChildByFieldName("left")
	if left == nil || left.GrammarName() != "identifier" {
		return Declaration{}, false
	}
	return Declaration{
		Node: node,
		Bare: nodeText(src, left),
	}, true
}
