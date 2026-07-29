package diff

import (
	"strings"
	"testing"
)

// TestRowHint_UnanchorableSuggestsToplevelOnlyForMarkdown asserts an
// (unanchorable) row's hint tracks what @toplevel actually spans
// (internal/resolve/lang_markdown.go): for Markdown, an (unanchorable) row
// is always exactly the unreachable lede (Declarations never returns an
// entry for it), so it is always stageable via --sym path:@toplevel, not
// merely via the whole path. A code file's (unanchorable) row must keep its
// --file-only hint: @toplevel there spans every declaration already broken
// out into its own row, so suggesting it as an alternative would point at a
// region that overlaps, rather than covers, the gap.
func TestRowHint_UnanchorableSuggestsToplevelOnlyForMarkdown(t *testing.T) {
	t.Parallel()

	t.Run("rowHint", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			path string
			lang string
			want string
		}{
			{"README.md", "markdown", "-> use --sym README.md:@toplevel or --file README.md"},
			{"docs/USAGE.markdown", "markdown", "-> use --sym docs/USAGE.markdown:@toplevel or --file docs/USAGE.markdown"},
			{"auth.go", "go", "-> use --file auth.go"},
			{"svc.ts", "typescript", "-> use --file svc.ts"},
			{"svc.py", "python", "-> use --file svc.py"},
			{"unknown.rs", "", "-> use --file unknown.rs"},
		}
		for _, tt := range tests {
			row := Row{Status: StatusUnanchorable, Added: "1", Deleted: "0"}
			got := rowHint(tt.path, tt.lang, row)
			if got != tt.want {
				t.Errorf("rowHint(%q, %q, unanchorable) = %q; want %q", tt.path, tt.lang, got, tt.want)
			}
		}
	})

	// RenderText's own level guards against the hint regressing back to
	// --file-only silently if a future change routes rows through a
	// different path than rowHint.
	t.Run("RenderText agrees", func(t *testing.T) {
		t.Parallel()
		report := &Report{Files: []FileReport{
			{Path: "README.md", Rows: []Row{{Status: StatusUnanchorable, Added: "2", Deleted: "1"}}, lang: "markdown"},
		}}
		out := RenderText(report)
		if !strings.Contains(out, "--sym README.md:@toplevel") {
			t.Errorf("RenderText output = %q; want a --sym README.md:@toplevel hint", out)
		}
	})
}
