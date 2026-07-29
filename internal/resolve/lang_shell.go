package resolve

import (
	ts "github.com/tree-sitter/go-tree-sitter"
)

func init() { register(newShellLanguage()) }

// shellLanguage adapts the tree-sitter Bash grammar.
//
// Claims .sh and .bash only, deliberately not .zsh: the grammar is a
// POSIX/Bash grammar, and zsh-only syntax (e.g. `foo=($( ))` word-splitting
// differences, `[[ ]]` extensions, `repeat`, associative-array literals
// zsh spells differently) produces ERROR nodes under it — the same reason
// lang_typescript.go keeps TSX and TypeScript as two grammars rather than
// stretching one over both.
type shellLanguage struct {
	defaultLanguage
	lang *ts.Language
}

func newShellLanguage() *shellLanguage {
	return &shellLanguage{lang: bashGrammar()}
}

func (s *shellLanguage) Name() string { return "shell" }

func (s *shellLanguage) Extensions() []string { return []string{".sh", ".bash"} }

func (s *shellLanguage) TSLanguage() *ts.Language { return s.lang }

func (s *shellLanguage) IsComment(kind string) bool { return kind == "comment" }

// ImportKinds is unused: shellLanguage implements ImportMatcher instead,
// which importsExtent consults first (lang.go). It is defined only to
// satisfy the Language interface, and returns nil so a hypothetical caller
// that skipped the type assertion would degrade to "no imports" rather than
// silently spanning every command in the script.
func (s *shellLanguage) ImportKinds() []string { return nil }

// IsImport reports whether n is a `source f.sh` or `. f.sh` line. Both
// forms parse as an ordinary "command" node -- the same kind as any other
// command invocation -- with a required "name" field of kind "command_name"
// whose own byte range is identical to the single "word" child it wraps, so
// reading the command_name node's own text is enough without descending
// further. No other command name plays
// this role, so matching on exactly "source" or "." is precise: `sourced`
// (a command of that name) and a builtin like `eval` are not mistaken for
// it, and a `dot-source`-style helper function of the caller's own naming is
// a command with a different name entirely.
func (s *shellLanguage) IsImport(src []byte, n *ts.Node) bool {
	if n.Kind() != "command" {
		return false
	}
	name := n.ChildByFieldName("name")
	if name == nil {
		return false
	}
	switch nodeText(src, name) {
	case "source", ".":
		return true
	default:
		return false
	}
}

// HeaderKinds is "comment" alone, identical to Python: there is no shebang
// node in the grammar -- a shebang line parses as an ordinary "comment" the
// same way Python's does, so it inherits that already-documented @header
// behaviour (docs/ANCHORS.md) with nothing new to specify.
func (s *shellLanguage) HeaderKinds() []string { return []string{"comment"} }

// OwnsTrailingSeparator is false: no shell formatter this resolver treats as
// authoritative the way gofmt is for Go enforces a deterministic blank line
// after a header comment run, so whatever separates it from the first
// statement is the author's own spacing, not a structural part of @header.
func (s *shellLanguage) OwnsTrailingSeparator() bool { return false }

// MembersSitFlush is false, though effectively unreachable: shell has no
// containers at all (Declarations' own doc comment -- function namespace is
// flat, nothing sets Container), so classify.go's escalateToContainer path
// that consults this never fires for a shell file. Answered explicitly
// anyway, the same discipline ImportKinds above already follows for a
// method that exists only to satisfy the interface.
func (s *shellLanguage) MembersSitFlush() bool { return false }

// AllowsRawHeadingFallback is inherited from defaultLanguage: shell has no
// heading concept for the fallback to apply to.

// Declarations walks the root's own named children -- program's children
// are exactly the hidden `_statement` supertype (src/node-types.json), so
// there is no wrapping node to unwrap the way TypeScript's export_statement
// or Python's decorated_definition require.
//
// Shell has no containers: function namespace is flat (no classes, no
// modules), so nothing here descends a level or sets Container -- a
// redefined function falls to the existing #N ordinal machinery
// (index.go's assignQualifiedNames) unchanged, the same as two Go
// package-level functions accidentally sharing a name would.
func (s *shellLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	var decls []Declaration
	count := root.NamedChildCount()
	for i := range count {
		node := root.NamedChild(i)
		if d, ok := s.declarationFor(src, node); ok {
			decls = append(decls, d)
		}
	}
	return decls
}

func (s *shellLanguage) declarationFor(src []byte, node *ts.Node) (Declaration, bool) {
	switch node.Kind() {
	case "function_definition":
		// Covers both `foo() {}` and `function foo {}` -- one grammar node
		// for both surface forms. The required "name" field is a "word"
		// node; namedDecl reads it the same way every other adapter reads a
		// name field.
		return namedDecl(src, node, node)
	case "variable_assignment":
		return s.variableDeclaration(src, node)
	default:
		return Declaration{}, false
	}
}

// variableDeclaration addresses a bare top-level `NAME=value` line.
// variable_assignment's "name" field is one of two kinds: "variable_name"
// for a plain assignment, or "subscript" for an indexed one (`arr[0]=1`).
// Only the former names a
// single addressable symbol; a subscript's own text ("arr[0]") is not a
// binding name and staging it under that spelling would be inventing a
// naming scheme the grammar does not offer, the same reasoning
// lang_python.go's assignmentDeclaration uses to skip subscripted and
// attribute targets rather than resolve them to a bogus symbol.
func (s *shellLanguage) variableDeclaration(src []byte, node *ts.Node) (Declaration, bool) {
	name := node.ChildByFieldName("name")
	if name == nil || name.Kind() != "variable_name" {
		return Declaration{}, false
	}
	return Declaration{Node: node, Bare: nodeText(src, name)}, true
}
