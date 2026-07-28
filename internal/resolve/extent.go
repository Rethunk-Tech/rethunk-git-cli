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

// fullExtent is [docStart, node.EndByte()) — the symbol together with any
// leading comments attributed to it.
func fullExtent(lang Language, src []byte, node *ts.Node) Extent {
	return Extent{Start: docStart(lang, src, node), End: node.EndByte()}
}

// declOnlyExtent is the declaration node's own range, excluding any
// attributed doc comment. It exists solely for the later LSP cross-check:
// LSP symbol ranges exclude doc comments, so comparing unnormalized extents
// would hard-fail every documented symbol — measured against live gopls,
// where ValidateToken is L5..L14 raw but L9..L14 in gopls (specs/design.md).
func declOnlyExtent(node *ts.Node) Extent {
	return Extent{Start: node.StartByte(), End: node.EndByte()}
}
