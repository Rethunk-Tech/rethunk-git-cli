package resolve

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

func init() { register(newRustLanguage()) }

// rustLanguage adapts the tree-sitter Rust grammar.
type rustLanguage struct {
	defaultLanguage
	lang *ts.Language
}

func newRustLanguage() *rustLanguage {
	return &rustLanguage{lang: rustGrammar()}
}

func (r *rustLanguage) Name() string { return "rust" }

func (r *rustLanguage) Extensions() []string { return []string{".rs"} }

func (r *rustLanguage) TSLanguage() *ts.Language { return r.lang }

// IsComment covers both spellings, including their doc forms: Rust's own
// "///" and "//!" are line_comment nodes, not a distinct kind.
func (r *rustLanguage) IsComment(kind string) bool {
	return kind == "line_comment" || kind == "block_comment"
}

// attachesPrefix implements prefixAttacher for Rust's outer attributes.
// tree-sitter makes "#[test]" a sibling of the item it annotates rather
// than a child or a wrapper, so without this the item's extent begins after
// its own attributes: deleting an attributed item orphaned them, and
// staging a new one would have dropped them.
func (r *rustLanguage) attachesPrefix(kind string) bool {
	return kind == "attribute_item"
}

// crossCheckUsesFullExtent is true: rust-analyzer ranges an item from the
// first line of its doc comment or attributes, not from the "fn"/"struct"
// keyword, so the extent to compare it against is the one that already
// carries both.
func (r *rustLanguage) crossCheckUsesFullExtent() bool { return true }

// extendThroughSeparator implements separatorExtender: a struct field and an
// enum variant end before their own trailing comma, so the extent grows over
// it (and any spaces between). Only the comma is taken -- never the newline
// after it -- so the member still occupies exactly its own line and the
// blank-line conventions around it are untouched.
//
// Applied to every node kind rather than only the two: an item that has no
// trailing comma has nothing to extend over, so the scan simply finds none.
func (r *rustLanguage) extendThroughSeparator(src []byte, node *ts.Node) uint {
	end := node.EndByte()
	for i := end; i < uint(len(src)); i++ {
		switch src[i] {
		case ' ', '\t':
		case ',':
			return i + 1
		default:
			return end
		}
	}
	return end
}

// ImportKinds is both routes a name enters scope by: a "use" path, and the
// 2015-edition "extern crate" that still appears in older sources.
func (r *rustLanguage) ImportKinds() []string {
	return []string{"use_declaration", "extern_crate_declaration"}
}

// HeaderKinds is the inner attribute run a crate or module opens with --
// "#![allow(...)]", "#![no_std]". An inner doc comment ("//!") is an ordinary
// line_comment and is attributed to whatever follows it, the same as any
// other doc comment, rather than being claimed by @header.
func (r *rustLanguage) HeaderKinds() []string { return []string{"inner_attribute_item"} }

// OwnsTrailingSeparator is false: rustfmt neither inserts nor removes the
// blank line after a use block, so whatever separates imports from the first
// item is the author's own spacing rather than a structural part of
// @imports. Only gofmt makes that guarantee unconditionally.
func (r *rustLanguage) OwnsTrailingSeparator() bool { return false }

// MembersSitFlush is true for the same reason Go answers true: rustfmt
// leaves struct fields, enum variants, and impl items exactly as spaced as
// the author wrote them, inserting no separator and requiring none, so
// whatever the worktree already has is "flush" as far as the formatter is
// concerned. The wider separator an impl block usually carries between
// methods is reproduced by editOp's own leadGap/trailGap, which only ever
// grow past this minimum.
func (r *rustLanguage) MembersSitFlush() bool { return true }

// rustItemKinds are the addressable item kinds, each carrying its own "name"
// field. impl_item is deliberately absent: it has no name field at all and
// is named by rustImplName instead.
var rustItemKinds = map[string]bool{
	"function_item":    true,
	"struct_item":      true,
	"enum_item":        true,
	"union_item":       true,
	"trait_item":       true,
	"mod_item":         true,
	"const_item":       true,
	"static_item":      true,
	"type_item":        true,
	"macro_definition": true,
}

func (r *rustLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	return rustDeclarations(src, root, "")
}

