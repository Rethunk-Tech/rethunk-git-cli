package resolve

import ts "github.com/tree-sitter/go-tree-sitter"

func init() { register(newPythonLanguage()) }

// pythonLanguage adapts the tree-sitter Python grammar.
type pythonLanguage struct {
	defaultLanguage
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
	// as an ordinary "comment" node, so "comment" is the header kind: the
	// core's leading run from byte 0 stops
	// at the first non-comment node regardless, so this never swallows a doc
	// comment sitting elsewhere in the file.
	return []string{"comment"}
}

// OwnsTrailingSeparator is false: Black preserves whatever blank-line count
// the author wrote after a header or import block rather than inserting one
// deterministically the way gofmt does, so claiming that blank line as
// structurally part of @header/@imports would misattribute a byte the
// region does not own.
func (p *pythonLanguage) OwnsTrailingSeparator() bool { return false }

// MembersSitFlush is false: PEP 8 requires exactly one blank line between
// method definitions inside a class body (linters enforce it as E301), and
// a Python class's only addressable member kind is a method (classMembers
// never descends into plain attribute assignments) -- so a newly
// spliced-in method is treated as an ordinary top-level-shaped boundary,
// not a flush one, or the synthesized blob would drop a blank line every
// Python style guide expects there.
func (p *pythonLanguage) MembersSitFlush() bool { return false }

// AllowsRawHeadingFallback is inherited from defaultLanguage: Python has no
// heading concept for the fallback to apply to.

// Declarations walks only the root's named children: top-level symbols
// alone, with class bodies never descended into, matching the Go and
// TypeScript adapters.
func (p *pythonLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	var decls []Declaration
	count := root.NamedChildCount()
	for i := range count {
		node := root.NamedChild(i)
		d, ok := p.declarationFor(src, node)
		if !ok {
			continue
		}
		decls = append(decls, d)
		if cls := classBody(node); cls != nil {
			decls = append(decls, p.classMembers(cls, d.Bare, src)...)
		}
	}
	return decls
}

// classBody returns the class_definition carrying the body, unwrapping a
// decorated_definition, or nil for anything that is not a class. The
// decorator wrapper has no "body" field of its own -- only the definition
// inside it does.
func classBody(node *ts.Node) *ts.Node {
	switch node.GrammarName() {
	case "class_definition":
		return node
	case "decorated_definition":
		count := node.NamedChildCount()
		for i := range count {
			if inner := node.NamedChild(i); inner.GrammarName() == "class_definition" {
				return inner
			}
		}
	}
	return nil
}

// classMembers enumerates a class body's methods, container-qualified, so
// `svc.py:UserService.login` addresses one method rather than the whole
// class. class_definition's "body" is a block whose named children are
// function_definition, or decorated_definition wrapping one -- the same two
// forms declarationFor already handles at module level.
func (p *pythonLanguage) classMembers(class *ts.Node, container string, src []byte) []Declaration {
	body := class.ChildByFieldName("body")
	if body == nil {
		return nil
	}
	var out []Declaration
	count := body.NamedChildCount()
	for i := range count {
		member := body.NamedChild(i)
		var (
			d  Declaration
			ok bool
		)
		switch member.GrammarName() {
		case "function_definition":
			d, ok = namedDecl(src, member, member)
		case "decorated_definition":
			d, ok = p.decoratedDeclaration(src, member)
		default:
			continue
		}
		if ok {
			d.Container = container
			out = append(out, d)
		}
	}
	return out
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
	for i := range count {
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
