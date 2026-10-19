package synth

import "testing"

// TestSpliceExcise_CollapsesToEOF covers spliceExcise's cold branch,
// measured at 53.8% under -short -coverpkg=./...: when the excised symbol
// was the last thing in the file (out[end:] is nothing but trailing
// newlines), collapsing the gap naively would eat HEAD's own trailing
// newline along with it. The far more common "something follows" branch is
// already exercised indirectly through cmd/rgit/index_test.go's deletion
// cases; only a last-symbol deletion reaches this one, on the blob-synthesis
// path AGENTS.md lists as breaking silently.
func TestSpliceExcise_CollapsesToEOF(t *testing.T) {
	t.Parallel()

	const twoFuncs = "func A() {}\n\nfunc B() {}\n"

	tests := []struct {
		name       string
		out        string
		start, end uint
		want       string
	}{
		{
			name:  "deleting the file's only symbol leaves it empty",
			out:   "func A() {}\n",
			start: 0,
			end:   uint(len("func A() {}\n")),
			want:  "",
		},
		{
			name:  "deleting the last of two symbols restores HEAD's own trailing newline",
			out:   twoFuncs,
			start: uint(len("func A() {}\n\n")),
			end:   uint(len(twoFuncs)),
			want:  "func A() {}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := spliceExcise([]byte(tt.out), tt.start, tt.end)
			if string(got) != tt.want {
				t.Errorf("spliceExcise() = %q; want %q", got, tt.want)
			}
		})
	}
}

// TestSwallowedBy pins the overlap rules coalesceOverlaps relies on:
// half-open HEAD intervals for ranges, worktree-extent containment for
// insertions, and identical-extent collapse for twice-named insertions.
func TestSwallowedBy(t *testing.T) {
	t.Parallel()

	rep := editOp{kind: editReplace, start: 10, end: 20, wstart: 100, wend: 200}
	del := editOp{kind: editDelete, start: 10, end: 20}
	ins := editOp{kind: editInsert, start: 10, wstart: 120, wend: 150}

	tests := []struct {
		name string
		kept []editOp
		op   editOp
		want bool
	}{
		{"range overlapping", []editOp{rep}, editOp{kind: editReplace, start: 15, end: 25}, true},
		{"range touching at end is disjoint", []editOp{rep}, editOp{kind: editReplace, start: 20, end: 30}, false},
		{"range touching at start is disjoint", []editOp{rep}, editOp{kind: editReplace, start: 0, end: 10}, false},
		{"range inside delete", []editOp{del}, editOp{kind: editDelete, start: 12, end: 14}, true},
		{"insert nested in replace worktree extent", []editOp{rep}, ins, true},
		{"insert sharing replace's exact worktree extent", []editOp{rep}, editOp{kind: editInsert, start: 20, wstart: 100, wend: 200}, true},
		{"insert starting before replace extent", []editOp{rep}, editOp{kind: editInsert, wstart: 99, wend: 150}, false},
		{"insert ending after replace extent", []editOp{rep}, editOp{kind: editInsert, wstart: 120, wend: 201}, false},
		{"empty insert extent is never swallowed", []editOp{rep}, editOp{kind: editInsert, wstart: 120, wend: 120}, false},
		{"delete never contains an insert", []editOp{del}, ins, false},
		{"identical insert extents collapse", []editOp{ins}, editOp{kind: editInsert, start: 10, wstart: 120, wend: 150}, true},
		{"same start, different end stay distinct", []editOp{ins}, editOp{kind: editInsert, wstart: 120, wend: 160}, false},
		{"same end, different start stay distinct", []editOp{ins}, editOp{kind: editInsert, wstart: 110, wend: 150}, false},
		{"empty insert extents never collapse", []editOp{{kind: editInsert, wstart: 5, wend: 5}}, editOp{kind: editInsert, wstart: 5, wend: 5}, false},
		{"insert is not swallowed by range op test", []editOp{ins}, editOp{kind: editReplace, start: 10, end: 20}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := swallowedBy(tt.kept, tt.op); got != tt.want {
				t.Errorf("swallowedBy() = %v; want %v", got, tt.want)
			}
		})
	}
}
