package resolve

import ts "github.com/tree-sitter/go-tree-sitter"

// json5Language adapts the tree-sitter JSON5 grammar. Its tree differs from
// JSON's: the root is "file", an object holds "member" nodes, and a member's
// "name" field is either an unquoted "identifier" or a quoted "string".
type json5Language struct {
	defaultLanguage
	lang *ts.Language
}

func newJSON5Language() *json5Language {
	return &json5Language{lang: json5Grammar()}
}

func (j *json5Language) Name() string { return "json5" }

func (j *json5Language) Extensions() []string { return []string{".json5"} }

func (j *json5Language) TSLanguage() *ts.Language { return j.lang }

// StructuredData is true for the same reason as JSON: `rgit commit` refuses a
// FILE:SYMBOL anchor against it.
func (j *json5Language) StructuredData() bool { return true }

func (j *json5Language) IsComment(kind string) bool { return kind == "comment" }

// HeaderKinds is nil, as for JSON: every key sits inside the one top-level
// object, so toplevelExtent widens to that object and @header has no room
// before it.
func (j *json5Language) HeaderKinds() []string { return nil }

func (j *json5Language) OwnsTrailingSeparator() bool { return false }

func (j *json5Language) MembersSitFlush() bool { return false }

// Declarations addresses a top-level object's key paths, container-qualified
// one level in for a nested object, exactly as jsonLanguage does. A document
// whose root is not an object has no key to address.
func (j *json5Language) Declarations(src []byte, root *ts.Node) []Declaration {
	for _, child := range namedChildren(root) {
		if child.Kind() == "comment" {
			continue
		}
		if child.Kind() != "object" {
			return nil
		}
		return json5MemberDeclarations(&child, "", src)
	}
	return nil
}

func json5MemberDeclarations(object *ts.Node, container string, src []byte) []Declaration {
	var out []Declaration
	for _, child := range namedChildren(object) {
		member := child
		if member.Kind() != "member" {
			continue
		}
		bare, ok := json5KeyName(src, member.ChildByFieldName("name"))
		if !ok {
			continue
		}
		out = append(out, Declaration{Node: &member, Bare: bare, Container: container})
		if value := member.ChildByFieldName("value"); value != nil && value.Kind() == "object" {
			out = append(out, json5MemberDeclarations(value, bare, src)...)
		}
	}
	return out
}

// json5KeyName reads a member's name: an identifier verbatim, a quoted string
// without its quotes. An empty string key is left unaddressable, as in JSON.
func json5KeyName(src []byte, name *ts.Node) (string, bool) {
	if name == nil {
		return "", false
	}
	text := nodeText(src, name)
	if name.Kind() == "string" {
		var ok bool
		if text, ok = stripQuotes(text); !ok {
			return "", false
		}
	}
	return text, text != ""
}
