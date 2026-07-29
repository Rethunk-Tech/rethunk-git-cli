//go:build rgit_sql

// This build tag must stay a literal -- go's toolchain parses //go:build
// constraints textually, before any Go code compiles, so it cannot
// reference gated.go's SQLBuildTag constant even though the two must
// name the identical string. Changing the tag here means changing
// SQLBuildTag too; gated_test.go's TestGatedTag_AgreesWithForExtension is
// the guard that catches the two drifting apart.

package resolve

import (
	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve/sqlgrammar"
)

func init() { register(newSQLLanguage()) }

// sqlGrammar constructs the compiled SQL grammar. Kept here rather than in
// grammars.go, unlike every other grammar in this resolver: grammars.go is
// compiled unconditionally, and internal/resolve/sqlgrammar's own single Go
// file carries the rgit_sql tag (see its doc comment), so an unconditional
// import from grammars.go would make a plain `go build ./...` try to
// compile a package with zero buildable files. This file carries the same
// tag, so the two are only ever compiled together.
func sqlGrammar() *ts.Language { return ts.NewLanguage(sqlgrammar.Language()) }

// sqlLanguage adapts the tree-sitter SQL grammar
// (github.com/DerekStride/tree-sitter-sql, generated at build/install time --
// see cmd/rgit-install/main.go and specs/design.md § Dependencies).
type sqlLanguage struct {
	defaultLanguage
	lang *ts.Language
}

func newSQLLanguage() *sqlLanguage {
	return &sqlLanguage{lang: sqlGrammar()}
}

func (l *sqlLanguage) Name() string { return "sql" }

func (l *sqlLanguage) Extensions() []string { return []string{".sql"} }

func (l *sqlLanguage) TSLanguage() *ts.Language { return l.lang }

// IsComment: a line comment ("-- ...") parses as "comment"; a block comment
// ("/* ... */") parses as "marginalia" -- a distinct node kind, not the
// same kind spelled two ways.
func (l *sqlLanguage) IsComment(kind string) bool {
	return kind == "comment" || kind == "marginalia"
}

// HeaderKinds is both comment kinds. SQL has no shebang or package clause,
// so a leading run of "-- ..." or "/* ... */" lines is the only thing
// @header can ever claim -- the same "comment kind(s) alone" shape every
// other comment-fronted grammar in this resolver already uses.
func (l *sqlLanguage) HeaderKinds() []string { return []string{"comment", "marginalia"} }

// ImportKinds is inherited from defaultLanguage: this grammar has no
// include/import statement of any kind -- none of its top-level statement
// kinds are shaped like one -- the same degraded-but-not-an-error answer
// TOML, JSON, and Markdown also give.

// OwnsTrailingSeparator is false: no SQL formatting convention this
// resolver relies on inserts a deterministic blank line after a leading
// comment run, so whatever blank line, if any, follows one is the author's
// own spacing, not a structural part of @header.
func (l *sqlLanguage) OwnsTrailingSeparator() bool { return false }

// MembersSitFlush is false, though effectively unreachable in the common
// case: the only Container this adapter sets is a schema qualifying a
// table/view/function/index/type name (sqlObjectReferenceDeclaration), and
// escalateToContainer (classify.go) only treats a member's Container as a
// true nesting when the container's own name resolves to a declaration
// that structurally encloses it -- a schema name is not itself a declared,
// resolvable symbol in this grammar (Declarations names no "schema" kind),
// so that path never fires, the same way a Go receiver's container is "a
// sibling of its methods, not their parent" (escalateToContainer's own doc
// comment). Answered explicitly anyway rather than left to fall through a
// switch by omission.
func (l *sqlLanguage) MembersSitFlush() bool { return false }

// AllowsRawHeadingFallback is inherited from defaultLanguage: SQL has no
// heading concept for the fallback to apply to.

// buildTagGated implements the buildTagGated seam (lang.go). This file
// itself carries the rgit_sql tag, so any build where this method exists to
// be called at all already answers true.
func (l *sqlLanguage) buildTagGated() bool { return true }

