//go:build rgit_sql

package sqlgrammar

import (
	"testing"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// TestLanguage_Parses proves the generated csrc/ actually links and parses,
// not merely that it compiles. Gated behind rgit_sql the same as grammar.go
// itself: this test can only run after the ~60-90s `tree-sitter generate`
// step (cmd/rgit-install's generateSQLParser, or `make sql-generate` if one
// exists), so it has no business in the default `go test ./...` lane
// (CONTRIBUTING.md § Tests) -- the lang_sql.go adapter's own tests carry the
// node-shape assertions; this is only "does the binding itself work".
func TestLanguage_Parses(t *testing.T) {
	lang := ts.NewLanguage(Language())
	if lang == nil {
		t.Fatal("ts.NewLanguage(Language()) returned nil")
	}

	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}

	tree := parser.Parse([]byte("SELECT 1;"), nil)
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		t.Fatalf("parse produced an ERROR node: %s", root.ToSexp())
	}
	if root.Kind() != "program" {
		t.Fatalf("root kind = %q, want %q", root.Kind(), "program")
	}
}
