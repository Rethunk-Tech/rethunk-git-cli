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
	start := node.StartByte()
	prev := node.PrevNamedSibling()
	for prev != nil && lang.IsComment(prev.Kind()) {
		gap := src[prev.EndByte():start]
		if bytes.Count(gap, []byte{'\n'}) > 1 {
			break
		}
		start = prev.StartByte()
		prev = prev.PrevNamedSibling()
	}
	return start
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
	return node.EndByte()
}

// declOnlyEndTrimmer is declOnlyExtent's optional refinement, separate from
// trailingCommentTrimmer because the two fix unrelated defects (YAML's
// scanner misattaching a comment vs. HTML's node absorbing a void element's
// trailing content) and neither should force the other's fix. lang_html.go
// is the only implementer.
//
// Unlike trailingCommentTrimmer, which extentEnd consults for the full
// staged extent, this affects only the declaration-only extent the LSP
// cross-check compares against -- the void-element absorption stays in what
// actually gets staged (specs/design.md § Grammar scope).
type declOnlyEndTrimmer interface {
	trimDeclOnlyEnd(src []byte, node *ts.Node) uint
}

// declOnlyExtent is the declaration node's own range, excluding any
// attributed doc comment. It exists solely for the later LSP cross-check:
// LSP symbol ranges exclude doc comments, so comparing unnormalized extents
// would hard-fail every documented symbol — ValidateToken is L5..L14 raw
// but L9..L14 via gopls (specs/design.md).
func declOnlyExtent(lang Language, src []byte, node *ts.Node) Extent {
	end := node.EndByte()
	if t, ok := lang.(declOnlyEndTrimmer); ok {
		end = t.trimDeclOnlyEnd(src, node)
	}
	return Extent{Start: node.StartByte(), End: end}
}
