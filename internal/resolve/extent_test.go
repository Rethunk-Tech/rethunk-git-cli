package resolve

import (
	"strings"
	"testing"
)

// TestResolve_GoDeclarationShapes pins which anchor names which bytes for the
// declaration forms where a wrong extent would commit the wrong hunk: a
// grouped const block addressing one spec, a struct field, an interface
// method, and a comma-separated const sharing one value list.
func TestResolve_GoDeclarationShapes(t *testing.T) {
	t.Parallel()

	const src = `package sample

const (
	Alpha = 1
	Beta  = 2
)

type Shape interface {
	Area() int
	Perimeter() int
}

type Box struct {
	sizeX int
	sizeY int
	A, B  float64
	Embedded
}

func (b Box) Area() int { return 1 }

type Alias = Box

type Named []int
`
	tests := []struct {
		anchor   string
		want     string
		ambiguos bool
	}{
		{"Alpha", "Alpha = 1", false},
		{"Beta", "Beta  = 2", false},
		{"Shape.Area", "Area() int", false},
		{"Shape.Perimeter", "Perimeter() int", false},
		{"Box.sizeX", "sizeX int", false},
		{"Box.Area", "func (b Box) Area() int { return 1 }", false},
		{"Alias", "type Alias = Box", false},
		{"Named", "type Named []int", false},
		// A comma-separated field has no sub-range naming one name without
		// the other, so neither gets an anchor of its own.
		{"Box.A", "", false},
		{"Box.B", "", false},
		// An embedded field names no identifier of its own.
		{"Box.Embedded", "", false},
		// The grouped block as a whole is not an anchor either: its specs
		// are.
		{"", "", false},
	}

	lang := langForExt(t, ".go")
	for _, tt := range tests {
		if tt.anchor == "" {
			continue
		}
		t.Run(tt.anchor, func(t *testing.T) {
			t.Parallel()
			res, err := Resolve(lang, []byte(src), tt.anchor)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("Resolve(%q) extent = %q; want unresolvable", tt.anchor, srcAt([]byte(src), res.Extent))
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q) = %v", tt.anchor, err)
			}
			if got := string(srcAt([]byte(src), res.Extent)); !strings.Contains(got, tt.want) {
				t.Errorf("Resolve(%q) extent = %q; want it to cover %q", tt.anchor, got, tt.want)
			}
		})
	}

	// The grouped block's own keyword belongs to no spec, so no anchor's
	// extent starts before Alpha.
	alpha, err := Resolve(lang, []byte(src), "Alpha")
	if err != nil {
		t.Fatalf("Resolve(Alpha) = %v", err)
	}
	if strings.Contains(string(srcAt([]byte(src), alpha.Extent)), "const") {
		t.Errorf("Resolve(Alpha) extent = %q; want only the spec, not the block keyword", srcAt([]byte(src), alpha.Extent))
	}
}

// TestExtendThroughOwnedSeparator pins the widening rule synth's new-file
// preamble depends on: a language that owns its trailing separator widens
// through the following whitespace but never past limit, and one that does
// not leaves the extent untouched.
func TestExtendThroughOwnedSeparator(t *testing.T) {
	t.Parallel()

	const src = "package sample\n\nimport \"fmt\"\n\nfunc A() {}\n"
	goLang := langForExt(t, ".go")
	markdown := langForExt(t, ".md")

	header, err := Resolve(goLang, []byte(src), "@header")
	if err != nil {
		t.Fatalf("Resolve(@header) = %v", err)
	}
	imports, err := Resolve(goLang, []byte(src), "@imports")
	if err != nil {
		t.Fatalf("Resolve(@imports) = %v", err)
	}

	tests := []struct {
		name  string
		lang  Language
		ext   Extent
		limit uint
		want  string
	}{
		{
			name:  "go header absorbs the gofmt blank line",
			lang:  goLang,
			ext:   header.Extent,
			limit: imports.Extent.Start,
			want:  "package sample\n\n",
		},
		{
			name:  "go header stops at its limit",
			lang:  goLang,
			ext:   header.Extent,
			limit: header.Extent.End + 1,
			want:  "package sample" + "\n",
		},
		{
			name:  "a language with no separator convention is unchanged",
			lang:  markdown,
			ext:   Extent{Start: 0, End: 3},
			limit: 10,
			want:  "pac",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExtendThroughOwnedSeparator(tt.lang, []byte(src), tt.ext, tt.limit)
			if got.Start != tt.ext.Start {
				t.Errorf("Start = %d; want the original %d", got.Start, tt.ext.Start)
			}
			if gotText := string(src[got.Start:got.End]); gotText != tt.want {
				t.Errorf("extent = %q; want %q", gotText, tt.want)
			}
		})
	}
}

// TestResolvePseudo_HeaderStopsBeforeImports pins the bound @header takes
// from @imports: the header run ends where the import statement begins, so
// the import line itself is never staged as part of @header even though a
// comment directly above it still is (comments are header kinds, and the run
// is continuous to its bound).
func TestResolvePseudo_HeaderStopsBeforeImports(t *testing.T) {
	t.Parallel()

	const src = "package sample\n\n// stdlib\nimport \"fmt\"\n\nfunc A() {}\n"
	lang := langForExt(t, ".go")

	header, err := Resolve(lang, []byte(src), "@header")
	if err != nil {
		t.Fatalf("Resolve(@header) = %v", err)
	}
	if got, want := string(srcAt([]byte(src), header.Extent)), "package sample\n\n// stdlib"; got != want {
		t.Errorf("Resolve(@header) = %q; want %q", got, want)
	}

	imports, err := Resolve(lang, []byte(src), "@imports")
	if err != nil {
		t.Fatalf("Resolve(@imports) = %v", err)
	}
	if got, want := string(srcAt([]byte(src), imports.Extent)), "import \"fmt\""; got != want {
		t.Errorf("Resolve(@imports) = %q; want %q", got, want)
	}
}

// TestEnclosingName pins the preference order synth's insertion path reads:
// a declaration's real enclosing block wins over its container, and a
// container stands in when there is no separate enclosing block.
func TestEnclosingName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		res  Resolution
		want string
	}{
		{"enclosing wins", Resolution{Container: "Config", Enclosing: "impl Config"}, "impl Config"},
		{"container when there is no enclosing block", Resolution{Container: "Box"}, "Box"},
		{"neither", Resolution{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.res.EnclosingName(); got != tt.want {
				t.Errorf("EnclosingName() = %q; want %q", got, tt.want)
			}
		})
	}
}
