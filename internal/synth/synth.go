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
	"cmp"
	"slices"
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

	// wstart/wend are the anchor's own resolved extent in WORKTREE byte
	// space -- distinct from start/end, which are HEAD splice coordinates.
	// swallowedBy needs both: an insertion's start is a splice position
	// that can coincide with another op's HEAD boundary by pure adjacency
	// (a sibling replaced right next to one newly inserted), which is not
	// the same thing as that op's replacement text already containing the
	// insertion. Only wstart/wend answer that -- they are zero (unset,
	// meaningfully absent) for editDelete, which has no worktree extent to
	// carry.
	wstart uint
	wend   uint

	// member is true when an editInsert names a container member (a struct
	// field, interface method, or class/namespace method) rather than a
	// top-level declaration. Container members sit flush against their
	// siblings in idiomatic source -- no blank line between them -- so
	// spliceInsert must not pad one in, or the synthesized blob is
	// semantically right but never byte-identical to the worktree
	// (specs/design.md § Blob synthesis). Zero value is false, so every
	// existing
	// top-level insertion keeps its blank-line padding unchanged.
	member bool

	// leadGap and trailGap are the blank-line run that already sits before
	// and after this anchor's own extent in the WORKTREE (classify.go's
	// leadingGap/trailingGap) -- the real separator a byte-identical splice
	// must reproduce when the file's own convention is wider than the
	// member/non-member minimum (Python's PEP 8 double blank line between
	// top-level defs, which a single hardcoded blank line would collapse).
	// widerGap takes whichever is longer, so these only ever grow a
	// separator past the minimum, never shrink it below. Left at the Go
	// zero value (nil, so shorter than any minimum) by addPreamble's own
	// ops, which already own their trailing separator via
	// resolve.ExtendThroughOwnedSeparator -- computing one here too would
	// double-count bytes that extent already claimed.
	leadGap  []byte
	trailGap []byte
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
	slices.SortStableFunc(sorted, func(a, b editOp) int { return cmp.Compare(b.start, a.start) })

	out := append([]byte(nil), head...)
	for _, op := range sorted {
		switch op.kind {
		case editReplace:
			out = spliceReplace(out, op.start, op.end, op.text)
		case editDelete:
			out = spliceExcise(out, op.start, op.end)
		case editInsert:
			out = spliceInsert(out, op)
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
	slices.SortStableFunc(ranked, func(a, b editOp) int { return cmp.Compare(b.span(), a.span()) })

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
// ranking.
//
// A non-insertion op (editReplace or editDelete) is a genuine HEAD range, so
// two of those overlap exactly when the ordinary half-open interval test
// says so.
//
// An insertion is different: op.start is a splice POSITION into HEAD's
// bytes (AGENTS.md's descending-offset splice pass), computed as the
// nearest-existing-sibling's own HEAD boundary (specs/design.md). That
// position can coincide exactly with another kept op's HEAD start or end by
// pure adjacency -- a sibling being replaced right next to a brand new
// symbol inserted immediately after it -- without that kept op's
// replacement text containing the insertion at all: a replaced sibling's
// text is only its own new body, never what comes after it. Only a genuine
// nesting says the kept op already carries this insertion's text: the
// insertion's own WORKTREE extent (wstart/wend) falling inside the kept
// op's own worktree extent, which is what its replacement text is actually
// drawn from. An editDelete has no worktree extent (wend stays 0), so the
// zero-value check alone is enough to say it never contains an insertion.
//
// Two insertions can also name the identical worktree extent rather than
// merely sit at the same splice position: a member of a container absent
// from HEAD widens to the whole container (escalateToContainer, classify.go),
// which resolves to the exact same Resolution a direct anchor on the
// container itself would -- same wstart, same wend, same text. That is one
// declaration named twice, not two declarations that happen to land
// together, so it must collapse to a single kept op rather than reach
// mergeInsertTies, which concatenates same-position insertions on the
// assumption that they are distinct.
func swallowedBy(kept []editOp, op editOp) bool {
	for _, k := range kept {
		if k.kind == editInsert {
			if op.kind == editInsert && op.wend > op.wstart &&
				op.wstart == k.wstart && op.wend == k.wend {
				return true
			}
			continue
		}
		if op.kind == editInsert {
			if k.kind == editReplace && op.wend > op.wstart &&
				op.wstart >= k.wstart && op.wend <= k.wend {
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
		slices.SortStableFunc(group, func(a, b editOp) int { return cmp.Compare(a.seq, b.seq) })
		// Every op in one group was resolved against the same insertion
		// point in the same file, so they share one container context (or
		// lack of one) -- the first op's flag speaks for the whole group.
		member := group[0].member
		text := group[0].text
		for _, g := range group[1:] {
			// The separator between two merged items is that LATER item's
			// own leading gap: the true worktree distance between the
			// previous item and this one, not a distance involving
			// whatever precedes the whole group.
			text = joinWithSeparator(text, g.text, widerGap(member, g.leadGap))
		}
		merged = append(merged, editOp{
			kind:  editInsert,
			start: start,
			text:  text,
			// The merged op's own boundary gaps are the first item's real
			// leading gap and the last item's real trailing gap -- the
			// group's interior gaps were already consumed building text.
			leadGap:  group[0].leadGap,
			trailGap: group[len(group)-1].trailGap,
			member:   member,
		})
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

// spliceInsert splices op.text in at op.start, with no HEAD extent to
// replace. It normalizes the boundary on both sides of the insertion
// (design.md: "boundary padding normalizes newlines between spliced regions
// only") but never manufactures a trailing newline where none existed: when
// start lands at true end-of-file (out[start:] is empty), the result's own
// trailing newline mirrors out's, not a forced default.
func spliceInsert(out []byte, op editOp) []byte {
	before := out[:op.start]
	after := out[op.start:]

	mid := joinWithSeparator(before, op.text, widerGap(op.member, op.leadGap))
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
	return joinWithSeparator(mid, after, widerGap(op.member, op.trailGap))
}

// widerGap picks the separator a splice should actually use: gap -- the
// real blank-line run this anchor already has on that side in the worktree
// -- when it is wider than the structural minimum (member selects which:
// a blank line between two top-level declarations, exactly one newline
// between two members of the same struct, interface, or class), else the
// minimum itself. Only ever widens, never narrows: gap stays nil (shorter
// than any minimum) for an op that never computed one (addPreamble's, which
// already owns its trailing separator via
// resolve.ExtendThroughOwnedSeparator), so those fall back to the minimum
// when gap is nil.
func widerGap(member bool, gap []byte) []byte {
	min := "\n\n"
	if member {
		min = "\n"
	}
	if len(gap) > len(min) {
		return gap
	}
	return []byte(min)
}

// joinWithSeparator concatenates a and b, trimming any newlines a already
// trails or b already leads so repeated splices cannot accumulate extra
// blank lines, then rejoining with sep. An empty side contributes no
// separator -- joining onto nothing is not a boundary.
func joinWithSeparator(a, b, sep []byte) []byte {
	a = bytes.TrimRight(a, "\n")
	b = bytes.TrimLeft(b, "\n")
	switch {
	case len(a) == 0:
		return append([]byte(nil), b...)
	case len(b) == 0:
		return append([]byte(nil), a...)
	default:
		out := make([]byte, 0, len(a)+len(sep)+len(b))
		out = append(out, a...)
		out = append(out, sep...)
		out = append(out, b...)
		return out
	}
}