// rustDeclarations reports every item in a node's own child list, plus the
// members of the ones that hold items: a struct's fields, an enum's
// variants, a trait's and an impl's associated items, and a module's own
// contents. Qualification is one level -- the nearest enclosing item's bare
// name -- the same nearest-ancestor rule every other nesting adapter uses.
//
// A module is descended into because that is where real Rust puts code a
// caller wants to name: every one of heft's 17 source files carries a
// "#[cfg(test)] mod tests { ... }", so refusing to descend would leave that
// crate's entire test suite unaddressable. A function body is not descended
// into -- a function declared inside another is not addressable in any
// language here.
func rustDeclarations(src []byte, node *ts.Node, container string) []Declaration {
	var out []Declaration
	for _, child := range namedChildren(node) {
		item := child
		kind := item.Kind()

		if kind == "impl_item" {
			name := rustImplName(src, &item)
			if name == "" {
				continue
			}
			out = append(out, Declaration{Node: &item, Bare: name, Container: container, Sep: rustSep(container)})
			out = append(out, rustMembers(src, &item, rustImplContainer(src, &item))...)
			continue
		}

		if !rustItemKinds[kind] {
			continue
		}
		nameNode := item.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		bare := nodeText(src, nameNode)
		out = append(out, Declaration{Node: &item, Bare: bare, Container: container, Sep: rustSep(container)})

		switch kind {
		case "struct_item", "enum_item", "union_item", "trait_item":
			out = append(out, rustMembers(src, &item, bare)...)
		case "mod_item":
			if body := rustBody(&item); body != nil {
				out = append(out, rustDeclarations(src, body, bare)...)
			}
		}
	}
	return out
}

// rustMembers reports the named children of an item's own body -- a struct's
// field_declaration list, an enum's enum_variant list, a trait's
// function_signature_item and default methods, an impl's function_item,
// const_item and type_item -- each qualified by container.
func rustMembers(src []byte, item *ts.Node, container string) []Declaration {
	body := rustBody(item)
	if body == nil || container == "" {
		return nil
	}
	var out []Declaration
	for _, child := range namedChildren(body) {
		member := child
		nameNode := member.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		out = append(out, Declaration{
			Node:      &member,
			Bare:      nodeText(src, nameNode),
			Container: container,
			Sep:       "::",
		})
	}
	return out
}

// rustBody finds the child holding an item's members. The kind differs per
// item -- declaration_list for impl/trait/mod, field_declaration_list for a
// struct, enum_variant_list for an enum -- and a function's own "body" field
// is deliberately not among them, so a nested function is never reached.
func rustBody(item *ts.Node) *ts.Node {
	for _, child := range namedChildren(item) {
		switch child.Kind() {
		case "declaration_list", "field_declaration_list", "enum_variant_list":
			body := child
			return &body
		}
	}
	return nil
}

// rustImplName names an impl block by how Rust itself reads it -- "impl
// Config", "impl Render for Config" -- rather than by its type alone. The
// bare type would collide with the struct of that name in every file that
// declares both, which is the ordinary case, and the "impl " prefix is also
// what a caller would think to type. The same reasoning names a CSS at-rule
// by its full prelude rather than the bare keyword.
func rustImplName(src []byte, item *ts.Node) string {
	typ := item.ChildByFieldName("type")
	if typ == nil {
		return ""
	}
	name := "impl " + nodeText(src, typ)
	if trait := item.ChildByFieldName("trait"); trait != nil {
		name = "impl " + nodeText(src, trait) + " for " + nodeText(src, typ)
	}
	return name
}

// rustImplContainer qualifies an impl's own members by the type they are
// implemented on, never by the impl's longer display name: an associated
// function is called as "Config::new" whatever impl block it sits in, and a
// trait method on the same type reads "Config::render" the same way. Two
// impls defining the same member name for one type collide into an ordinal,
// which is the ordinary answer to a repeated name.
func rustImplContainer(src []byte, item *ts.Node) string {
	typ := item.ChildByFieldName("type")
	if typ == nil {
		return ""
	}
	return strings.TrimSpace(nodeText(src, typ))
}

// rustSep is "::" whenever there is a container to join to, matching Rust's
// own path syntax -- "tests::parses_empty" is what a caller types, not the
// dot every other adapter's convention produces. Empty at the top level,
// where Declaration.Sep is meaningless.
func rustSep(container string) string {
	if container == "" {
		return ""
	}
	return "::"
}
