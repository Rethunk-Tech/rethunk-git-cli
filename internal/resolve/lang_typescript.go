package resolve

import ts "github.com/tree-sitter/go-tree-sitter"

// TypeScript and TSX are two grammars, not one grammar with a mode flag: the
// two disagree on whether a leading `<` opens a type assertion or a JSX
// element, so a .tsx file parsed as TypeScript yields ERROR nodes on the
// first JSX expression (see grammars.go). Both share the same declaration
// shapes otherwise, so the walk below is written once and reused by both
// registrations.

// tsFamily implements Language for both the TypeScript and TSX grammars. The
// two differ only in TSLanguage and Extensions. lang is resolved once, in
// the constructors below, and stored rather than re-invoked on every parse
// -- every other adapter in this package caches its *ts.Language the same
// way (e.g. goLanguage in lang_go.go); ts.NewLanguage wraps the grammar's C
// symbol table, which does not change between parses of the same language.
type tsFamily struct {
	defaultLanguage
	name string
	exts []string
	lang *ts.Language
}

func (l *tsFamily) Name() string             { return l.name }
func (l *tsFamily) Extensions() []string     { return l.exts }
func (l *tsFamily) TSLanguage() *ts.Language { return l.lang }

// IsComment is true for "comment", the one kind the grammar uses for both
// `//` line comments and `/** */` doc comments.
func (l *tsFamily) IsComment(kind string) bool { return kind == "comment" }

// ImportKinds is a single-element list, but @imports still spans a run: the
// grammar emits one import_statement node per import line, not one node for
// the whole block the way Go's import_declaration does.
func (l *tsFamily) ImportKinds() []string { return []string{"import_statement"} }

// HeaderKinds is hash_bang_line plus comment, matching Python: TypeScript has
// no package clause, so a licence/copyright block opening a file with no
// shebang is an ordinary "comment" node like any other, the same shape
// Python's own header comment parses as (lang_python.go). hash_bang_line
// appears as the first child when present, identically for both
// LanguageTypescript and LanguageTSX.
//
// Including "comment" cannot make @header swallow a documented first
// declaration's doc comment: the core resolver bounds the header run at
// @toplevel's start (headerExtent's limit, resolvePseudo in resolver.go),
// and that start already extends backward over any comment attributed to the
// first declaration by the same blank-line rule every symbol's doc comment
// uses (docStart, extent.go) — the mechanism this reuses rather than
// duplicating, per docs/ANCHORS.md's blank-line rule.
func (l *tsFamily) HeaderKinds() []string { return []string{"hash_bang_line", "comment"} }

// OwnsTrailingSeparator is false: Prettier preserves whatever blank-line
// count the author wrote after a header or import block rather than
// inserting one deterministically the way gofmt does, so claiming that
// blank line as structurally part of @header/@imports would misattribute a
// byte the region does not own.
func (l *tsFamily) OwnsTrailingSeparator() bool { return false }

// MembersSitFlush is true: Prettier leaves class methods and fields exactly
// as spaced as the author wrote them, inserting no separator and requiring
// none, the same as gofmt for Go -- true for both TypeScript and TSX, since
// this method is shared by both registrations (tsFamily's own doc comment).
func (l *tsFamily) MembersSitFlush() bool { return true }

// AllowsRawHeadingFallback is inherited from defaultLanguage: TypeScript
// has no heading concept for the fallback to apply to.

// Declarations walks the top-level (program) children. The trap this exists
// to avoid: an exported symbol is not a top-level function_declaration, it is
// an export_statement wrapping one. export_statement always exposes the
// wrapped node via the "declaration" field, including "export default class
// C", where a naive implementation might expect "value" instead.
func (l *tsFamily) Declarations(src []byte, root *ts.Node) []Declaration {
	var decls []Declaration
	var run overloadRun
	n := root.NamedChildCount()
	targets := make([]*ts.Node, n)
	for i := range n {
		targets[i] = declTarget(root.NamedChild(i))
	}
	for i := range n {
		outer := root.NamedChild(i)
		target := targets[i]
		if outer == nil || target == nil {
			continue
		}

		lead := run.step(src, outer, target)
		if target.Kind() == "function_signature" {
			if lead = run.lead; !orphanSignature(src, targets, int(i)) {
				continue
			}
		}
		for _, d := range declarationFor(outer, target, src) {
			if lead != nil {
				d.Lead = lead
			}
			decls = append(decls, d)
			switch target.Kind() {
			case "class_declaration", "abstract_class_declaration":
				decls = append(decls, classMembers(target, d.Bare, src)...)
			case "internal_module", "module":
				decls = append(decls, moduleMembers(target, d.Bare, src)...)
			case "lexical_declaration", "variable_declaration":
				decls = append(decls, variableExtras(target, d, src)...)
			}
		}
	}
	return decls
}

