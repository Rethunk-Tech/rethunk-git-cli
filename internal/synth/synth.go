// Package synth constructs the blob that would exist if only the named
// symbols had changed, and stages it -- the two things AGENTS.md assigns
// this package: "blob synthesis" and, via Stage, "staging". Everything
// else (hooks, pathspec matching, credential prompting) stays git's job,
// delegated through internal/gitx.
//
// The algorithm is specs/design.md's "Blob synthesis" section: read HEAD
// and worktree content, resolve each anchor's extent in both, and splice,
// insert, or excise accordingly. Multiple edits in one file apply in reverse
// byte-offset order so an earlier splice cannot invalidate a later
// offset (AGENTS.md's invariant table).
package synth

import (
	"bytes"
	"sort"
)

// editKind is what one resolved target does to a file's HEAD content.
type editKind int

const (
	// editReplace substitutes [start,end) in HEAD with text -- the
	// symbol exists in both HEAD and the worktree.
	editReplace editKind = iota
	// editInsert splices text in at start, with no existing extent to
	// replace -- the symbol is new in the worktree.
	editInsert
	// editDelete excises [start,end) with no replacement -- the symbol
	// existed in HEAD but is gone from the worktree.
	editDelete
)

// editOp is one resolved edit against a file's HEAD content. start/end
// are byte offsets into the ORIGINAL HEAD blob; applyEdits relies on that
// staying true across the whole batch, which is exactly what applying
// edits in descending start order guarantees (AGENTS.md).
type editOp struct {
	kind  editKind
	start uint
	end   uint
	text  []byte
	seq   int // worktree declaration order; meaningful for insertions only
}

// applyEdits synthesizes the final blob for one file: head with every op
// applied. Overlapping extents are coalesced first, then insertions that
// land at the identical byte offset (two brand new symbols with the same
// nearest-existing-sibling, most commonly "no sibling exists at all") are
// merged in worktree source order, so a single splice pass -- sorted
// strictly by descending start -- can apply the whole batch without one
// edit's offset invalidating another's.
func applyEdits(head []byte, ops []editOp) []byte {
	sorted := mergeInsertTies(coalesceOverlaps(ops))
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].start > sorted[j].start })

	out := append([]byte(nil), head...)
	for _, op := range sorted {
		switch op.kind {
		case editReplace:
			out = spliceReplace(out, op.start, op.end, op.text)
		case editDelete:
			out = spliceExcise(out, op.start, op.end)
		case editInsert:
			out = spliceInsert(out, op.start, op.text)
		}
	}
	return out
}

// coalesceOverlaps implements docs/ANCHORS.md's "overlapping or nested
// anchors in one file merge into a single contiguous extent before
// staging". Naming both `foo.go:@toplevel` and a symbol inside it yields
// two extents covering the same bytes, and the descending-offset splice
// pass assumes every op addresses HEAD's ORIGINAL offsets: applying the
// inner one first shifts the bytes the outer one's end still points at, so
// the second splice lands mid-token and writes a blob that does not parse.
//
// The widest extent wins because it is already the merged result -- an
// enclosing extent's replacement text is its own worktree content, which
// contains whatever the nested anchor resolved to, in its new form. Only
// extents that genuinely overlap are dropped; disjoint symbols in one file
// are the ordinary case and all survive.
//
// Extents come from tree-sitter nodes, which nest properly, so overlap here
// always means containment. Ranking by width and keeping the first
// therefore picks the container every time.
func coalesceOverlaps(ops []editOp) []editOp {
	ranked := append([]editOp(nil), ops...)
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].span() > ranked[j].span()
	})

	kept := make([]editOp, 0, len(ranked))
	for _, op := range ranked {
		if !swallowedBy(kept, op) {
			kept = append(kept, op)
		}
	}
	return kept
}

// span is how many HEAD bytes an op addresses. An insertion addresses none
// -- it is a point, not a range -- so insertions sort last and never
// swallow anything.
func (o editOp) span() uint {
	if o.kind == editInsert {
		return 0
	}
	return o.end - o.start
}

// swallowedBy reports whether op is already covered by an op that survived
// ranking. An insertion counts as covered when its point lies anywhere in
// [start,end], the closed interval: a new symbol appended at the very end
// of an enclosing extent is part of that extent's worktree text too, so
// splicing it in again would duplicate it.
func swallowedBy(kept []editOp, op editOp) bool {
	for _, k := range kept {
		if k.kind == editInsert {
			continue
		}
		if op.kind == editInsert {
			if op.start >= k.start && op.start <= k.end {
				return true
			}
			continue
		}
		if op.start < k.end && k.start < op.end {
			return true
		}
	}
	return false
}

