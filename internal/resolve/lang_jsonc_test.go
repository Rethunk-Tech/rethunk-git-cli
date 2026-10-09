package resolve

import (
	"slices"
	"testing"
)

func TestJSONC_DeclarationsAndComments(t *testing.T) {
	t.Parallel()
	lang, ok := ForExtension(".jsonc")
	if !ok {
		t.Fatal(".jsonc is not registered")
	}
	src := []byte("// tool settings\n{\n  // the editor block\n  \"editor\": {\n    /* width */ \"tabSize\": 2\n  },\n  \"name\": \"demo\"\n}\n")

	got, err := DeclOrder(lang, src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"editor", "editor.tabSize", "name"}
	if !slices.Equal(got, want) {
		t.Fatalf("DeclOrder = %v; want %v", got, want)
	}

	res, err := Resolve(lang, src, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if text := string(src[res.Extent.Start:res.Extent.End]); text[:3] != "// " {
		t.Errorf("full extent starts %q; want the doc comment attributed to the key", text[:3])
	}
	if text := string(src[res.DeclOnly.Start:res.DeclOnly.End]); text[:8] != `"editor"` {
		t.Errorf("declaration-only extent starts %q; want the key itself", text[:8])
	}
}

func TestJSONC_TrailingComma(t *testing.T) {
	t.Parallel()
	lang, _ := ForExtension(".jsonc")
	src := []byte("{\n  \"a\": 1,\n  \"b\": 2,\n}\n")
	got, err := DeclOrder(lang, src)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "a") {
		t.Fatalf("DeclOrder = %v; want it to keep the keys before the trailing comma", got)
	}
}
