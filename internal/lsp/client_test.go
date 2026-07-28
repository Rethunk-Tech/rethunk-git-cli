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
		{"foo.unknown", protocol.LanguageKindTypeScript},
	}

	for _, tt := range tests {
		got := languageKindFor(tt.path)
		if got != tt.want {
			t.Errorf("languageKindFor(%q) = %q; want %q", tt.path, got, tt.want)
		}
	}
}
