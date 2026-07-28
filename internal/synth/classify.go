package synth

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

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
		workRes, workErr = fp.workFile.Resolve(anchor)
	}
	if fp.headExists {
		headRes, headErr = fp.headFile.Resolve(anchor)
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
		workRes = fp.escalateToContainer(workRes)
		pos, seq := fp.insertionPoint(workRes)
		tsOnly, err = fp.crossCheck(ctx, sess, root, workRes)
		if err != nil {
			return editOp{}, false, false, err
		}
		op = editOp{
			kind:  editInsert,
			start: pos,
			seq:   seq,
			text:  insertionText(fp.workSrc, workRes.Extent),
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
		// Excise the extent's own indentation with it. spliceExcise collapses
		// the gap the removal leaves by joining what precedes to what
		// follows, which assumes the cut begins at a line boundary -- true
		// for a top-level declaration in column zero, and false for a class
		// member, where the leading whitespace would be left behind to run
		// into the next member's own indentation.
		return editOp{
			kind:  editDelete,
			start: lineStart(fp.headSrc, headRes.Extent.Start),
			end:   headRes.Extent.End,
		}, false, false, nil

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

// insertionText is the extent's own bytes prefixed with the indentation of
// the line it starts on. An extent begins at the declaration's first token,
// not at the start of its line, so splicing one in verbatim puts it at
// column zero -- which for a top-level declaration is where it belongs and
// costs nothing, but for a class member means a method landing hard against
// the left margin of a body indented one level in.
func insertionText(src []byte, ext resolve.Extent) []byte {
	indent := src[lineStart(src, ext.Start):ext.Start]

	out := make([]byte, 0, len(indent)+int(ext.End-ext.Start))
	out = append(out, indent...)
	return append(out, src[ext.Start:ext.End]...)
}

// lineStart is the offset of the start of the line holding off, when only
// whitespace separates the two, and off itself otherwise. It is what makes
// an extent's own indentation part of the extent for the two operations
// that need it: an insertion carries it, and a deletion takes it away.
//
// A top-level declaration begins in column zero, so this returns off
// unchanged and neither operation behaves differently than before.
func lineStart(src []byte, off uint) uint {
	start := uint(bytes.LastIndexByte(src[:off], '\n') + 1)
	if len(bytes.TrimLeft(src[start:off], " \t")) != 0 {
		return off // something other than whitespace precedes it on the line
	}
	return start
}

// escalateToContainer widens a new member anchor to the container that
// encloses it when HEAD has neither. A method spliced in on its own lands at
// file scope -- `hello(): number { return 1 }` sitting beside the imports --
// which is not the file the caller has in their worktree and does not parse
// as the language it claims to be. There is no way to add a member to a
// class HEAD does not have without bringing the class.
//
// Nesting is tested structurally, by extent containment, rather than by the
// mere presence of a container name. Go's receiver container is a sibling of
// its methods, not their parent: staging `(*A).Get` must never drag in the
// `type A struct` declaration, and does not, because A's extent does not
// enclose Get's.
func (fp *filePlan) escalateToContainer(member *resolve.Resolution) *resolve.Resolution {
	dot := strings.LastIndexByte(member.Anchor, '.')
	if dot <= 0 {
		return member
	}
	container := member.Anchor[:dot]

	// Already in HEAD: the ordinary insertion path can find a sibling
	// member to splice against, inside the container that is already there.
	if fp.headExists {
		if _, err := fp.headFile.Resolve(container); err == nil {
			return member
		}
	}
	outer, err := fp.workFile.Resolve(container)
	if err != nil {
		return member
	}
	if outer.Extent.Start > member.Extent.Start || member.Extent.End > outer.Extent.End {
		return member // a sibling, not a parent -- Go's receiver container
	}
	fp.escalated = append(fp.escalated, member.Anchor+" -> "+container)
	return outer
}

// insertionPoint implements design.md's nearest-existing-sibling rule:
// walk backwards through the worktree's declaration order to the first
// sibling also present in HEAD and insert after it; else walk forwards
// and insert before; else append at the end of the synthesized region.
// It must work when the immediate neighbours are themselves new (W =
// [A, X_new, Y_new, C], staging Y_new inserts after A) -- which is
// exactly why this walks fp.workOrder rather than only checking adjacent
// entries.
//
// seq is the anchor's own start offset in the worktree, which is what
// mergeInsertTies orders same-offset insertions by. Declaration order and
// byte order agree, so this matches the old index-based seq for every
// declaration -- but a pseudo-anchor is not a declaration and so has no
// index at all. Ranking it by a missing index put @header after every
// symbol, writing the package clause at the bottom of a new file.
func (fp *filePlan) insertionPoint(res *resolve.Resolution) (pos uint, seq int) {
	seq = int(res.Extent.Start)

	idx := slices.Index(fp.workOrder, res.Anchor)
	if idx < 0 {
		// A pseudo-anchor: real position, but no entry in the declaration
		// table, so there is no sibling walk to do from here.
		idx = len(fp.workOrder)
	}
	if !fp.headExists {
		return 0, seq
	}
	for i := idx - 1; i >= 0; i-- {
		if sib, err := fp.headFile.Resolve(fp.workOrder[i]); err == nil {
			return sib.Extent.End, seq
		}
	}
	for i := idx + 1; i < len(fp.workOrder); i++ {
		if sib, err := fp.headFile.Resolve(fp.workOrder[i]); err == nil {
			return sib.Extent.Start, seq
		}
	}
	return uint(len(fp.headSrc)), seq
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
