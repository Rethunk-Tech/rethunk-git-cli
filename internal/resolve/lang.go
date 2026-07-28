// Package resolve maps a symbol anchor to a byte extent in a source file.
//
// Tree-sitter is the primary resolver and always produces the extent that gets
// staged; a language server, when reachable, only verifies it. Extents are byte
// offsets rather than line numbers because line-based ranges are exactly the
// fragility symbol anchors exist to avoid.
package resolve

import ts "github.com/tree-sitter/go-tree-sitter"

// Extent is a half-open byte range [Start, End) in a source file.
type Extent struct {
	Start uint
	End   uint
}

// Declaration is one addressable top-level entity, as reported by a grammar
// adapter. It carries the outermost node — for an exported TypeScript function
// that is the export_statement, and for a decorated Python function the
// decorated_definition — because a caller naming that symbol means the whole
// statement, not the declaration buried inside it.
//
// Doc-comment attribution, ordinals, and the bare/qualified name indexes are
// computed by the core resolver, not by adapters, so the blank-line rule cannot
// drift between languages.
type Declaration struct {
	// Node is the outermost node whose extent is staged.
	Node *ts.Node

	// Bare is the unqualified name, e.g. "Get".
	Bare string

	// Container qualifies Bare when the language nests the symbol, e.g. the
	// receiver type "A" for Go's (a *A) Get. Empty when there is none.
	Container string
}

// Language adapts one tree-sitter grammar. An implementation reports which
// nodes are addressable and how they are named; it does not compute extents.
type Language interface {
	// Name is the identifier used in diagnostics and language-server routing,
	// e.g. "go", "typescript", "python".
	Name() string

	// Extensions lists the file suffixes this grammar claims, including the
	// leading dot.
	Extensions() []string

	// TSLanguage returns the compiled grammar.
	TSLanguage() *ts.Language

	// IsComment reports whether a node kind is a comment, for doc attribution.
	IsComment(kind string) bool

	// Declarations returns every addressable top-level declaration in source
	// order. Nodes that own no symbol are omitted; the core resolver reports
	// their hunks as unanchorable.
	Declarations(src []byte, root *ts.Node) []Declaration

	// ImportKinds lists the node kinds @imports spans. It is a list, not a
	// single kind, because Go emits one import_declaration while TypeScript
	// and Python emit one node per import and Python distinguishes
	// import_statement from import_from_statement.
	ImportKinds() []string

	// HeaderKinds lists the node kinds belonging to @header — shebang,
	// build tags, copyright, package clause.
	HeaderKinds() []string
}

// registered holds every adapter, keyed by file extension. Adapters add
// themselves from an init function in their own file so that adding a grammar
// touches exactly one file and no shared registry.
var registered = map[string]Language{}

// register claims each of l's extensions. It panics on a duplicate claim,
// which can only be a programming error: two grammars fighting over one
// extension would make resolution depend on package initialisation order.
func register(l Language) {
	for _, ext := range l.Extensions() {
		if prior, dup := registered[ext]; dup {
			panic("resolve: " + ext + " claimed by both " + prior.Name() + " and " + l.Name())
		}
		registered[ext] = l
	}
}

// ForExtension returns the adapter claiming ext (including the leading dot).
// A false result means the language is unsupported, which callers report as
// exit 9 — not an error, since naming the path still works.
func ForExtension(ext string) (Language, bool) {
	l, ok := registered[ext]
	return l, ok
}
