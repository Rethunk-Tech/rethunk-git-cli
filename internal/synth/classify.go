package synth

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// classify resolves one anchor against a file's already-loaded HEAD and
// worktree content and reports what to do about it. It performs no
// mutating I/O and no LSP round trip of its own -- a symbol that needs
// verifying against a live language server is only queued onto
// fp.pendingCrossCheck here; crossCheckPending below is what actually
// dials, once per file rather than once per anchor (m20's own fix: a
// commit naming several symbols in one file used to pay one documentSymbol
// round trip per anchor, the same query repeated, where internal/diff's
// own crossCheckFile already batches identically-shaped work into one).
// Deferring the dial rather than dropping it keeps this a pure read, same
// as before: a failure surfaces once crossCheckPending runs, still before
// planStage returns a plan Apply could act on, so AGENTS.md's "resolve
// every target before staging any" still holds.
//
// unchanged reports whether the resolved extent is byte-identical between
// HEAD and the worktree -- docs/USAGE.md's "target has no uncommitted
// changes" warning, which the caller (not classify) turns into a message
// and folds into the exit-11 rule.
func (fp *filePlan) classify(anchor string) (op editOp, unchanged bool, err error) {
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
	if _, ok := resolve.AsResolveError(workErr); workErr != nil && !ok {
		return editOp{}, false, workErr
	}
	if _, ok := resolve.AsResolveError(headErr); headErr != nil && !ok {
		return editOp{}, false, headErr
	}
	if amb, ok := resolve.AsAmbiguous(workErr); ok {
		return editOp{}, false, amb
	}
	if amb, ok := resolve.AsAmbiguous(headErr); ok {
		return editOp{}, false, amb
	}

	switch {
	case workRes != nil && headRes != nil:
		workBytes := fp.workSrc[workRes.Extent.Start:workRes.Extent.End]
		headBytes := fp.headSrc[headRes.Extent.Start:headRes.Extent.End]
		fp.deferCrossCheck(workRes)
		op = editOp{
			kind:   editReplace,
			start:  headRes.Extent.Start,
			end:    headRes.Extent.End,
			text:   append([]byte(nil), workBytes...),
			wstart: workRes.Extent.Start,
			wend:   workRes.Extent.End,
		}
		return op, bytes.Equal(workBytes, headBytes), nil

	case workRes != nil && headRes == nil:
		workRes, isNestedMember, escErr := fp.escalateToContainer(workRes)
		if escErr != nil {
			return editOp{}, false, escErr
		}
		pos, seq := fp.insertionPoint(workRes)
		fp.deferCrossCheck(workRes)
		op = editOp{
			kind:   editInsert,
			start:  pos,
			seq:    seq,
			text:   insertionText(fp.workSrc, workRes.Extent),
			wstart: workRes.Extent.Start,
			wend:   workRes.Extent.End,
			// A structurally nested member only sits flush against its
			// siblings when the language's own convention keeps it that
			// way (Language.MembersSitFlush) -- Python requires a blank line
			// between class methods even though they are just as nested as
			// a Go struct field or a TypeScript class method.
			member: isNestedMember && fp.lang.MembersSitFlush(),
		}
		return op, false, nil

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
		}, false, nil

	default:
		// Both sides came back nil, which can only be an AnchorUnresolvable
		// ResolveError -- a hard error or an ambiguity would already have
		// returned above. workErr and headErr carry the "did you mean"
		// Candidates idx.suggest computed; a fresh ResolveError here would
		// silently drop them, leaving the caller with a bare exit 3.
		if rerr, ok := resolve.AsResolveError(workErr); ok {
			return editOp{}, false, rerr
		}
		if rerr, ok := resolve.AsResolveError(headErr); ok {
			return editOp{}, false, rerr
		}
		return editOp{}, false, &resolve.ResolveError{Code: exitcode.AnchorUnresolvable, Anchor: anchor}
	}
}

// deferCrossCheck queues res to be verified against a live language server
// the next time crossCheckPending runs for this file, instead of dialing
// immediately -- the batching m20's fix rests on. Pseudo-anchors are
// skipped here too, proactively, even though resolve.CrossCheckExtents
// also treats a Pseudo entry as exempt internally -- docs/ANCHORS.md
// documents the exemption as the caller's rule to know, not something to
// rely on a callee catching, and there is no reason to grow the batch with
// an entry that can only ever be a no-op.
func (fp *filePlan) deferCrossCheck(res *resolve.Resolution) {
	if res.Pseudo {
		return
	}
	fp.pendingCrossCheck = append(fp.pendingCrossCheck, res)
}

