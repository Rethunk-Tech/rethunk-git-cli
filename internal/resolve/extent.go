package resolve

import (
	"bytes"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// docStart walks backward through node's named siblings while they are
// comments, extending the start of the extent over each one. It stops the
// first time the gap between a comment's end and the current start contains
// more than one newline — the blank-line rule: a comment block directly
// above a symbol with no intervening blank line belongs to it; a blank line
// breaks the association (docs/ANCHORS.md).
func docStart(lang Language, src []byte, node *ts.Node) uint {
	attaches := func(string) bool { return false }
	if a, ok := lang.(prefixAttacher); ok {
		attaches = a.attachesPrefix
	}

	start := node.StartByte()
	prev := node.PrevNamedSibling()
	for prev != nil {
		switch {
		case attaches(prev.Kind()):
			// No blank-line rule: an attribute binds to its item as a
			// matter of syntax, not layout, so a blank line between them
			// does not break the association the way it does for a
			// comment.
		case lang.IsComment(prev.Kind()):
			gap := src[prev.EndByte():start]
			if bytes.Count(gap, []byte{'\n'}) > 1 {
				return start
			}
		default:
			return start
		}
		start = prev.StartByte()
		prev = prev.PrevNamedSibling()
	}
	return start
}

// fullExtentCrossChecker is an optional refinement of Language for a server
// whose documentSymbol range covers the declaration together with its own
// doc comment and attributes, rather than the declaration alone.
// declOnlyExtent exists because gopls and most others exclude a doc comment;
// rust-analyzer includes it, so comparing Rust against the declaration-only
// extent reported a disagreement on every documented item -- measured, 64 of
// 183 symbols across three real files, every one differing only in where it
// started.
type fullExtentCrossChecker interface {
	crossCheckUsesFullExtent() bool
}

// prefixAttacher is an optional refinement of Language for a grammar whose
// declaration is preceded by sibling nodes that belong to it without being
// comments. lang_rust.go is the only implementer: Rust's outer attributes
// ("#[test]", "#[derive(Debug)]") are siblings of the item they annotate,
// not children of it and not a wrapper around it the way Python's
// decorated_definition wraps its decorators.
//
// Without this the extent starts after the attribute, so deleting an item
// left its attribute orphaned -- measured, deleting one #[test] function
// from a mod committed a file with two consecutive #[test] lines above the
// surviving one, which does not compile.
type prefixAttacher interface {
	attachesPrefix(kind string) bool
}

// fullExtent is [docStart, extentEnd) — the symbol together with any leading
// comments attributed to it.
func fullExtent(lang Language, src []byte, node *ts.Node) Extent {
	return Extent{Start: docStart(lang, src, node), End: extentEnd(lang, src, node)}
}

// trailingCommentTrimmer is an optional refinement of Language for a grammar
// whose external scanner grafts a comment onto the wrong node's trailing
// edge. lang_yaml.go is the only implementer: a comment before a shallower
// sibling becomes the trailing child of whatever block was still open when
// the scanner consumed it, regardless of its written column, because the
// scanner decides whether to dedent from the next real line -- which it has
// not looked ahead to when it emits the comment token.
//
// extentEnd consults this when a Language implements it, falling back to
// node.EndByte() otherwise.
type trailingCommentTrimmer interface {
	trimTrailingComment(src []byte, node *ts.Node) uint
}

func extentEnd(lang Language, src []byte, node *ts.Node) uint {
	if t, ok := lang.(trailingCommentTrimmer); ok {
		return t.trimTrailingComment(src, node)
	}
	if e, ok := lang.(separatorExtender); ok {
		return e.extendThroughSeparator(src, node)
	}
	return node.EndByte()
}

// separatorExtender is an optional refinement of Language for a grammar
// that ends a list member before its own separator. lang_rust.go is the only
// implementer: a struct field's and an enum variant's node stops at
// "pub name: String", leaving the "," a sibling token of the list rather
// than part of the member.
//
// Deleting such a member is what makes that a defect rather than a detail --
// measured, removing one field from a struct committed a file with a bare
// "," on its own line, which does not compile. Excising the separator along
// with the member is what git's own diff of the same edit does.
//
// Mutually exclusive with trailingCommentTrimmer above, which no grammar
// needs both of: one shrinks an over-wide extent, this one grows an
// under-wide one.
type separatorExtender interface {
	extendThroughSeparator(src []byte, node *ts.Node) uint
}

// declOnlyEndTrimmer is declOnlyExtent's optional refinement, separate from
// trailingCommentTrimmer because the two fix unrelated defects (YAML's
// scanner misattaching a comment vs. HTML's node absorbing a void element's
// trailing content) and neither should force the other's fix. lang_html.go
// and lang_yaml.go implement it.
//
// Unlike trailingCommentTrimmer, which extentEnd consults for the full
// staged extent, this affects only the declaration-only extent the LSP
// cross-check compares against -- the void-element absorption stays in what
// actually gets staged.
type declOnlyEndTrimmer interface {
	trimDeclOnlyEnd(src []byte, node *ts.Node) uint
}

// declOnlyExtent is the declaration node's own range, excluding any
// attributed doc comment. It exists solely for the later LSP cross-check:
// LSP symbol ranges exclude doc comments, so comparing unnormalized extents
// would hard-fail every documented symbol — ValidateToken is L5..L14 raw
// but L9..L14 via gopls.
func declOnlyExtent(lang Language, src []byte, d Declaration) Extent {
	node := d.Node
	end := node.EndByte()
	if t, ok := lang.(declOnlyEndTrimmer); ok {
		end = t.trimDeclOnlyEnd(src, node)
	}
	start := node.StartByte()
	if d.NameNode != nil {
		start = d.NameNode.StartByte()
	}
	return Extent{Start: start, End: end}
}
