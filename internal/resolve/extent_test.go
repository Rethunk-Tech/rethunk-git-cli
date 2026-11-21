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

	lang := goLang(t)
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
	goLang := goLang(t)
	markdown := mdLang(t)

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
			if gotText := src[got.Start:got.End]; gotText != tt.want {
				t.Errorf("extent = %q; want %q", gotText, tt.want)
			}
		})
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

// TestResolve_TrailingCommentIsNotNextSymbolsDoc pins that a comment sharing
// a line with code belongs to that code, so deleting the next symbol leaves it.
func TestResolve_TrailingCommentIsNotNextSymbolsDoc(t *testing.T) {
	t.Parallel()
	tests := []struct{ ext, src, anchor, want string }{
		{".go", "package p\n\nvar A = 1 // note on A\nvar B = 2\n", "B", "var B = 2"},
		{".py", "A = 1  # note on A\nB = 2\n", "B", "B = 2"},
		{".ts", "const A = 1; // note on A\nconst B = 2;\n", "B", "const B = 2;"},
	}
	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			t.Parallel()
			lang, ok := ForExtension(tt.ext)
			if !ok {
				t.Fatalf("no adapter for %s", tt.ext)
			}
			res, err := Resolve(lang, []byte(tt.src), tt.anchor)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(srcAt([]byte(tt.src), res.Extent)); got != tt.want {
				t.Errorf("extent = %q; want %q", got, tt.want)
			}
		})
	}
}

// TestResolve_TSOverloadsFoldIntoImplementation pins that overload signatures
// are part of the implementation's extent, exported or not, and in classes.
func TestResolve_TSOverloadsFoldIntoImplementation(t *testing.T) {
	t.Parallel()
	const src = `// unrelated
function h(a: string): void;
export function f(a: string): void;
export function f(a: number): void;
export function f(a: any) {}
function g(a: string): void;
function g(a: any) {}
class C {
  m(a: string): void;
  m(a: any) {}
}
`
	lang, _ := ForExtension(".ts")
	tests := []struct{ anchor, want string }{
		{"f", "export function f(a: string): void;\nexport function f(a: number): void;\nexport function f(a: any) {}"},
		{"g", "function g(a: string): void;\nfunction g(a: any) {}"},
		{"C.m", "m(a: string): void;\n  m(a: any) {}"},
	}
	for _, tt := range tests {
		res, err := Resolve(lang, []byte(src), tt.anchor)
		if err != nil {
			t.Fatalf("%s: %v", tt.anchor, err)
		}
		if got := string(srcAt([]byte(src), res.Extent)); got != tt.want {
			t.Errorf("%s extent = %q; want %q", tt.anchor, got, tt.want)
		}
	}
}

// TestResolve_TSObjectMembersAndWrappedNames pins object-literal function
// members (qualified and bare, comma included) and a wrapped function
// expression's inner name as an alias of the same span.
func TestResolve_TSObjectMembersAndWrappedNames(t *testing.T) {
	t.Parallel()
	const src = `export const api = { rescanFleet() {}, x: () => {}, y: 1 };
const Turn = memo(function TurnView() {});
`
	lang, _ := ForExtension(".ts")
	tests := []struct{ anchor, want string }{
		{"api.rescanFleet", "rescanFleet() {},"},
		{"rescanFleet", "rescanFleet() {},"},
		{"api.x", "x: () => {},"},
		{"api.y", ""},
		{"TurnView", "const Turn = memo(function TurnView() {});"},
		{"Turn", "const Turn = memo(function TurnView() {});"},
	}
	for _, tt := range tests {
		res, err := Resolve(lang, []byte(src), tt.anchor)
		if tt.want == "" {
			if err == nil {
				t.Errorf("%s resolved; want unresolvable", tt.anchor)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tt.anchor, err)
			continue
		}
		if got := string(srcAt([]byte(src), res.Extent)); got != tt.want {
			t.Errorf("%s extent = %q; want %q", tt.anchor, got, tt.want)
		}
	}
}

