package resolve

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

func init() { register(newGoLanguage()) }

type goLanguage struct {
	defaultLanguage
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
		decls := goSpecNameDeclarations(node, specs[0], src)
		if len(decls) == 0 {
			return nil
		}
		if specs[0].Kind() == "type_spec" {
			decls = append(decls, goContainerMembers(specs[0], decls[0].Bare, src)...)
		}
		return decls
	}

	out := make([]Declaration, 0, len(specs))
	for _, spec := range specs {
		decls := goSpecNameDeclarations(spec, spec, src)
		out = append(out, decls...)
		// type ( ... ) groups each spec individually (goSpecs above);
		// members must work the same way inside a grouped block as
		// beside it, so this runs for every spec, not just a lone one.
		if len(decls) > 0 && spec.Kind() == "type_spec" {
			out = append(out, goContainerMembers(spec, decls[0].Bare, src)...)
		}
	}
	return out
}

// goSpecNameDeclarations builds one Declaration per identifier spec's own
// "name" field carries, every one staged at extent. const_spec and var_spec
// allow more than one comma-separated name sharing a single value list
// ("const a, b = 1, 2" is one const_spec, not two) -- unlike TypeScript's
// grouped declarators, which the grammar already separates into their own
// nodes for lexicalDeclarations to give each its own extent, there is no
// sub-range of a shared Go spec that names b without a's text (and the
// keyword) coming along too, the same constraint goStructFields' shared
// "A, B int" field line hits. Rather than resolve only the first name and
// leave the rest silently unaddressable, every name here shares the
// identical extent: resolving "b" now finds the whole spec, same as "a"
// already did, instead of reporting b unresolvable. type_spec and
// type_alias carry exactly one name, so this is a no-op split for them --
// ChildByFieldName's own single-match behavior, generalized to read every
// match rather than just the first.
//
// ChildrenByFieldName also surfaces the anonymous "," tokens joining a
// comma-separated "name" field's identifiers: field() in this grammar tags
// every top-level symbol of the seq it wraps, commas included, not only the
// identifiers -- so each candidate is filtered to IsNamed() before being
// read as a name, or a grouped spec would mint a bogus "," symbol between
// every real one.
func goSpecNameDeclarations(extent, spec *ts.Node, src []byte) []Declaration {
	cursor := spec.Walk()
	defer cursor.Close()
	var out []Declaration
	for _, name := range spec.ChildrenByFieldName("name", cursor) {
		if !name.IsNamed() {
			continue
		}
		out = append(out, Declaration{Node: extent, Bare: nodeText(src, &name)})
	}
	return out
}

// goContainerMembers reaches one level into a type_spec's underlying type:
// a struct's fields (S.Field) or an interface's methods (I.Do), container-
// qualified by the type's own name. type_spec's "type" field holds the
// struct_type or interface_type directly, not a further wrapper node. Any
// other underlying type — an alias, a named slice or map, a defined basic
// type — has no members to reach, and yields nil rather than descending
// into something that isn't a container.
func goContainerMembers(spec *ts.Node, container string, src []byte) []Declaration {
	typ := spec.ChildByFieldName("type")
	if typ == nil {
		return nil
	}
	switch typ.Kind() {
	case "struct_type":
		return goStructFields(typ, container, src)
	case "interface_type":
		return goInterfaceMethods(typ, container, src)
	default:
		return nil
	}
}

// goStructFields enumerates a struct_type's own fields, container-qualified.
// struct_type's sole child is a field_declaration_list, and each
// field_declaration carries its name(s) as field_identifier children rather
// than in a single-valued "name" field — field_declaration.name is itself
// multiple, because "A, B int" is one field_declaration sharing a type
// between two names.
//
// A field_declaration with exactly one name is addressed the ordinary way.
// Zero names means an embedded/anonymous field (`Anon` with no identifier of
// its own) — skipped rather than fabricated from the type name, since
// embedding is not the same construct as declaring a named field. More than
// one name means a shared line like "A, B int": both names denote the same
// byte extent, so there is no way to give A its own anchor without B's text
// silently coming along too (and vice versa) — rather than pick one
// arbitrarily, neither gets a per-name anchor. Name the type or the path.
func goStructFields(structType *ts.Node, container string, src []byte) []Declaration {
	list := structType.NamedChild(0)
	if list == nil || list.Kind() != "field_declaration_list" {
		return nil
	}
	var out []Declaration
	for _, field := range namedChildren(list) {
		if field.Kind() != "field_declaration" {
			continue
		}
		var names []ts.Node
		for _, c := range namedChildren(&field) {
			if c.Kind() == "field_identifier" {
				names = append(names, c)
			}
		}
		if len(names) != 1 {
			continue
		}
		out = append(out, Declaration{Node: &field, Bare: nodeText(src, &names[0]), Container: container})
	}
	return out
}

// goInterfaceMethods enumerates an interface_type's own method elements,
// container-qualified. interface_type holds method_elem (an ordinary
// method) and type_elem (an embedded interface or a type-set constraint
// term) directly as children, with no wrapping list. type_elem names no
// method of its own and is skipped; method_elem carries exactly one name in
// its "name" field, so namedDecl applies unchanged.
func goInterfaceMethods(interfaceType *ts.Node, container string, src []byte) []Declaration {
	var out []Declaration
	for _, elem := range namedChildren(interfaceType) {
		if elem.Kind() != "method_elem" {
			continue
		}
		if d, ok := namedDecl(src, &elem, &elem); ok {
			d.Container = container
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

// OwnsTrailingSeparator is true: gofmt inserts exactly one blank line after
// the package clause and after the import block, unconditionally, so in a
// gofmt'd file that blank line is as much "part of the header" as the
// package clause's own trailing newline (pseudo.go's Language method doc).
func (g *goLanguage) OwnsTrailingSeparator() bool { return true }

// MembersSitFlush is true: gofmt leaves struct fields and interface methods
// exactly as spaced as the author wrote them, inserting no separator and
// requiring none, so whatever the worktree already has is "flush" as far as
// gofmt is concerned.
func (g *goLanguage) MembersSitFlush() bool { return true }

// AllowsRawHeadingFallback is inherited from defaultLanguage: Go has no
// heading concept for the fallback to apply to.
