package resolve

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseOrdinal covers finding 23's merged parsing rule: docs/ANCHORS.md's
// positional "Bare#N" form requires a non-empty bare name and a strictly
// positive N, unifying what crosscheck.go's old splitOrdinal (neither
// check) and internal/synth/stage.go's isOrdinalAnchor (both checks) used
// to enforce differently.
func TestParseOrdinal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		anchor   string
		wantBare string
		wantN    int
		wantOK   bool
	}{
		{anchor: "init#2", wantBare: "init", wantN: 2, wantOK: true},
		{anchor: "init", wantOK: false},
		{anchor: "init#", wantOK: false},
		{anchor: "init#abc", wantOK: false},
		{anchor: "init#0", wantOK: false},
		{anchor: "init#-1", wantOK: false},
		{anchor: "#2", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.anchor, func(t *testing.T) {
			t.Parallel()
			bare, n, ok := ParseOrdinal(tt.anchor)
			if ok != tt.wantOK {
				t.Fatalf("ParseOrdinal(%q) ok = %v; want %v", tt.anchor, ok, tt.wantOK)
			}
			if ok {
				if bare != tt.wantBare || n != tt.wantN {
					t.Errorf("ParseOrdinal(%q) = (%q, %d); want (%q, %d)", tt.anchor, bare, n, tt.wantBare, tt.wantN)
				}
			}
		})
	}
}

// TestTSFamily_CachesLanguage covers finding 5: TSLanguage() must return
// the same *ts.Language on every call rather than invoking ts.NewLanguage
// again -- go-tree-sitter's own NewLanguage allocates a fresh *Language
// struct on every call even though the underlying grammar table is the
// same static data, so two calls returning identical pointers is only true
// once the value is actually cached rather than recomputed.
func TestTSFamily_CachesLanguage(t *testing.T) {
	t.Parallel()

	tsLang := newTypeScriptLanguage()
	a := tsLang.TSLanguage()
	b := tsLang.TSLanguage()
	if a != b {
		t.Error("TypeScript TSLanguage() returned a different pointer on a second call; want the cached one reused")
	}

	tsxLang := newTSXLanguage()
	c := tsxLang.TSLanguage()
	d := tsxLang.TSLanguage()
	if c != d {
		t.Error("TSX TSLanguage() returned a different pointer on a second call; want the cached one reused")
	}

	if a == c {
		t.Error("TypeScript and TSX must not share the same *ts.Language -- they are different grammars")
	}
}