// declTarget unwraps a top-level statement to the declaration it names: the
// exported declaration, or the namespace a bare `namespace N {}` expression
// statement wraps. It returns nil for a statement with nothing addressable,
// such as `export { a, b }` (a re-export with no wrapped declaration).
func declTarget(outer *ts.Node) *ts.Node {
	if outer == nil {
		return nil
	}
	switch outer.Kind() {
	case "export_statement":
		return unwrapAmbient(outer.ChildByFieldName("declaration"))
	case "ambient_declaration":
		return unwrapAmbient(outer)
	case "expression_statement":
		// A bare (non-exported) `namespace N { ... }` parses as an
		// expression_statement wrapping internal_module, not as the
		// internal_module directly (`export namespace N {}` instead
		// wraps it in export_statement). Only this one wrapped shape is
		// unwrapped; an ordinary expression statement like `foo();` has
		// no name and keeps falling through to declarationFor's default.
		if only := onlyNamedChild(outer); only != nil &&
			(only.Kind() == "internal_module" || only.Kind() == "module") {
			return only
		}
	}
	return outer
}

// unwrapAmbient returns the declaration inside `declare ...`, or n itself.
func unwrapAmbient(n *ts.Node) *ts.Node {
	if n != nil && n.Kind() == "ambient_declaration" {
		if only := onlyNamedChild(n); only != nil {
			return only
		}
	}
	return n
}

// orphanSignature reports whether the function_signature at targets[i] ends a
// run of signatures that no implementation follows, as in a .d.ts file. Such
// a run is addressable as one declaration; a run followed by its
// implementation is folded into that implementation instead.
func orphanSignature(src []byte, targets []*ts.Node, i int) bool {
	nm := targets[i].ChildByFieldName("name")
	if nm == nil {
		return false
	}
	name := nodeText(src, nm)
	for _, t := range targets[i+1:] {
		if t == nil {
			return true
		}
		if t.Kind() == "comment" {
			continue
		}
		next := t.ChildByFieldName("name")
		if next == nil || nodeText(src, next) != name {
			return true
		}
		return t.Kind() != "function_signature" && t.Kind() != "function_declaration"
	}
	return true
}

// onlyNamedChild returns n's sole named child, or nil when n has zero or
// more than one — the guard that keeps the expression_statement unwrap in
// Declarations from ever firing on an ordinary expression statement, which
// has exactly one named child too but of some other kind entirely.
func onlyNamedChild(n *ts.Node) *ts.Node {
	if n.NamedChildCount() != 1 {
		return nil
	}
	return n.NamedChild(0)
}

// variableExtras adds what a `const NAME = <value>` declarator exposes beyond
// NAME itself: an object literal's function members (`api.rescanFleet`), and
// the inner name of a wrapped function expression (`memo(function TurnView()
// {})` also answers to TurnView, over the same span as NAME).
func variableExtras(target *ts.Node, d Declaration, src []byte) []Declaration {
	for j := uint(0); j < target.NamedChildCount(); j++ {
		vd := target.NamedChild(j)
		if vd == nil || vd.Kind() != "variable_declarator" {
			continue
		}
		if name := vd.ChildByFieldName("name"); name == nil || nodeText(src, name) != d.Bare {
			continue
		}
		value := vd.ChildByFieldName("value")
		for value != nil && (value.Kind() == "as_expression" || value.Kind() == "satisfies_expression" || value.Kind() == "parenthesized_expression") {
			value = value.NamedChild(0)
		}
		if value == nil {
			return nil
		}
		switch value.Kind() {
		case "object":
			return objectMembers(value, d.Bare, src)
		case "call_expression":
			return wrappedFunctionAlias(value, d, src)
		}
	}
	return nil
}

