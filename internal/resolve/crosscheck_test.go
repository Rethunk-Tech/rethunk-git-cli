package resolve

import (
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
)

// TestCrossCheckVerdict_AgreesPerAnchorAndBatch guards the invariant this
// package's two public cross-check entry points must never independently
// drift on: commit's per-anchor CrossCheckExtent and diff's batch
// CrossCheckExtents must never disagree about what "degraded" or "a
// mismatch" means for an identical resolution against an identical symbol
// table. Neither
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
		{"a pseudo-anchor never reaches a verdict at all -- exempt, not degraded", pseudoImports, false, false},
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
	// degrades overall because missingName is not found -- even though
	// pseudoImports is exempt rather than degrading, and matchA and
	// mismatchB, sharing the same query, were each individually found.
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

// TestMatchAndCompare_FlatContainerIgnoresServerContainerName pins HTML's
// own reason for existing: a server-reported symbol's genuine containerName
// (here "div#app", a real ancestor) must never be joined onto its Name
// before comparing against a Flat resolution's Anchor, because HTML's own
// Container/Sep pair is a formatting trick (tag#id), not a real ancestor --
// joining a real one on top would produce a string the resolver's own flat
// anchor space can never contain. Anchor and the server's own Name already
// agree exactly; the join is what would have broken it.
func TestMatchAndCompare_FlatContainerIgnoresServerContainerName(t *testing.T) {
	t.Parallel()

	src := []byte(`<input id="name">` + "\n")
	symbols := []lsp.Symbol{
		{Name: "input#name", Container: "div#app", StartLine: 0, EndLine: 0},
	}
	res := &Resolution{Anchor: "input#name", Sep: "#", Flat: true, DeclOnly: Extent{Start: 0, End: uint(len(src) - 1)}}

	found, err := MatchAndCompare(src, res, symbols)
	if !found {
		t.Fatal("found = false; want true -- Flat must match on Name alone, ignoring the server's real containerName")
	}
	if err != nil {
		t.Errorf("err = %v; want nil, ranges agree", err)
	}

	// The same symbol list with Flat left false (a non-HTML default) must
	// NOT match, proving the test above exercises the Flat branch and not
	// some other path that happened to already ignore Container.
	notFlat := &Resolution{Anchor: "input#name", Sep: "#", DeclOnly: Extent{Start: 0, End: uint(len(src) - 1)}}
	found, err = MatchAndCompare(src, notFlat, symbols)
	if found {
		t.Error("found = true with Flat unset; want false -- qualifyLSPSymbol's join should have produced \"div#app#input#name\", not a match")
	}
	if err != nil {
		t.Errorf("err = %v; want nil (found=false never carries an error)", err)
	}
}

// TestMatchAndCompare_CorruptedOffsetFailsLoudly guards lineOf's own bounds
// check. Every DeclOnly offset this package hands MatchAndCompare today
// comes from a Declaration parsed out of the exact src passed alongside it
// (index.go's buildIndex), so this is unreachable in practice -- but a
// *Resolution naming an offset past len(src) must be reported, not panic
// slicing src[:offset], and not be silently swallowed the way a genuine
// "server never named this symbol" (found=false) answer already is.
func TestMatchAndCompare_CorruptedOffsetFailsLoudly(t *testing.T) {
	t.Parallel()

	src := []byte("func A() int { return 1 }\n")
	symbols := []lsp.Symbol{{Name: "A", StartLine: 0, EndLine: 0}}
	corrupted := &Resolution{Anchor: "A", DeclOnly: Extent{Start: 0, End: uint(len(src) + 100)}}

	found, err := MatchAndCompare(src, corrupted, symbols)
	if !found {
		t.Error("found = false; want true so the caller does not drop err on the floor")
	}
	if err == nil {
		t.Fatal("err = nil; want a reported internal-inconsistency error")
	}
	if _, ok := err.(*ResolveError); ok {
		t.Errorf("err = %T (%v); want a plain error, not exitcode.ExtentMismatch's own -- this is not a real tree-sitter/language-server disagreement", err, err)
	}
}
