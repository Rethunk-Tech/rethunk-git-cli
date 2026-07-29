package resolve

import (
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
)

// TestCrossCheckVerdict_AgreesPerAnchorAndBatch is LOW9's parity guard:
// commit's per-anchor CrossCheckExtent and diff's batch CrossCheckExtents
// must never independently drift on what "degraded" or "a mismatch" means
// for an identical resolution against an identical symbol table. Neither
// function is driven directly here -- both dial a real *lsp.Session before
// ever reaching a verdict, and Session's client cache is unexported to
// package lsp, so there is no seam to hand either one a mock client
// without a live server. What both actually share for the verdict itself
// is crossCheckVerdict (crosscheck.go), extracted for exactly this reason:
// this test drives it directly with one shared mock symbol list, first
// per-anchor (a single-resolution list, precisely what CrossCheckExtent
// constructs internally) and then as part of the full batch (precisely
// CrossCheckExtents' own list, unmodified), and asserts the two agree.
func TestCrossCheckVerdict_AgreesPerAnchorAndBatch(t *testing.T) {
	t.Parallel()

	// Two lines, so DeclOnly extents below can point at real byte offsets
	// lineOf (crosscheck.go) converts back to the line numbers symbols
	// reports against.
	src := []byte("func A() int { return 1 }\nfunc B() int { return 2 }\n")
	const lineA = "func A() int { return 1 }\n"

	symbols := []lsp.Symbol{
		{Name: "A", StartLine: 0, EndLine: 0},
		// B's reported range disagrees with tree-sitter's own -- a genuine
		// mismatch, not an absence.
		{Name: "B", StartLine: 1, EndLine: 5},
	}

	matchA := &Resolution{Anchor: "A", DeclOnly: Extent{Start: 0, End: uint(len(lineA) - 1)}}
	mismatchB := &Resolution{Anchor: "B", DeclOnly: Extent{Start: uint(len(lineA)), End: uint(len(src) - 1)}}
	pseudoImports := &Resolution{Anchor: "@imports", Pseudo: true}
	missingName := &Resolution{Anchor: "NoSuchName", DeclOnly: Extent{Start: 0, End: 1}}

	list := []*Resolution{matchA, mismatchB, pseudoImports, missingName}

	tests := []struct {
		name         string
		res          *Resolution
		wantDegraded bool
		wantMismatch bool
	}{
		{"exact match is not degraded and has no mismatch", matchA, false, false},
		{"a genuine range disagreement is a mismatch, not degraded", mismatchB, false, true},
		{"a pseudo-anchor never reaches a verdict at all -- degraded", pseudoImports, true, false},
		{"absent from the server's own outline degrades, not a mismatch", missingName, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			degraded, mismatches := crossCheckVerdict(src, []*Resolution{tt.res}, symbols)
			if degraded != tt.wantDegraded {
				t.Errorf("degraded = %v; want %v", degraded, tt.wantDegraded)
			}
			if gotMismatch := len(mismatches) > 0; gotMismatch != tt.wantMismatch {
				t.Errorf("mismatch present = %v; want %v", gotMismatch, tt.wantMismatch)
			}
		})
	}

	// The batch form over the whole list at once must agree with every
	// per-anchor verdict above: exactly mismatchB's own error surfaces (an
	// identical mismatch, not merely one of some count), and the batch
	// degrades overall because pseudoImports and missingName are each
	// individually not-found -- even though matchA and mismatchB, sharing
	// the same query, were each individually found.
	_, soloMismatches := crossCheckVerdict(src, []*Resolution{mismatchB}, symbols)
	batchDegraded, batchMismatches := crossCheckVerdict(src, list, symbols)

	if !batchDegraded {
		t.Error("batch degraded = false; want true (missingName is never found)")
	}
	if len(batchMismatches) != 1 {
		t.Fatalf("batch mismatches = %d; want exactly 1 (mismatchB)", len(batchMismatches))
	}
	if batchMismatches[0].Error() != soloMismatches[0].Error() {
		t.Errorf("batch mismatch = %q; want the identical per-anchor verdict %q",
			batchMismatches[0].Error(), soloMismatches[0].Error())
	}
}
