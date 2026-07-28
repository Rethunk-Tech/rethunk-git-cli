package resolve

import ts "github.com/tree-sitter/go-tree-sitter"

// TypeScript and TSX are two grammars, not one grammar with a mode flag: the
// two disagree on whether a leading `<` opens a type assertion or a JSX
// element, so a .tsx file parsed as TypeScript yields ERROR nodes on the
// first JSX expression (see grammars.go). Both share the same declaration
// shapes otherwise — verified against a compiled parse tree, not assumed —
// so the walk below is written once and reused by both registrations.

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

// HeaderKinds resolved to hash_bang_line by parsing a fixture that opens with
// "#!/usr/bin/env node" and printing the root's named children: hash_bang_line
// appears as the first child when present, and nothing else in the grammar
// can precede code as a preamble (TypeScript has no package clause). Verified
// against both LanguageTypescript and LanguageTSX; the shape is identical.
func (l *tsFamily) HeaderKinds() []string { return []string{"hash_bang_line"} }

// Declarations walks the top-level (program) children. The trap this exists
// to avoid: an exported symbol is not a top-level function_declaration, it is
// an export_statement wrapping one. Verified against a compiled parse tree —
// export_statement always exposes the wrapped node via the "declaration"
// field, including "export default class C", where a naive implementation
// might expect "value" instead.
func (l *tsFamily) Declarations(src []byte, root *ts.Node) []Declaration {
	var decls []Declaration
	n := root.NamedChildCount()
	for i := uint(0); i < n; i++ {
		outer := root.NamedChild(i)
		if outer == nil {
			continue
		}
		target := outer
		if outer.Kind() == "export_statement" {
			if d := outer.ChildByFieldName("declaration"); d != nil {
				target = d
			} else {
				// export { a, b } or export * from "…" — a re-export
				// statement with no wrapped declaration of its own.
				// Nothing to name; it is not an addressable symbol.
				continue
			}
		}

		if d, ok := declarationFor(outer, target, src); ok {
			decls = append(decls, d)
		}
	}
	return decls
}

// declarationFor names target, the (possibly descended-into) declaration
// node, while reporting outer as the Node to stage. A caller naming
// "file.ts:F" means the whole export_statement, not the function_declaration
// buried inside it — the extent is always the outermost node even though the
// name is found further down.
func declarationFor(outer, target *ts.Node, src []byte) (Declaration, bool) {
	switch target.Kind() {
	case "function_declaration":
		return named(outer, target, src, "function")
	case "class_declaration":
		return named(outer, target, src, "class")
	case "type_alias_declaration":
		return named(outer, target, src, "type")
	case "interface_declaration":
		return named(outer, target, src, "interface")
	case "lexical_declaration":
		// const/let bindings — including const-bound arrow functions and
		// function expressions, which the language server reports no
		// differently from any other const. Only the first declarator is
		// addressable; `const a = 1, b = 2` is 8% territory (design.md),
		// not v1 scope.
		for j := uint(0); j < target.NamedChildCount(); j++ {
			vd := target.NamedChild(j)
			if vd != nil && vd.Kind() == "variable_declarator" {
				return named(outer, vd, src, "var")
			}
		}
		return Declaration{}, false
	default:
		return Declaration{}, false
	}
}

// named reads target's "name" field as Bare. Top-level declarations only are
// in scope for v1 (matching the Go adapter), so Container is always empty —
// class methods are not enumerated.
func named(outer, target *ts.Node, src []byte, kind string) (Declaration, bool) {
	nameNode := target.ChildByFieldName("name")
	if nameNode == nil {
		return Declaration{}, false
	}
	return Declaration{
		Node: outer,
		Bare: string(src[nameNode.StartByte():nameNode.EndByte()]),
		Kind: kind,
	}, true
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