// crossCheckPending verifies every resolution deferCrossCheck queued for
// this file against a live language server, in one query -- the same
// per-file batching internal/diff's own crossCheckFile already does via
// resolve.CrossCheckExtents, rather than the one-documentSymbol-round-trip-
// per-anchor query classify used to issue directly (m20). Only the
// worktree side is ever queued (deferCrossCheck's callers, classify's own
// two non-deletion branches), matching CrossCheckExtents' own worktree-only
// contract.
//
// err is the first genuine range disagreement (exit 6), if any. Called once
// per file after every target naming that file has already been resolved
// and its op built (planStage's own final pass over plan.files), so a
// mismatch here still aborts planStage before it ever returns a plan Apply
// could act on -- AGENTS.md's "resolve every target before staging any"
// holds exactly as it did when this dialed inline.
func (fp *filePlan) crossCheckPending(ctx context.Context, sess *lsp.Session, root string) (tsOnly bool, err error) {
	if len(fp.pendingCrossCheck) == 0 {
		return false, nil
	}
	absPath := filepath.Join(root, fp.path)
	degraded, mismatches := resolve.CrossCheckExtents(ctx, sess, fp.lang, root, absPath, fp.workSrc, fp.pendingCrossCheck)
	if len(mismatches) > 0 {
		return degraded, mismatches[0]
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
// unchanged.
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
// mere presence of a container name: resolve.Resolution.Container is set for
// a Go receiver method too, and a receiver is a sibling of its methods, not
// their parent -- staging `(*A).Get` must never drag in the `type A struct`
// declaration, and does not, because A's extent does not enclose Get's.
//
// isMember reports whether member names a symbol structurally nested inside
// its container -- a struct field, interface method, or class/namespace
// method, as opposed to a Go receiver method merely named with a container
// prefix while sitting beside it. Only a true nested member being spliced
// next to siblings that are already there wants spliceInsert's flush
// treatment (no blank line, specs/design.md § Blob synthesis); a sibling method
// and a whole freshly-escalated container are both ordinary top-level
// insertions and keep their blank-line padding.
//
// err is a hard failure resolving member.Container against the worktree --
// anything other than "no such container" (exitcode.AnchorUnresolvable). In
// particular an exitcode.AnchorAmbiguous container name (two duplicate TOML
// "[[servers]]" headers is the measured case) must reach the caller as the
// same exit-4 ambiguity a direct anchor resolve would give, not be read as
// "no container, so insert the bare member" -- Resolve's own three-outcome
// contract (found / not-found / hard-failure) does not collapse just because
// this call happens to be probing for absence.
func (fp *filePlan) escalateToContainer(member *resolve.Resolution) (res *resolve.Resolution, isMember bool, err error) {
	if member.Container == "" {
		return member, false, nil
	}
	// HTML's own Declaration.Container (lang_html.go) is not an enclosing
	// ancestor's name -- it is the element's OWN tag, carried only so
	// containerQualified can build the "tag#id" anchor text this adapter
	// emits. Resolving it as if it named a real parent matches whatever
	// element elsewhere happens to share that tag as its id (a coincidence,
	// not containment), and when that accidental match is itself new, the
	// escalated branch below would splice in its entire unrelated extent in
	// place of the member actually named. There is no per-Declaration
	// ancestor chain recorded to escalate to correctly instead (specs/
	// design.md § Grammar scope keeps HTML's addressing at element+id, one
	// level, on purpose), so the only sound fix is to never widen a flat-
	// container adapter's member to a "container" at all -- new nested
	// elements insert directly at their sibling position, uninvolved with
	// this mechanism. resolve.FlatContainerLanguage's own doc comment is
	// the seam; matched by type assertion, the same way markdown's
	// structural difference is (toplevelExtent, pseudo.go), rather than a
	// Language.Name() string compare a future rename would silently break.
	if flat, ok := fp.lang.(resolve.FlatContainerLanguage); ok && flat.FlatContainer() {
		return member, false, nil
	}
	container := member.Container

	outer, werr := fp.workFile.Resolve(container)
	if werr != nil {
		if rerr, ok := resolve.AsResolveError(werr); ok && rerr.Code == exitcode.AnchorUnresolvable {
			return member, false, nil
		}
		return nil, false, werr
	}
	if outer.Extent.Start > member.Extent.Start || member.Extent.End > outer.Extent.End {
		return member, false, nil // a sibling, not a parent -- Go's receiver container
	}

	// Already in HEAD: the ordinary insertion path finds a sibling member
	// inside the container that is already there, and the new member sits
	// flush against it -- exactly as it already does in the worktree.
	if fp.headExists {
		if _, err := fp.headFile.Resolve(container); err == nil {
			return member, true, nil
		}
	}

	// The container itself is also new: there is no way to add a member to
	// one HEAD does not have, so the whole container is staged instead --
	// a top-level insertion, not a member.
	fp.escalated = append(fp.escalated, member.Anchor+" -> "+container)
	return outer, false, nil
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
// mergeInsertTies orders same-offset insertions by. A byte offset rather
// than a declaration index because a pseudo-anchor is not a declaration
// and has no index at all: ranking it by a missing index puts @header
// after every symbol, writing the package clause at the bottom of a new
// file. Declaration order and byte order agree, so ordinary declarations
// rank identically either way.
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
