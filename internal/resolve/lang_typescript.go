package resolve

import ts "github.com/tree-sitter/go-tree-sitter"

// TypeScript and TSX are two grammars, not one grammar with a mode flag: the
// two disagree on whether a leading `<` opens a type assertion or a JSX
// element, so a .tsx file parsed as TypeScript yields ERROR nodes on the
// first JSX expression (see grammars.go). Both share the same declaration
// shapes otherwise, so the walk below is written once and reused by both
// registrations.

// tsFamily implements Language for both the TypeScript and TSX grammars. The
// two differ only in TSLanguage and Extensions.
type tsFamily struct {
	name string
	exts []string
	lang func() *ts.Language
}

func (l *tsFamily) Name() string             { return l.name }
func (l *tsFamily) Extensions() []string     { return l.exts }
func (l *tsFamily) TSLanguage() *ts.Language { return l.lang() }

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

// AllowsRawHeadingFallback is false: TypeScript has no heading concept for
// the fallback to apply to.
func (l *tsFamily) AllowsRawHeadingFallback() bool { return false }

// Declarations walks the top-level (program) children. The trap this exists
// to avoid: an exported symbol is not a top-level function_declaration, it is
// an export_statement wrapping one. export_statement always exposes the
// wrapped node via the "declaration" field, including "export default class
// C", where a naive implementation might expect "value" instead.
func (l *tsFamily) Declarations(src []byte, root *ts.Node) []Declaration {
	var decls []Declaration
	n := root.NamedChildCount()
	for i := range n {
		outer := root.NamedChild(i)
		if outer == nil {
			continue
		}
		target := outer
		switch outer.Kind() {
		case "export_statement":
			if d := outer.ChildByFieldName("declaration"); d != nil {
				target = d
			} else {
				// export { a, b } or export * from "…" — a re-export
				// statement with no wrapped declaration of its own.
				// Nothing to name; it is not an addressable symbol.
				continue
			}
		case "expression_statement":
			// A bare (non-exported) `namespace N { ... }` parses as an
			// expression_statement wrapping internal_module, not as the
			// internal_module directly (`export namespace N {}` instead
			// wraps it in export_statement, handled by the case above). Only
			// this one wrapped shape is unwrapped here; an ordinary expression
			// statement like `foo();` has no name and must keep falling
			// through to declarationFor's default case.
			if only := onlyNamedChild(outer); only != nil &&
				(only.Kind() == "internal_module" || only.Kind() == "module") {
				target = only
			}
		}

		for _, d := range declarationFor(outer, target, src) {
			decls = append(decls, d)
			switch target.Kind() {
			case "class_declaration", "abstract_class_declaration":
				decls = append(decls, classMembers(target, d.Bare, src)...)
			case "internal_module", "module":
				decls = append(decls, moduleMembers(target, d.Bare, src)...)
			}
		}
	}
	return decls
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
	for i := uint(0); i < body.NamedChildCount(); i++ {
		member := body.NamedChild(i)
		switch member.Kind() {
		case "method_definition", "public_field_definition":
		default:
			continue
		}
		if d, ok := namedDecl(src, member, member); ok {
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
	case "function_declaration":
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
		// are out of scope for v1 — an enum's own values are not addressed,
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
	for i := uint(0); i < body.NamedChildCount(); i++ {
		member := body.NamedChild(i)
		target := member
		if member.Kind() == "export_statement" {
			d := member.ChildByFieldName("declaration")
			if d == nil {
				continue
			}
			target = d
		}
		for _, d := range declarationFor(member, target, src) {
			d.Container = container
			out = append(out, d)
		}
	}
	return out
}

// newTypeScriptLanguage claims .ts, .mts, .cts.
func newTypeScriptLanguage() Language {
	return &tsFamily{
		name: "typescript",
		exts: []string{".ts", ".mts", ".cts"},
		lang: typescriptGrammar,
	}
}

// newTSXLanguage claims .tsx, .jsx, .js, .mjs, .cjs. Plain JavaScript files
// parse under the TSX grammar rather than the TypeScript one: TSX is a
// superset that also accepts untyped JS, and routing .js through it is what
// lets a .jsx file with no extension change still resolve JSX elements.
func newTSXLanguage() Language {
	return &tsFamily{
		name: "tsx",
		exts: []string{".tsx", ".jsx", ".js", ".mjs", ".cjs"},
		lang: tsxGrammar,
	}
}

func init() {
	register(newTypeScriptLanguage())
	register(newTSXLanguage())
}