// wrappedFunctionAlias indexes the name of a named function expression passed
// as an argument (`memo(function TurnView() {})`) as an alias of d's span.
func wrappedFunctionAlias(call *ts.Node, d Declaration, src []byte) []Declaration {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	for i := uint(0); i < args.NamedChildCount(); i++ {
		fn := args.NamedChild(i)
		if fn.Kind() != "function_expression" && fn.Kind() != "generator_function" {
			continue
		}
		if name := fn.ChildByFieldName("name"); name != nil && nodeText(src, name) != d.Bare {
			alias := d
			alias.Bare = nodeText(src, name)
			return []Declaration{alias}
		}
	}
	return nil
}

// objectMembers enumerates an object literal's function-valued members --
// methods, and properties holding an arrow or function expression --
// container-qualified like classMembers. Plain-data properties stay
// unaddressable.
func objectMembers(obj *ts.Node, container string, src []byte) []Declaration {
	var out []Declaration
	for i := uint(0); i < obj.NamedChildCount(); i++ {
		member := obj.NamedChild(i)
		var key *ts.Node
		switch member.Kind() {
		case "method_definition":
			key = member.ChildByFieldName("name")
		case "pair":
			if v := member.ChildByFieldName("value"); v != nil &&
				(v.Kind() == "arrow_function" || v.Kind() == "function_expression") {
				key = member.ChildByFieldName("key")
			}
		}
		if key == nil || (key.Kind() != "property_identifier" && key.Kind() != "identifier") {
			continue
		}
		out = append(out, Declaration{Node: member, Bare: nodeText(src, key), Container: container})
	}
	return out
}

// extendThroughSeparator implements separatorExtender for object-literal
// members only: deleting `a() {}` from `{ a() {}, b() {} }` must take its
// comma too, or the result is `{ , b() {} }`.
func (l *tsFamily) extendThroughSeparator(src []byte, node *ts.Node) uint {
	end := node.EndByte()
	if p := node.Parent(); p == nil || p.Kind() != "object" {
		return end
	}
	return extentThroughComma(src, end)
}

// overloadRun tracks a run of same-name overload signatures
// (function_signature, or method_signature in a class body) so the
// implementation that follows can start its extent at the first one. Deleting
// the implementation alone would otherwise strand the signatures.
type overloadRun struct {
	name string
	lead *ts.Node
}

// step feeds the next sibling (outer is its extent node, n the unwrapped declaration) and returns the first
// signature of the run when n is an implementation of the same name.
func (r *overloadRun) step(src []byte, outer, n *ts.Node) *ts.Node {
	switch n.Kind() {
	case "comment":
		return nil
	case "function_signature", "method_signature":
		if nm := n.ChildByFieldName("name"); nm != nil {
			name := nodeText(src, nm)
			if r.lead == nil || r.name != name {
				r.name, r.lead = name, outer
			}
			return nil
		}
	case "function_declaration", "method_definition":
		if nm := n.ChildByFieldName("name"); nm != nil && r.lead != nil && r.name == nodeText(src, nm) {
			lead := r.lead
			r.lead = nil
			return lead
		}
	}
	r.lead = nil
	return nil
}

// classMembers enumerates a class body's own members, container-qualified,
// so `svc.ts:UserService.login` addresses one method instead of collapsing
// to the whole class. Without it the finest unit in an idiomatic
// one-class-per-file module is the class, which for staging purposes is the
// same thing as naming the path. It is shared by class_declaration and
// abstract_class_declaration, which both hold their body under a "body"
// field of kind class_body — the two declarations are otherwise unrelated
// node kinds with no common parent, but this function only ever looks at the
// shape the field points to.
//
// class_body holds method_definition (ordinary methods, statics and
// accessors alike) and public_field_definition, each carrying its own
// "name" field. Signature-only members with no body (method_signature,
// abstract_method_signature — legal inside an abstract class) fall through
// the switch below and stay unaddressable, same as interface_declaration's
// members. The extent is the member node, so a doc comment above it is
// attributed by the same blank-line rule as any other declaration.
func classMembers(class *ts.Node, container string, src []byte) []Declaration {
	body := class.ChildByFieldName("body")
	if body == nil {
		return nil
	}
	var out []Declaration
	var run overloadRun
	for i := uint(0); i < body.NamedChildCount(); i++ {
		member := body.NamedChild(i)
		lead := run.step(src, member, member)
		switch member.Kind() {
		case "method_definition", "public_field_definition":
		default:
			continue
		}
		if d, ok := namedDecl(src, member, member); ok {
			d.Lead = lead
			d.Container = container
			out = append(out, d)
		}
	}
	return out
}

