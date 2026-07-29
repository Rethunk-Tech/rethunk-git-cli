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
