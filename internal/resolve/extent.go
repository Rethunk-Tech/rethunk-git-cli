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

// trailingCommentTrimmer is an optional refinement of Language for a
// grammar whose own external scanner can graft a comment onto the wrong
// node's trailing edge. lang_yaml.go is the only implementer: a comment
// between the end of a nested value and the next, more shallowly indented
// sibling is not attached as that sibling's leading trivia the way every
// other grammar in this resolver places a comment -- it becomes the
// trailing child of whatever block was still structurally open when the
// scanner consumed it, regardless of the comment's own written column,
// because the scanner decides whether to dedent based on the next real
// line, which it has not looked ahead to yet when it emits the comment
// token.
//
// extentEnd consults this when a Language implements it, falling back to
// node.EndByte() unchanged otherwise -- Go, TypeScript, Python, Markdown and
// Shell keep exactly the extent they always computed.
type trailingCommentTrimmer interface {
	trimTrailingComment(src []byte, node *ts.Node) uint
}

func extentEnd(lang Language, src []byte, node *ts.Node) uint {
	if t, ok := lang.(trailingCommentTrimmer); ok {
		return t.trimTrailingComment(src, node)
	}
	return node.EndByte()
}

// declOnlyEndTrimmer is declOnlyExtent's own optional refinement, kept
// separate from trailingCommentTrimmer rather than reused for it: the two
// seams fix unrelated defects (YAML's scanner misattaching a comment vs.
// HTML's own node absorbing a void element's trailing sibling content), and
// a future language needing one must not be forced to also implement a fix
// for the other's problem. lang_html.go is the only implementer today.
//
// Unlike trailingCommentTrimmer (which extentEnd consults for the full,
// staged extent), this seam affects only the declaration-only extent the
// LSP cross-check compares against — the void-element absorption is left
// alone in the extent that actually gets staged (specs/design.md § Grammar
// scope's own "TOML's trailing-blank-line precedent, not YAML's
// misattached-comment one" reasoning for why).
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
