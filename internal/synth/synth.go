// Package synth constructs the blob that would exist if only the named
// symbols had changed, and stages it -- the two things AGENTS.md assigns
// this package: "blob synthesis" and, via Stage, "staging". Everything
// else (hooks, pathspec matching, credential prompting) stays git's job,
// delegated through internal/gitx.
//
// The algorithm is specs/design.md's "Blob synthesis" section, ported
// from the validated prototype in spike/synth.py: read HEAD and worktree
// content, resolve each anchor's extent in both, and splice, insert, or
// excise accordingly. Multiple edits in one file apply in reverse
// byte-offset order so an earlier splice cannot invalidate a later
// offset (AGENTS.md's invariant table).
package synth

import "sort"

// editKind is what one resolved target does to a file's HEAD content.
type editKind int

const (
	// editReplace substitutes [start,end) in HEAD with text -- the
	// symbol exists in both HEAD and the worktree.
	editReplace editKind = iota
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
// applied, in descending start order so an earlier splice cannot
// invalidate a later offset (AGENTS.md's invariant table).
func applyEdits(head []byte, ops []editOp) []byte {
	sorted := append([]editOp(nil), ops...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].start > sorted[j].start })

	out := append([]byte(nil), head...)
	for _, op := range sorted {
		switch op.kind {
		case editReplace:
			out = spliceReplace(out, op.start, op.end, op.text)
		}
	}
	return out
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