// TestResolve_PythonNestedClasses pins Outer.Inner and Outer.Inner.m, with
// and without decorators on the inner class.
func TestResolve_PythonNestedClasses(t *testing.T) {
	t.Parallel()
	const src = `class Outer:
    class Inner:
        def m(self):
            pass

    @dec
    class Deco:
        def n(self):
            pass
`
	lang, _ := ForExtension(".py")
	tests := []struct{ anchor, want string }{
		{"Outer.Inner", "class Inner:\n        def m(self):\n            pass"},
		{"Outer.Inner.m", "def m(self):\n            pass"},
		{"Outer.Deco", "@dec\n    class Deco:\n        def n(self):\n            pass"},
		{"Outer.Deco.n", "def n(self):\n            pass"},
	}
	for _, tt := range tests {
		res, err := Resolve(lang, []byte(src), tt.anchor)
		if err != nil {
			t.Errorf("%s: %v", tt.anchor, err)
			continue
		}
		if got := string(srcAt([]byte(src), res.Extent)); !strings.HasPrefix(got, tt.want) {
			t.Errorf("%s extent = %q; want prefix %q", tt.anchor, got, tt.want)
		}
	}
}

// TestResolve_HTMLVoidElementExtentStopsAtStartTag pins that a void element's staged extent
// ends at its tag instead of absorbing the text after it, while an element closed
// implicitly keeps its content.
func TestResolve_HTMLVoidElementExtentStopsAtStartTag(t *testing.T) {
	t.Parallel()
	const src = "<div>\n  <img id=\"logo\">\n  Welcome text\n  <p id=\"lede\">Hi</p>\n  <ul><li id=\"a\">one\n  <li id=\"b\">two</ul>\n</div>\n"
	lang, _ := ForExtension(".html")
	tests := []struct{ anchor, want string }{
		{"img#logo", `<img id="logo">`},
		{"p#lede", `<p id="lede">Hi</p>`},
		{"li#a", "<li id=\"a\">one\n  "},
	}
	for _, tt := range tests {
		res, err := Resolve(lang, []byte(src), tt.anchor)
		if err != nil {
			t.Fatalf("%s: %v", tt.anchor, err)
		}
		if got := string(srcAt([]byte(src), res.Extent)); got != tt.want {
			t.Errorf("%s extent = %q; want %q", tt.anchor, got, tt.want)
		}
	}
}

// TestResolve_TSSignaturesInNamespaceAndDeclarationFiles pins that overloads inside a
// namespace fold into their implementation, and that a run of
// signatures with no implementation (a .d.ts) is addressable.
func TestResolve_TSSignaturesInNamespaceAndDeclarationFiles(t *testing.T) {
	t.Parallel()
	lang, _ := ForExtension(".ts")
	const ns = `namespace N {
  export function q(a: string): void;
  export function q(a: any) {}
}
declare namespace M {
  export function r(a: string): void;
  export function r(a: number): void;
}
export function f(a: string): string;
export function f(a: number): number;
`
	tests := []struct{ anchor, want string }{
		{"N.q", "export function q(a: string): void;\n  export function q(a: any) {}"},
		{"M.r", "export function r(a: string): void;\n  export function r(a: number): void;"},
		{"f", "export function f(a: string): string;\nexport function f(a: number): number;"},
	}
	for _, tt := range tests {
		res, err := Resolve(lang, []byte(ns), tt.anchor)
		if err != nil {
			t.Fatalf("%s: %v", tt.anchor, err)
		}
		if got := string(srcAt([]byte(ns), res.Extent)); got != tt.want {
			t.Errorf("%s extent = %q; want %q", tt.anchor, got, tt.want)
		}
	}
}

// TestResolve_SameLineCommentJoinsSymbol pins that a symbol's extent takes the comment
// sharing its last line, matching the line diff assigns to it.
func TestResolve_SameLineCommentJoinsSymbol(t *testing.T) {
	t.Parallel()
	lang, _ := ForExtension(".go")
	src := []byte("package p\n\nfunc A() int { return 1 } // trailing A\nfunc B() {}\n")
	res, err := Resolve(lang, src, "A")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(srcAt(src, res.Extent)); got != "func A() int { return 1 } // trailing A" {
		t.Errorf("extent = %q", got)
	}
}

// TestResolve_PythonPartialQualifiedNameSuggests pins did-you-mean for a name that skips a nesting level.
func TestResolve_PythonPartialQualifiedNameSuggests(t *testing.T) {
	t.Parallel()
	lang, _ := ForExtension(".py")
	src := []byte("class Outer:\n    class Inner:\n        def meth(self):\n            pass\n")
	for _, anchor := range []string{"Inner.meth", "Outer.meth"} {
		_, err := Resolve(lang, src, anchor)
		if err == nil || !strings.Contains(err.Error(), "did you mean: Outer.Inner.meth") {
			t.Errorf("%s: err = %v", anchor, err)
		}
	}
}
