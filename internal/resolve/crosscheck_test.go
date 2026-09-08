package resolve

import (
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
)

// TestCrossCheckVerdict_SingleAgreesWithBatch guards the invariant a batch
// verdict must hold: one resolution's outcome must not change because
// other resolutions shared its query. CrossCheckExtents is not driven
// directly here -- it dials a real *lsp.Session before reaching a verdict,
// and Session's client cache is unexported to package lsp, so there is no
// seam to hand it a mock client without a live server. crossCheckVerdict
// (crosscheck.go) is where the verdict is actually decided: this test
// drives it with one shared mock symbol list, first per-anchor and then
// over the full batch, and asserts the two agree.
func TestCrossCheckVerdict_SingleAgreesWithBatch(t *testing.T) {
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

func TestMatchAndCompare_FlatHTMLClassSuffix(t *testing.T) {
	t.Parallel()

	src := []byte(`<div id="app" class="widget">` + "\n")
	symbols := []lsp.Symbol{
		{Name: "div#app.widget", StartLine: 0, EndLine: 0},
	}
	res := &Resolution{Anchor: "div#app", Sep: "#", Flat: true, DeclOnly: Extent{Start: 0, End: uint(len(src) - 1)}}

	found, err := MatchAndCompare(src, res, symbols)
	if !found {
		t.Fatal("found = false; want true -- Flat HTML must ignore server class suffixes")
	}
	if err != nil {
		t.Errorf("err = %v; want nil, ranges agree", err)
	}
}

func TestMatchAndCompare_FlatHTMLOrdinalClassSuffix(t *testing.T) {
	t.Parallel()

	src := []byte(`<div id="app" class="widget">` + "\n" + `<div id="app" class="other">` + "\n")
	symbols := []lsp.Symbol{
		{Name: "div#app.widget", StartLine: 0, EndLine: 0},
		{Name: "div#app.other", StartLine: 1, EndLine: 1},
	}
	second := []byte(`<div id="app" class="widget">` + "\n")
	res := &Resolution{Anchor: "div#app#2", Sep: "#", Flat: true, SameName: 2, DeclOnly: Extent{Start: uint(len(second)), End: uint(len(src) - 1)}}

	found, err := MatchAndCompare(src, res, symbols)
	if !found {
		t.Fatal("found = false; want true -- Flat HTML ordinals must select the second class-suffixed symbol")
	}
	if err != nil {
		t.Errorf("err = %v; want nil, ranges agree", err)
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

// TestMatchAndCompare_OrdinalRequiresAgreeingCounts pins the gate on the
// ordinal fallback. An ordinal names the Nth declaration the resolver found,
// so indexing the server's own list by it is only the same declaration when
// both sides found the same number. A server that splits, adds, or repeats a
// symbol shifts every later ordinal, and pairing across that shift reports a
// disagreement between two unrelated declarations. Not naming the anchor at
// all is the honest answer, and degrades to [ts-only].
func TestMatchAndCompare_OrdinalRequiresAgreeingCounts(t *testing.T) {
	t.Parallel()

	src := []byte(`<div id="app" class="widget">` + "\n" + `<div id="app" class="other">` + "\n")
	second := []byte(`<div id="app" class="widget">` + "\n")
	decl := Extent{Start: uint(len(second)), End: uint(len(src) - 1)}

	// The server reports three same-named symbols where the resolver found
	// two, so its second is not the resolver's second.
	symbols := []lsp.Symbol{
		{Name: "div#app.widget", StartLine: 0, EndLine: 0},
		{Name: "div#app.injected", StartLine: 0, EndLine: 0},
		{Name: "div#app.other", StartLine: 1, EndLine: 1},
	}

	res := &Resolution{Anchor: "div#app#2", Sep: "#", Flat: true, SameName: 2, DeclOnly: decl}
	found, err := MatchAndCompare(src, res, symbols)
	if found {
		t.Errorf("found = true (err %v); want false -- a shifted ordinal must not be compared", err)
	}

	// With the counts agreeing, the same anchor still resolves and agrees.
	agreeing := []lsp.Symbol{
		{Name: "div#app.widget", StartLine: 0, EndLine: 0},
		{Name: "div#app.other", StartLine: 1, EndLine: 1},
	}
	found, err = MatchAndCompare(src, res, agreeing)
	if !found || err != nil {
		t.Errorf("found = %v, err = %v; want found with no disagreement", found, err)
	}
}

// TestMatchAndCompare_SlugAnchorsMatchRawHeadingText pins the cross-check for
// a language whose anchors are slugs of human-readable text. A server that
// reports the heading verbatim names the same declaration under a different
// spelling, so comparing the two literally never matches and every symbol in
// the file degrades to [ts-only] while both sides agree on the extent.
func TestMatchAndCompare_SlugAnchorsMatchRawHeadingText(t *testing.T) {
	t.Parallel()

	src := []byte("# Quick start\n\nbody\n")
	res := &Resolution{
		Anchor:      "quick-start",
		SlugAnchors: true,
		SameName:    1,
		DeclOnly:    Extent{Start: 0, End: uint(len(src) - 1)},
	}
	symbols := []lsp.Symbol{{Name: "Quick start", StartLine: 0, EndLine: 2}}

	found, err := MatchAndCompare(src, res, symbols)
	if !found {
		t.Fatal("found = false; want true -- a slug anchor must match the heading it was derived from")
	}
	if err != nil {
		t.Errorf("err = %v; want nil, both sides cover the same lines", err)
	}

	// Without the slug rule the same pair must not match, which is the
	// degradation this exists to remove.
	literal := &Resolution{Anchor: "quick-start", SameName: 1, DeclOnly: res.DeclOnly}
	if found, _ := MatchAndCompare(src, literal, symbols); found {
		t.Error("found = true for a literal comparison; want false")
	}
}

// TestMatchAndCompare_ExtentEndIsExclusive pins the half-open extent contract
// on rgit's own side of the comparison. An Extent is [Start, End), so the byte
// at End belongs to whatever follows; for a construct that ends exactly on a
// line boundary -- a YAML block mapping, a Markdown section -- that byte is the
// first byte of the next sibling, one line down. Converting End directly
// reports an extent one line longer than the one rgit stages and blames, and
// blames the language server for the difference.
func TestMatchAndCompare_ExtentEndIsExclusive(t *testing.T) {
	t.Parallel()

	// Two lines; the first "declaration" covers line 1 only, its extent
	// ending at the first byte of line 2.
	src := []byte("first\nsecond\n")
	firstLineEnd := uint(len("first\n"))

	res := &Resolution{Anchor: "first", SameName: 1, DeclOnly: Extent{Start: 0, End: firstLineEnd}}
	symbols := []lsp.Symbol{{Name: "first", StartLine: 0, EndLine: 0}}

	found, err := MatchAndCompare(src, res, symbols)
	if !found {
		t.Fatal("found = false; want true")
	}
	if err != nil {
		t.Errorf("err = %v; want nil -- an extent ending at a line boundary covers the line above it", err)
	}
}

// TestMatchAndCompare_GroupedAnchorMatchesAnyMember pins the selector-group
// pairing. One CSS rule is anchored by its whole selector list while the
// server reports one symbol per selector, each carrying that rule's identical
// range, so pairing on any member describes the same declaration. Members are
// compared whole: a prefix must never match.
func TestMatchAndCompare_GroupedAnchorMatchesAnyMember(t *testing.T) {
	t.Parallel()

	src := []byte("html, body, h1 {\n  margin: 0;\n}\n")
	res := &Resolution{
		Anchor:         "html, body, h1",
		GroupedAnchors: true,
		SameName:       1,
		DeclOnly:       Extent{Start: 0, End: uint(len(src))},
	}

	// The server names only one member of the group.
	found, err := MatchAndCompare(src, res, []lsp.Symbol{{Name: "body", StartLine: 0, EndLine: 2}})
	if !found {
		t.Fatal("found = false; want true -- any member of the group names the rule")
	}
	if err != nil {
		t.Errorf("err = %v; want nil", err)
	}

	// A selector that merely shares a prefix with a member is not a member.
	if found, _ := MatchAndCompare(src, res, []lsp.Symbol{{Name: "bod", StartLine: 0, EndLine: 2}}); found {
		t.Error("found = true for a prefix of a member; want false")
	}

	// Without the grouped rule the same pair must not match.
	plain := &Resolution{Anchor: "html, body, h1", SameName: 1, DeclOnly: res.DeclOnly}
	if found, _ := MatchAndCompare(src, plain, []lsp.Symbol{{Name: "body", StartLine: 0, EndLine: 2}}); found {
		t.Error("found = true without GroupedAnchors; want false")
	}
}

// TestMatchAndCompare_SlugAnchorDropsTrailingBlankLines pins the other half of
// the slug-anchor normalization. A prose section runs to the blank line before
// the next heading while a server reports it ending at its last line of real
// content; neither reading is wrong, so the blank tail is not a disagreement.
func TestMatchAndCompare_SlugAnchorDropsTrailingBlankLines(t *testing.T) {
	t.Parallel()

	// Section covers lines 1-3, with line 3 blank; the server says 1-2.
	src := []byte("# Title\nbody\n\n# Next\n")
	sectionEnd := uint(len("# Title\nbody\n\n"))
	res := &Resolution{
		Anchor:      "title",
		SlugAnchors: true,
		SameName:    1,
		DeclOnly:    Extent{Start: 0, End: sectionEnd},
	}

	found, err := MatchAndCompare(src, res, []lsp.Symbol{{Name: "Title", StartLine: 0, EndLine: 1}})
	if !found {
		t.Fatal("found = false; want true")
	}
	if err != nil {
		t.Errorf("err = %v; want nil -- a trailing blank line is not a disagreement", err)
	}
}