// declarationFor names target, the (possibly descended-into) declaration
// node, while reporting outer as the Node to stage. A caller naming
// "file.ts:F" means the whole export_statement, not the function_declaration
// buried inside it — the extent is always the outermost node even though the
// name is found further down.
//
// It returns a slice rather than one (Declaration, bool) because a grouped
// lexical_declaration/variable_declaration wraps several variable_declarator
// children that each need their own entry — every other case here still
// reports at most one.
func declarationFor(outer, target *ts.Node, src []byte) []Declaration {
	switch target.Kind() {
	case "function_declaration", "function_signature":
		return declOne(namedDecl(src, outer, target))
	case "class_declaration":
		return declOne(namedDecl(src, outer, target))
	case "type_alias_declaration":
		return declOne(namedDecl(src, outer, target))
	case "interface_declaration":
		return declOne(namedDecl(src, outer, target))
	case "abstract_class_declaration":
		// name field "type_identifier", same as class_declaration. Members
		// are enumerated by the Declarations loop, exactly as for
		// class_declaration.
		return declOne(namedDecl(src, outer, target))
	case "enum_declaration":
		// name field "identifier"; body is enum_body. Members (Color.Red)
		// are not addressable — an enum's own values are not addressed,
		// only the enum itself, matching how enum_body is never descended
		// into below.
		return declOne(namedDecl(src, outer, target))
	case "generator_function_declaration":
		// Same shape as function_declaration but a distinct grammar kind.
		return declOne(namedDecl(src, outer, target))
	case "lexical_declaration", "variable_declaration":
		// const/let (lexical_declaration) and var (variable_declaration) —
		// two distinct node kinds for what reads like one construct, both
		// wrapping one or more variable_declarator children with no field
		// name of their own. Each declarator is addressed individually, the
		// same fix goSpecDeclarations applies to Go's grouped const/var/type
		// blocks.
		return lexicalDeclarations(outer, target, src)
	case "internal_module", "module":
		// TypeScript spells the common `namespace N {}` internal_module, and
		// the rarer ambient `module "pkg" {}` form module — both carry a
		// "name" field and an optional "body" field. Members are enumerated
		// by the Declarations loop via moduleMembers.
		return declOne(namedDecl(src, outer, target))
	case "function_expression":
		// Deliberately unaddressable, not merely unhandled: this is what
		// `export default function () {}` wraps its anonymous function in —
		// export_statement's "value" field, not "declaration" — and the
		// grammar gives function_expression no name field to read one from.
		// Inventing a stand-in spelling (e.g. a synthetic "default") risks
		// colliding with a real identifier someday, so this stays
		// unaddressable; name the path to stage it (docs/ANCHORS.md). A
		// *named* default export (`export default function f() {}`) is a
		// function_declaration under "declaration" instead, and already
		// resolves via the case above.
		return nil
	default:
		return nil
	}
}

// declOne adapts namedDecl's (Declaration, bool) to declarationFor's slice
// return, so every single-declaration case above can keep sharing namedDecl
// unchanged.
func declOne(d Declaration, ok bool) []Declaration {
	if !ok {
		return nil
	}
	return []Declaration{d}
}

// lexicalDeclarations addresses each variable_declarator in target
// individually. A single declarator keeps outer (the export_statement, if
// any, or the bare declaration) as its extent, matching every other
// declarationFor case, so its extent still covers the `const`/`let`/`var`
// keyword. A grouped statement cannot: the keyword and the commas joining
// declarators belong to the statement as a whole, not to any one of them, so
// each is addressed by its own variable_declarator node alone — mirroring how
// a grouped Go spec's extent is the spec, not the block (goSpecDeclarations).
//
// A destructuring declarator (`const {a, b} = obj`, `const [x, y] = arr`) is
// skipped by identifierDecl below rather than given a fabricated name —
// see its comment for why.
func lexicalDeclarations(outer, target *ts.Node, src []byte) []Declaration {
	var vds []*ts.Node
	for j := uint(0); j < target.NamedChildCount(); j++ {
		if vd := target.NamedChild(j); vd != nil && vd.Kind() == "variable_declarator" {
			vds = append(vds, vd)
		}
	}
	switch len(vds) {
	case 0:
		return nil
	case 1:
		return declOne(identifierDecl(src, outer, vds[0]))
	default:
		out := make([]Declaration, 0, len(vds))
		for _, vd := range vds {
			if d, ok := identifierDecl(src, vd, vd); ok {
				out = append(out, d)
			}
		}
		return out
	}
}