// Declarations walks the program root's own named children. A "comment" or
// "marginalia" node sits as a program-level sibling of "statement" nodes,
// not nested inside one, so both are skipped here the same way lang_css.go
// skips "comment" among rule_set's own siblings; the core resolver's
// doc-comment attribution (docStart) walks PrevNamedSibling from the
// "statement" wrapper itself and finds them there regardless.
//
// Every top-level "statement" node wraps exactly one inner statement node,
// across every kind handled below and every one this resolver leaves
// unaddressed. Declarations reports the "statement" wrapper itself
// as Node, not the inner node -- the same "outermost node is what a caller
// means" rule TypeScript's export_statement and Python's
// decorated_definition already follow -- so a caller naming a table also
// gets the ";" or blank line up to its own extent boundary handled by the
// core resolver's normal machinery, no SQL-specific extension needed.
//
// Only CREATE TABLE/VIEW/FUNCTION/INDEX/TRIGGER/TYPE are addressable. DROP,
// ALTER, INSERT, SELECT, and CREATE SCHEMA all parse but declare no
// persistent named object the way the six covered kinds do; CREATE DOMAIN
// does not even parse under this grammar version (it produces an ERROR
// node). None of the excluded kinds clears the measured-demand bar
// specs/design.md § Grammar scope holds every addressable shape to.
func (l *sqlLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	var decls []Declaration
	for _, child := range namedChildren(root) {
		stmt := child
		if stmt.Kind() != "statement" || stmt.NamedChildCount() != 1 {
			continue
		}
		inner := stmt.NamedChild(0)
		if d, ok := sqlDeclarationFor(src, &stmt, inner); ok {
			decls = append(decls, d)
		}
	}
	return decls
}

func sqlDeclarationFor(src []byte, stmt, inner *ts.Node) (Declaration, bool) {
	switch inner.Kind() {
	case "create_table", "create_view", "create_function", "create_trigger", "create_type":
		return sqlObjectReferenceDeclaration(src, stmt, inner)
	case "create_index":
		return sqlIndexDeclaration(src, stmt, inner)
	default:
		// "statement" nodes wrapping anything else -- select, insert,
		// drop_table, alter_table, create_schema, and whatever else this
		// grammar accepts -- name no persistent schema object, per the
		// Declarations doc comment above.
		return Declaration{}, false
	}
}

// sqlObjectReferenceDeclaration names a CREATE TABLE/VIEW/FUNCTION/TRIGGER/
// TYPE statement by its first "object_reference" child's own "name" field --
// the first "object_reference"-kind child in every one of the five
// statement kinds above -- positional rather than a field on the
// statement itself, since tree-sitter-sql declares no field naming that
// child directly (CREATE TRIGGER's own statement carries three
// object_reference children -- its own name, the table it fires on, the
// function it calls -- and the trigger's own name is always the first).
//
// A schema-qualified name ("s.t") carries the schema as object_reference's
// own "schema" field, present only when written in the source; it is read
// as Container, the same one-level qualification a Go receiver type or a
// TOML table header already gives, so both "schema.sql:s.t" and the bare
// "t" (when unambiguous) resolve. CREATE TRIGGER never carries a schema
// field on its own name -- correctly, since Postgres does not allow a
// schema-qualified trigger name -- so a trigger's Container is always
// empty; two same-named triggers in one file (legal when they fire on
// different tables, a real gap this adapter does not close) disambiguate
// with the existing #N ordinal, the same as two same-named Go functions.
//
// A quoted identifier's own text ("\"Users\"") is read verbatim, quotes
// included, rather than unwrapped the way lang_toml.go strips a
// quoted_key's surrounding quote: matching lang_css.go's "a selector's bare
// name is its own text, exactly as written" precedent instead, since a
// caller who wrote a quoted identifier in the source will naturally type it
// quoted in the anchor too.
func sqlObjectReferenceDeclaration(src []byte, stmt, inner *ts.Node) (Declaration, bool) {
	var ref *ts.Node
	for _, c := range namedChildren(inner) {
		if c.Kind() == "object_reference" {
			node := c
			ref = &node
			break
		}
	}
	if ref == nil {
		return Declaration{}, false
	}
	name := ref.ChildByFieldName("name")
	if name == nil {
		return Declaration{}, false
	}
	container := ""
	if schema := ref.ChildByFieldName("schema"); schema != nil {
		container = nodeText(src, schema)
	}
	return Declaration{Node: stmt, Bare: nodeText(src, name), Container: container}, true
}

// sqlIndexDeclaration names a CREATE INDEX statement by create_index's own
// "column" field -- not a copy error in this comment: despite the field's
// name, it holds the index's own identifier ("myidx" in "CREATE INDEX
// myidx ON t (col1, col2)"), a different field from the "column" field each
// entry inside the statement's own "index_fields" carries for the columns
// actually being indexed. An anonymous index ("CREATE INDEX ON t (c)",
// legal SQL -- Postgres synthesizes a name) has no "column" field on
// create_index at all, and is left unaddressable rather than guessing at
// the name Postgres would assign.
func sqlIndexDeclaration(src []byte, stmt, inner *ts.Node) (Declaration, bool) {
	name := inner.ChildByFieldName("column")
	if name == nil {
		return Declaration{}, false
	}
	return Declaration{Node: stmt, Bare: nodeText(src, name)}, true
}
