package synth

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// classify resolves one anchor against a file's already-loaded HEAD and
// worktree content and reports what to do about it. It performs no
// mutating I/O -- resolution (including the LSP cross-check) is a pure
// read, so a failure here leaves the caller free to abandon the whole
// batch with the index exactly as found (AGENTS.md).
//
// unchanged reports whether the resolved extent is byte-identical between
// HEAD and the worktree -- docs/USAGE.md's "target has no uncommitted
// changes" warning, which the caller (not classify) turns into a message
// and folds into the exit-11 rule. tsOnly reports whether the cross-check
// degraded to tree-sitter-only for this anchor because no live language
// server answered in time -- normal, not an error (specs/design.md), but
// worth the caller announcing once on stderr.
func (fp *filePlan) classify(ctx context.Context, sess *lsp.Session, root, anchor string) (op editOp, unchanged, tsOnly bool, err error) {
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
		return editOp{}, false, false, workErr
	}
	if headErr != nil && !isResolveError(headErr) {
		return editOp{}, false, false, headErr
	}
	if amb, ok := asAmbiguous(workErr); ok {
		return editOp{}, false, false, amb
	}
	if amb, ok := asAmbiguous(headErr); ok {
		return editOp{}, false, false, amb
	}

	switch {
	case workRes != nil && headRes != nil:
		workBytes := fp.workSrc[workRes.Extent.Start:workRes.Extent.End]
		headBytes := fp.headSrc[headRes.Extent.Start:headRes.Extent.End]
		tsOnly, err = fp.crossCheck(ctx, sess, root, workRes)
		if err != nil {
			return editOp{}, false, false, err
		}
		op = editOp{
			kind:  editReplace,
			start: headRes.Extent.Start,
			end:   headRes.Extent.End,
			text:  append([]byte(nil), workBytes...),
		}
		return op, bytes.Equal(workBytes, headBytes), tsOnly, nil

	case workRes != nil && headRes == nil:
		pos, seq := fp.insertionPoint(workRes.Anchor)
		tsOnly, err = fp.crossCheck(ctx, sess, root, workRes)
		if err != nil {
			return editOp{}, false, false, err
		}
		op = editOp{
			kind:  editInsert,
			start: pos,
			seq:   seq,
			text:  append([]byte(nil), fp.workSrc[workRes.Extent.Start:workRes.Extent.End]...),
		}
		return op, false, tsOnly, nil

	case workRes == nil && headRes != nil:
		// Deletion: never cross-checked. The symbol exists only in HEAD,
		// outside a language server's worktree view -- both
		// docs/ANCHORS.md's cross-check exemptions and
		// resolve.CrossCheckExtent's own doc comment ("callers must not
		// invoke this for deletions") are explicit that this is the
		// caller's job to skip, not something the exempted function
		// itself is trusted to catch every time.
		return editOp{kind: editDelete, start: headRes.Extent.Start, end: headRes.Extent.End}, false, false, nil

	default:
		return editOp{}, false, false, &resolve.ResolveError{Code: exitcode.AnchorUnresolvable, Anchor: anchor}
	}
}

// crossCheck verifies res against a live language server, when reachable.
// Pseudo-anchors are skipped here too, proactively, even though
// resolve.CrossCheckExtent also treats res.Pseudo as a safety net --
// docs/ANCHORS.md documents the exemption as the caller's rule to know,
// not something to rely on a callee catching.
func (fp *filePlan) crossCheck(ctx context.Context, sess *lsp.Session, root string, res *resolve.Resolution) (tsOnly bool, err error) {
	if res.Pseudo {
		return false, nil
	}
	absPath := filepath.Join(root, fp.path)
	degraded, cerr := resolve.CrossCheckExtent(ctx, sess, fp.lang, root, absPath, fp.workSrc, res, false)
	if cerr != nil {
		return false, cerr
	}
	return degraded, nil
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
	idx := slices.Index(fp.workOrder, qualified)
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
