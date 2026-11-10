package resolve

import (
	"strings"
	"testing"
)

// TestDeclOrder_AddressesGoTypeAliases pins that `type A = B` is an
// addressable declaration. It is the one type declaration whose spec is a
// type_alias node rather than a type_spec, so a walk that only recognises the
// "_spec" node kinds drops it: naming the alias then fails as unresolvable,
// and a grouped block would address every sibling except the aliases -- which
// is the hunk a caller most often edits alone.
func TestDeclOrder_AddressesGoTypeAliases(t *testing.T) {
	t.Parallel()

	const src = `package sample

type Alias = Box

type (
	Gone = string
	Kept int
)

type Box struct{}
`
	lang := goLang(t)

	order, err := DeclOrder(lang, []byte(src))
	if err != nil {
		t.Fatalf("DeclOrder: %v", err)
	}
	if got, want := strings.Join(order, ","), "Alias,Gone,Kept,Box"; got != want {
		t.Fatalf("DeclOrder = %q; want %q", got, want)
	}

	for _, tt := range []struct{ anchor, want string }{
		{"Alias", "type Alias = Box"},
		{"Gone", "Gone = string"},
	} {
		res, err := Resolve(lang, []byte(src), tt.anchor)
		if err != nil {
			t.Errorf("Resolve(%q) = %v; want the alias to resolve", tt.anchor, err)
			continue
		}
		if got := string(srcAt([]byte(src), res.Extent)); !strings.Contains(got, tt.want) {
			t.Errorf("Resolve(%q) extent = %q; want it to cover %q", tt.anchor, got, tt.want)
		}
	}
}