// identifierDecl names vd, a variable_declarator, when its "name" field is a
// plain identifier — e.g. `const a = 1` — and reports it unaddressable
// otherwise.
//
// A destructuring declarator's "name" field is object_pattern or
// array_pattern instead (`const {x, y} = obj`, `const [p, q] = arr`), with
// no single name to read. `const {a, b} = obj` binds two names to one
// right-hand side; there is no way to give `a` its own extent without `b`'s
// (and obj's) text coming along too, the same principle as Go's shared
// `A, B int` field line
// (goStructFields). Reporting (unanchorable) here is a deliberate choice,
// not a missing case — inventing a name from the pattern's own text (e.g.
// "{x, y}") would resolve to an anchor that drags every sibling binding's
// bytes along with it.
func identifierDecl(src []byte, extent, vd *ts.Node) (Declaration, bool) {
	name := vd.ChildByFieldName("name")
	if name == nil || name.Kind() != "identifier" {
		return Declaration{}, false
	}
	return Declaration{Node: extent, Bare: nodeText(src, name)}, true
}

// moduleMembers enumerates a namespace body's own top-level declarations,
// container-qualified, so `ns.ts:N.inner` addresses one function inside
// `namespace N { ... }` instead of collapsing to the whole namespace.
//
// internal_module's (and module's) "body" field is a statement_block whose
// named children are either the declaration directly — an unexported
// member, `namespace N { function
// inner() {} }` — or an export_statement wrapping one, `namespace N { export
// function inner() {} }`. Both are exactly the two shapes declarationFor
// already resolves at the top level, so this reuses it rather than
// duplicating the export-unwrap logic. A member that is itself a container
// (a class or nested namespace) is not descended a second level —
// `N.Cls.method` is out of scope, matching how a top-level class's own
// members are not descended into either.
func moduleMembers(mod *ts.Node, container string, src []byte) []Declaration {
	body := mod.ChildByFieldName("body")
	if body == nil {
		// `declare namespace N` with no body — an ambient signature-only
		// form — has nothing to enumerate.
		return nil
	}
	var out []Declaration
	var run overloadRun
	n := int(body.NamedChildCount())
	targets := make([]*ts.Node, n)
	for i := range n {
		member := body.NamedChild(uint(i))
		if member.Kind() == "export_statement" {
			targets[i] = unwrapAmbient(member.ChildByFieldName("declaration"))
		} else {
			targets[i] = unwrapAmbient(member)
		}
	}
	for i, target := range targets {
		if target == nil {
			continue
		}
		member := body.NamedChild(uint(i))
		lead := run.step(src, member, target)
		if target.Kind() == "function_signature" {
			if lead = run.lead; !orphanSignature(src, targets, i) {
				continue
			}
		}
		for _, d := range declarationFor(member, target, src) {
			d.Container = container
			d.Lead = lead
			out = append(out, d)
		}
	}
	return out
}

// newTypeScriptLanguage claims .ts, .mts, .cts.
func newTypeScriptLanguage() *tsFamily {
	return &tsFamily{
		name: "typescript",
		exts: []string{".ts", ".mts", ".cts"},
		lang: typescriptGrammar(),
	}
}

// newTSXLanguage claims .tsx, .jsx, .js, .mjs, .cjs. Plain JavaScript files
// parse under the TSX grammar rather than the TypeScript one: TSX is a
// superset that also accepts untyped JS, and routing .js through it is what
// lets a .jsx file with no extension change still resolve JSX elements.
func newTSXLanguage() *tsFamily {
	return &tsFamily{
		name: "tsx",
		exts: []string{".tsx", ".jsx", ".js", ".mjs", ".cjs"},
		lang: tsxGrammar(),
	}
}
