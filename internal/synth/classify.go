package synth

import (
	"errors"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// classify resolves one anchor against a file's already-loaded HEAD and
// worktree content and reports what to do about it. It performs no I/O
// and mutates nothing -- resolution is a pure read, so a failure here
// leaves the caller free to abandon the whole batch with the index
// exactly as found (AGENTS.md).
func (fp *filePlan) classify(anchor string) (editOp, error) {
	var workRes, headRes *resolve.Resolution
	var workErr, headErr error
	if fp.workExists {
		workRes, workErr = resolve.Resolve(fp.lang, fp.workSrc, anchor)
	}
	if fp.headExists {
		headRes, headErr = resolve.Resolve(fp.lang, fp.headSrc, anchor)
	}

	// A non-ResolveError means tree-sitter or the language adapter
	// itself failed (e.g. SetLanguage), not "anchor not found" -- that
	// is a hard failure, not grounds to guess this is a new symbol.
	if workErr != nil && !isResolveError(workErr) {
		return editOp{}, workErr
	}
	if headErr != nil && !isResolveError(headErr) {
		return editOp{}, headErr
	}
	if amb, ok := asAmbiguous(workErr); ok {
		return editOp{}, amb
	}
	if amb, ok := asAmbiguous(headErr); ok {
		return editOp{}, amb
	}

	switch {
	case workRes != nil && headRes != nil:
		return editOp{
			kind:  editReplace,
			start: headRes.Extent.Start,
			end:   headRes.Extent.End,
			text:  append([]byte(nil), fp.workSrc[workRes.Extent.Start:workRes.Extent.End]...),
		}, nil

	case workRes != nil && headRes == nil:
		pos, seq := fp.insertionPoint(workRes.Anchor)
		return editOp{
			kind:  editInsert,
			start: pos,
			seq:   seq,
			text:  append([]byte(nil), fp.workSrc[workRes.Extent.Start:workRes.Extent.End]...),
		}, nil

	case workRes == nil && headRes != nil:
		return editOp{kind: editDelete, start: headRes.Extent.Start, end: headRes.Extent.End}, nil

	default:
		return editOp{}, &resolve.ResolveError{Code: exitcode.AnchorUnresolvable, Anchor: anchor}
	}
}

// insertionPoint implements design.md's nearest-existing-sibling rule:
// walk backwards through the worktree's declaration order to the first
// sibling also present in HEAD and insert after it; else walk forwards
// and insert before; else append at the end of the synthesized region.
// It must work when the immediate neighbours are themselves new (W =
// [A, X_new, Y_new, C], staging Y_new inserts after A) -- which is
// exactly why this walks fp.workOrder rather than only checking adjacent
// entries. seq is qualified's index in that order, used later to keep
// multiple same-offset insertions in worktree order (mergeInsertTies).
func (fp *filePlan) insertionPoint(qualified string) (pos uint, seq int) {
	idx := indexOf(fp.workOrder, qualified)
	if idx < 0 {
		// Cannot happen: qualified is Resolve's own normalized answer
		// for the anchor that was just resolved against fp.workSrc.
		idx = len(fp.workOrder)
	}
	if !fp.headExists {
		return 0, idx
	}
	for i := idx - 1; i >= 0; i-- {
		if res, err := resolve.Resolve(fp.lang, fp.headSrc, fp.workOrder[i]); err == nil {
			return res.Extent.End, idx
		}
	}
	for i := idx + 1; i < len(fp.workOrder); i++ {
		if res, err := resolve.Resolve(fp.lang, fp.headSrc, fp.workOrder[i]); err == nil {
			return res.Extent.Start, idx
		}
	}
	return uint(len(fp.headSrc)), idx
}

func indexOf(order []string, name string) int {
	for i, n := range order {
		if n == name {
			return i
		}
	}
	return -1
}

func isResolveError(err error) bool {
	var rerr *resolve.ResolveError
	return errors.As(err, &rerr)
}

func asAmbiguous(err error) (*resolve.ResolveError, bool) {
	var rerr *resolve.ResolveError
	if errors.As(err, &rerr) && rerr.Code == exitcode.AnchorAmbiguous {
		return rerr, true
	}
	return nil, false
}
