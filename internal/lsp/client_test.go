package lsp

import (
	"testing"

	"go.lsp.dev/protocol"
)

func TestLanguageKindFor(t *testing.T) {
	tests := []struct {
		path string
		want protocol.LanguageKind
	}{
		{"foo.go", protocol.LanguageKindGo},
		{"foo.ts", protocol.LanguageKindTypeScript},
		{"foo.mts", protocol.LanguageKindTypeScript},
		{"foo.cts", protocol.LanguageKindTypeScript},
		{"foo.tsx", protocol.LanguageKindTypeScriptReact},
		{"foo.jsx", protocol.LanguageKindJavaScriptReact},
		{"foo.js", protocol.LanguageKindJavaScript},
		{"foo.mjs", protocol.LanguageKindJavaScript},
		{"foo.cjs", protocol.LanguageKindJavaScript},
		{"foo.py", protocol.LanguageKindPython},
		{"foo.pyi", protocol.LanguageKindPython},
		{"foo.sh", protocol.LanguageKindShellScript},
		{"foo.bash", protocol.LanguageKindShellScript},
		{"foo.yaml", protocol.LanguageKindYAML},
		{"foo.yml", protocol.LanguageKindYAML},
		{"foo.json", protocol.LanguageKindJSON},
		{"foo.css", protocol.LanguageKindCSS},
		{"foo.md", protocol.LanguageKindMarkdown},
		{"foo.markdown", protocol.LanguageKindMarkdown},
		{"foo.unknown", protocol.LanguageKindTypeScript},
	}

	for _, tt := range tests {
		got := languageKindFor(tt.path)
		if got != tt.want {
			t.Errorf("languageKindFor(%q) = %q; want %q", tt.path, got, tt.want)
		}
	}
}

// TestTrimTrailingBlankLines pins the guarantee trimTrailingBlankLines
// exists for: a yaml-language-server range for a nested container
// consistently extends one line past its own last real content, through a
// blank line separating it from a following sibling, and trimming it back
// must not depend on a live server to verify. This is the unit-lane
// guarantee (CONTRIBUTING.md) -- it must fail here, unlike the server-dial
// tests in servers_test.go which need a live one.
func TestTrimTrailingBlankLines(t *testing.T) {
	src := []byte("build:\n  a: 1\n\ntest:\n  b: 2\n")
	// Lines: 0 "build:", 1 "  a: 1", 2 "", 3 "test:", 4 "  b: 2", then a
	// trailing empty element from the final newline at index 5.
	tests := []struct {
		name           string
		start, end     uint32
		wantTrimmedEnd uint32
	}{
		{"trims the single blank separator before a sibling", 0, 2, 1},
		{"stops at a non-blank line immediately", 0, 1, 1},
		{"never trims down to or past its own StartLine", 2, 2, 2},
		{"never trims the file's own final element (EOF, no sibling follows)", 3, 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syms := []Symbol{{Name: "x", StartLine: tt.start, EndLine: tt.end}}
			trimTrailingBlankLines(src, syms)
			if syms[0].EndLine != tt.wantTrimmedEnd {
				t.Errorf("EndLine = %d; want %d", syms[0].EndLine, tt.wantTrimmedEnd)
			}
		})
	}
}
