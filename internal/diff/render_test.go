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
		// Two rows, not six: go, typescript, and python all take the same
		// --file-only branch as unknown.rs (unanchorableHint only ever asks
		// "is lang == markdown?"), so one non-Markdown row already covers
		// what the other three would only repeat. unknown.rs is kept over
		// auth.go/svc.ts/svc.py because it is also the empty-lang case, not
		// merely another named language. The two Markdown rows are not
		// redundant with each other: both take the --sym ...@toplevel
		// branch, but the assertion is that the *shape* holds regardless of
		// which markdown extension supplied it, and both a .md and a
		// .markdown path are real docs/USAGE.md-recognized inputs.
		tests := []struct {
			path string
			lang string
			want string
		}{
			{"README.md", "markdown", "-> use --sym README.md:@toplevel or --file README.md"},
			{"docs/USAGE.markdown", "markdown", "-> use --sym docs/USAGE.markdown:@toplevel or --file docs/USAGE.markdown"},
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

// TestRowLabelAndRowHint_ThinStatuses covers rowLabel's and rowHint's own
// placeholder branches for (mode), (binary), and (untracked) -- measured at
// 44.4% and 42.9% respectively under -short -coverpkg=./..., since the test
// above only ever drives StatusUnanchorable. cmd/rgit/rgit_e2e_test.go does
// see mode and binary rows, but execs a separate binary and so contributes
// nothing to this package's own coverprofile.
func TestRowLabelAndRowHint_ThinStatuses(t *testing.T) {
	t.Parallel()

	t.Run("rowLabel", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			row  Row
			want string
		}{
			{"mode note", Row{Status: StatusMode, ModeNote: "644->755"}, "(mode 644->755)"},
			{"binary", Row{Status: StatusBinary}, "(binary)"},
			{"untracked", Row{Status: StatusUntracked}, "(untracked)"},
		}
		for _, tt := range tests {
			if got := rowLabel(tt.row); got != tt.want {
				t.Errorf("%s: rowLabel = %q; want %q", tt.name, got, tt.want)
			}
		}
	})

	t.Run("rowHint", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			row  Row
			want string
		}{
			{"mode", Row{Status: StatusMode, ModeNote: "644->755"}, "-> use rgit commit script.sh"},
			{"untracked with a resolvable symbol", Row{Status: StatusUntracked, HintSymbol: "Handler"}, "-> use --sym script.sh:Handler or --file script.sh"},
			{"untracked with no symbols at all", Row{Status: StatusUntracked}, "-> use --file script.sh"},
			{"binary has no hint", Row{Status: StatusBinary}, ""},
		}
		for _, tt := range tests {
			if got := rowHint("script.sh", "shell", tt.row); got != tt.want {
				t.Errorf("%s: rowHint = %q; want %q", tt.name, got, tt.want)
			}
		}
	})

	// RenderText's own level: a mode row and a binary row both reach the
	// tabwriter with their placeholder label, proving RenderText routes
	// through rowLabel/rowHint here too rather than a second, drifting
	// rendering only the (unanchorable) case above would catch.
	t.Run("RenderText agrees", func(t *testing.T) {
		t.Parallel()
		report := &Report{Files: []FileReport{
			{Path: "script.sh", Rows: []Row{{Status: StatusMode, ModeNote: "644->755", Added: "0", Deleted: "0"}}, lang: "shell"},
			{Path: "logo.png", Rows: []Row{{Status: StatusBinary, Added: "-", Deleted: "-"}}},
		}}
		out := RenderText(report)
		if !strings.Contains(out, "(mode 644->755)") {
			t.Errorf("RenderText output = %q; want a (mode 644->755) row", out)
		}
		if !strings.Contains(out, "(binary)") {
			t.Errorf("RenderText output = %q; want a (binary) row", out)
		}
	})
}
