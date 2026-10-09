package resolve

import (
	"slices"
	"testing"
)

func TestJSON5_Declarations(t *testing.T) {
	t.Parallel()
	lang, ok := ForExtension(".json5")
	if !ok {
		t.Fatal(".json5 is not registered")
	}
	src := []byte("// app config\n{\n  name: 'demo',\n  // limits\n  \"limits\": {\n    max: 10, /* inline */\n    'min': 1,\n  },\n  list: [1, 2,],\n}\n")

	got, err := DeclOrder(lang, src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"name", "limits", "limits.max", "limits.min", "list"}
	if !slices.Equal(got, want) {
		t.Fatalf("DeclOrder = %v; want %v", got, want)
	}

	res, err := Resolve(lang, src, "limits.max")
	if err != nil {
		t.Fatal(err)
	}
	if text := string(src[res.DeclOnly.Start:res.DeclOnly.End]); text != "max: 10" {
		t.Errorf("declaration-only extent = %q; want %q", text, "max: 10")
	}

	res, err = Resolve(lang, src, "limits")
	if err != nil {
		t.Fatal(err)
	}
	if text := string(src[res.Extent.Start:res.Extent.End]); text[:9] != "// limits" {
		t.Errorf("extent starts %q; want the attached doc comment", text[:9])
	}
}

func TestJSON5_NonObjectRootHasNoSymbols(t *testing.T) {
	t.Parallel()
	lang, _ := ForExtension(".json5")
	got, err := DeclOrder(lang, []byte("[1, 2, 3]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("DeclOrder = %v; want none", got)
	}
}