// mergeInsertTies combines editInsert ops that share a start offset into
// one, concatenating their text in ascending worktree order (seq) joined
// by a blank line. Without this, two same-offset insertions applied in
// sequence would nest rather than concatenate -- the second application
// would land its text ahead of the first's, reversing worktree order.
func mergeInsertTies(ops []editOp) []editOp {
	var (
		merged []editOp
		groups = map[uint][]editOp{}
		order  []uint
	)
	for _, op := range ops {
		if op.kind != editInsert {
			merged = append(merged, op)
			continue
		}
		if _, seen := groups[op.start]; !seen {
			order = append(order, op.start)
		}
		groups[op.start] = append(groups[op.start], op)
	}
	for _, start := range order {
		group := groups[start]
		sort.SliceStable(group, func(i, j int) bool { return group[i].seq < group[j].seq })
		text := group[0].text
		for _, g := range group[1:] {
			text = joinWithBlankLine(text, g.text)
		}
		merged = append(merged, editOp{kind: editInsert, start: start, text: text})
	}
	return merged
}

// spliceReplace substitutes out[start:end] with text. No boundary padding
// is needed: the bytes on either side of [start,end) -- including
// whatever follows the last symbol in the file -- are untouched, so a
// replacement of the file's final symbol still ends on exactly whatever
// byte HEAD itself ended on (AGENTS.md: EOF newline inherited, never
// normalized).
func spliceReplace(out []byte, start, end uint, text []byte) []byte {
	result := make([]byte, 0, uint(len(out))-(end-start)+uint(len(text)))
	result = append(result, out[:start]...)
	result = append(result, text...)
	result = append(result, out[end:]...)
	return result
}

// spliceExcise removes out[start:end] -- a deleted symbol's extent -- and
// collapses the blank-line gap it leaves. When the excised symbol was last
// in the file, collapsing naively would take HEAD's own trailing newline
// with the gap; the second branch restores it separately, so deletion
// never changes EOF newline-or-not.
func spliceExcise(out []byte, start, end uint) []byte {
	after := out[end:]
	trimmed := bytes.TrimLeft(after, "\n")
	if len(trimmed) != 0 {
		result := make([]byte, 0, start+uint(len(trimmed)))
		result = append(result, out[:start]...)
		result = append(result, trimmed...)
		return result
	}

	prefix := bytes.TrimRight(out[:start], "\n")
	if len(prefix) == 0 {
		return prefix
	}
	if bytes.HasSuffix(out, []byte("\n")) {
		prefix = append(prefix, '\n')
	}
	return prefix
}

// spliceInsert splices text in at start, with no HEAD extent to replace.
// It normalizes the blank-line boundary on both sides of the insertion
// (design.md: "boundary padding normalizes newlines between spliced
// regions only") but never manufactures a trailing newline where none
// existed: when start lands at true end-of-file (out[start:] is empty),
// the result's own trailing newline mirrors out's, not a forced default.
func spliceInsert(out []byte, start uint, text []byte) []byte {
	before := out[:start]
	after := out[start:]

	mid := joinWithBlankLine(before, text)
	// Both empty and newlines-only mean end-of-file: nothing follows the
	// insertion but the file's own terminator. Appending after the last
	// symbol lands in the newlines-only case, since HEAD's trailing
	// newline sits between that symbol and EOF.
	if len(bytes.TrimLeft(after, "\n")) == 0 {
		mid = bytes.TrimRight(mid, "\n")
		if bytes.HasSuffix(out, []byte("\n")) {
			mid = append(mid, '\n')
		}
		return mid
	}
	return joinWithBlankLine(mid, after)
}

// joinWithBlankLine concatenates a and b with exactly one blank line
// between them, trimming any newlines a already trails or b already
// leads so repeated splices cannot accumulate extra blank lines. An empty
// side contributes no separator -- joining onto nothing is not a
// boundary.
func joinWithBlankLine(a, b []byte) []byte {
	a = bytes.TrimRight(a, "\n")
	b = bytes.TrimLeft(b, "\n")
	switch {
	case len(a) == 0:
		return append([]byte(nil), b...)
	case len(b) == 0:
		return append([]byte(nil), a...)
	default:
		out := make([]byte, 0, len(a)+2+len(b))
		out = append(out, a...)
		out = append(out, '\n', '\n')
		out = append(out, b...)
		return out
	}
}
