package synth

import (
	"errors"
	"fmt"

	ts "github.com/tree-sitter/go-tree-sitter"

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

// declOrder returns every top-level declaration in src, in source order,
// as the exact qualified anchor string resolve.Resolve accepts for it.
//
// Seam note: internal/resolve.Resolve only resolves one named anchor at a
// time; it has no exported enumeration. Nearest-existing-sibling
// insertion needs the FULL ordered symbol table (spike/synth.py's
// w_order), so this duplicates resolve's own (unexported)
// assignQualifiedNames -- deliberately the small, mechanical half of
// resolution, not the fragile half. Extent computation, doc-comment
// attribution, and ambiguity handling all still go through
// resolve.Resolve itself; declOrder only supplies the name each
// worktree-order position resolves to. A follow-up to internal/resolve
// exporting this enumeration (or Symbol.Qualified) directly would let
// this duplication go away -- reported as a seam in the Phase 3 handoff.
func declOrder(lang resolve.Language, src []byte) ([]string, error) {
	root, err := parseRoot(lang, src)
	if err != nil {
		return nil, err
	}
	decls := lang.Declarations(src, root)
	return assignQualified(decls), nil
}

// assignQualified mirrors resolve/index.go's assignQualifiedNames exactly:
// container-qualified when the declaration has one, the bare name when
// it's the only uncontained declaration sharing that name, else an
// ordinal suffix (Go's repeated func init()).
func assignQualified(decls []resolve.Declaration) []string {
	uncontained := map[string]int{}
	for _, d := range decls {
		if d.Container == "" {
			uncontained[d.Bare]++
		}
	}
	seen := map[string]int{}
	out := make([]string, len(decls))
	for i, d := range decls {
		switch {
		case d.Container != "":
			out[i] = d.Container + "." + d.Bare
		case uncontained[d.Bare] == 1:
			out[i] = d.Bare
		default:
			seen[d.Bare]++
			out[i] = fmt.Sprintf("%s#%d", d.Bare, seen[d.Bare])
		}
	}
	return out
}

// parseRoot parses src with lang's grammar and returns its root node,
// mirroring internal/resolve's own parseSource -- duplicated for the same
// seam reason as declOrder: it is not exported.
func parseRoot(lang resolve.Language, src []byte) (*ts.Node, error) {
	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang.TSLanguage()); err != nil {
		return nil, fmt.Errorf("synth: set language %s: %w", lang.Name(), err)
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, fmt.Errorf("synth: %s: parse produced no tree", lang.Name())
	}
	return tree.RootNode(), nil
}
