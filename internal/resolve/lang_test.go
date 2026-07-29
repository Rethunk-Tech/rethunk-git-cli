package resolve

import (
	"os"
	"path/filepath"
	"testing"
)

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
