package resolve

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/lsp"
)

func TestFile_OpenReusesIndexAcrossAnchors(t *testing.T) {
	t.Parallel()

	lang := goLang(t)
	f, err := Open(lang, []byte(goTwoGetters))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(f.Close)

	order := f.DeclOrder()
	if got, want := strings.Join(order, ","), "A,A.Get,B,B.Get"; got != want {
		t.Fatalf("DeclOrder = %q; want %q", got, want)
	}
	for _, d := range f.DeclExtents() {
		res, err := f.Resolve(d.Anchor)
		if err != nil {
			t.Errorf("Resolve(%q) = %v", d.Anchor, err)
			continue
		}
		if res.Extent != d.Extent {
			t.Errorf("Resolve(%q).Extent = %+v; DeclExtents says %+v", d.Anchor, res.Extent, d.Extent)
		}
	}
}

func TestCrossCheckExtents_EmptyAndPseudoSkipDial(t *testing.T) {
	t.Parallel()

	deg, mm := CrossCheckExtents(t.Context(), nil, nil, "", "", nil, nil)
	if !deg || mm != nil {
		t.Errorf("empty list = (%v, %v); want degraded, nil mismatches", deg, mm)
	}

	deg, mm = CrossCheckExtents(t.Context(), nil, nil, "", "", nil, []*Resolution{nil, {Pseudo: true}})
	if deg || mm != nil {
		t.Errorf("pseudo-only list = (%v, %v); want not degraded, nil mismatches", deg, mm)
	}
}

func TestCrossCheckExtents_ComparableListDials(t *testing.T) {
	t.Parallel()

	lang, ok := ForExtension(".json")
	if !ok {
		t.Fatal("ForExtension(.json) not registered")
	}
	src := []byte("{\"foo\": 1}\n")
	res, err := Resolve(lang, src, "foo")
	if err != nil {
		t.Fatalf("Resolve(foo): %v", err)
	}

	sess := lsp.NewSession()
	t.Cleanup(sess.Close)
	_, mm := CrossCheckExtents(t.Context(), sess, lang, t.TempDir(), filepath.Join(t.TempDir(), "a.json"), src, []*Resolution{res})
	for i, err := range mm {
		if err == nil {
			t.Errorf("mismatches[%d] = nil", i)
		}
	}
}

func TestLastOffset_EmptyExtent(t *testing.T) {
	t.Parallel()

	if got := lastOffset(Extent{Start: 5, End: 5}); got != 5 {
		t.Errorf("lastOffset(empty) = %d; want 5", got)
	}
}

func TestSlugQualified(t *testing.T) {
	t.Parallel()

	if got, want := slugQualified(lsp.Symbol{Name: "Diff Scope"}, "."), "diff-scope"; got != want {
		t.Errorf("bare slug = %q; want %q", got, want)
	}
	if got, want := slugQualified(lsp.Symbol{Name: "Child", Container: "Parent Section"}, "."), "parent-section.child"; got != want {
		t.Errorf("qualified slug = %q; want %q", got, want)
	}
}

func TestHTMLServerNameMatches_EmptyAnchor(t *testing.T) {
	t.Parallel()

	if htmlServerNameMatches("div#app", "") {
		t.Error("empty anchor matched; want false")
	}
}

func TestLanguageForPathFolding_HeadSampleErrorAndAbsent(t *testing.T) {
	t.Parallel()

	lang, ok, peeked, err := LanguageForPathFolding(t.TempDir(), "hook", false, func() ([]byte, bool, error) {
		return nil, false, errors.New("missing blob")
	})
	if err == nil {
		t.Fatalf("LanguageForPathFolding(HEAD error) = (%v, %v, peeked=%v, err=nil); want the sample error", lang, ok, peeked)
	}

	lang, ok, peeked, err = LanguageForPathFolding(t.TempDir(), "hook", false, func() ([]byte, bool, error) {
		return nil, false, nil
	})
	if err != nil || ok || lang != nil || peeked {
		t.Errorf("absent HEAD sample = (%v, %v, peeked=%v, err=%v); want nil, false, false, nil", lang, ok, peeked, err)
	}
}

func TestLanguageForPathFolding_ExtensionDoesNotPeek(t *testing.T) {
	t.Parallel()

	lang, ok, peeked, err := LanguageForPathFolding(t.TempDir(), "x.go", false, func() ([]byte, bool, error) {
		t.Error("HEAD sample must not run for a path that already has an extension")
		return nil, false, nil
	})
	if err != nil {
		t.Fatalf("LanguageForPathFolding(x.go): %v", err)
	}
	if !ok || lang == nil || lang.Name() != "go" || peeked {
		t.Errorf("LanguageForPathFolding(x.go) = (%v, %v, peeked=%v); want go, true, false", lang, ok, peeked)
	}
}

func TestGatedTag_SQLWhenUnregistered(t *testing.T) {
	t.Parallel()

	if tag, ok := GatedTag(".go"); ok {
		t.Errorf("GatedTag(.go) = (%q, true); want ok=false for a grammar this build already has", tag)
	}
	if tag, ok := GatedTag(".rb"); ok {
		t.Errorf("GatedTag(.rb) = (%q, true); want ok=false for an unknown extension", tag)
	}

	if _, registered := ForExtension(".sql"); registered {
		tag, ok := GatedTag(".sql")
		if ok {
			t.Errorf("GatedTag(.sql) = (%q, true) while SQL is registered; want ok=false", tag)
		}
		return
	}
	tag, ok := GatedTag(".sql")
	if !ok || tag != SQLBuildTag {
		t.Errorf("GatedTag(.sql) = (%q, %v); want (%q, true)", tag, ok, SQLBuildTag)
	}
}

func TestPeekShebangLine_NestedPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "bin", "script")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\necho\n"), 0o700); err != nil { //nolint:gosec // executable mode matches a shebang file
		t.Fatal(err)
	}
	line, exists, ok := peekShebangLine(dir, "bin/script")
	if !exists || !ok {
		t.Fatal("peekShebangLine(bin/script) ok = false; want true")
	}
	if string(line) != "#!/usr/bin/env bash\n" {
		t.Errorf("line = %q; want the shebang", line)
	}
}

func TestDeclOrder_LanguageAdapters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ext  string
		src  string
		want string
	}{
		{
			name: "python function class members assignment decorator",
			ext:  ".py",
			src:  "import os\n\n@dec\ndef foo():\n    pass\n\nclass Box:\n    size = 1\n    def get(self):\n        return 1\n\nx = 2\n",
			want: "foo,Box,Box.get,x",
		},
		{
			name: "rust struct impl method and free function",
			ext:  ".rs",
			src:  "struct Config { size: u32 }\n\nimpl Config {\n    fn new() -> Self { Self { size: 0 } }\n}\n\nfn main() {}\n",
			want: "Config,Config::size,impl Config,Config::new,main",
		},
		{
			name: "html tag with id",
			ext:  ".html",
			src:  "<div id=\"app\" class=\"widget\"><span id=\"title\">x</span></div>\n",
			want: "div#app,span#title",
		},
		{
			name: "typescript function class members and const",
			ext:  ".ts",
			src:  "import { x } from './x';\nexport function foo() {}\nclass Box {\n  size: number;\n  get() { return this.size; }\n}\nconst y = 1;\n",
			want: "foo,Box,Box.size,Box.get,y",
		},
		{
			name: "toml tables and keys",
			ext:  ".toml",
			src:  "[package]\nname = \"x\"\n\n[dependencies]\nfoo = \"1\"\n",
			want: "package,package.name,dependencies,dependencies.foo",
		},
		{
			name: "css nested and grouped selectors",
			ext:  ".css",
			src:  "@import \"x.css\";\n.parent { .child { color: red; } }\n.a, .b { color: blue; }\n",
			want: ".parent,.parent .child,.a,.b",
		},
		{
			name: "json nested object",
			ext:  ".json",
			src:  "{\n  \"foo\": 1,\n  \"bar-baz\": { \"nested\": true }\n}\n",
			want: "foo,bar-baz,bar-baz.nested",
		},
		{
			name: "yaml quoted and bare keys",
			ext:  ".yaml",
			src:  "foo: 1\n\"quoted\": 2\n'also': 3\n",
			want: "foo,quoted,also",
		},
		{
			name: "markdown setext and inline markup in headings",
			ext:  ".md",
			src:  "Intro.\n\nHello `World`\n=============\n\n## A **bold** title\n\nBody.\n",
			want: "hello-world,a-bold-title",
		},
		{
			name: "typescript interface enum type namespace and default",
			ext:  ".ts",
			src:  "export interface I { n: number }\nexport type T = number;\nexport enum E { A }\nnamespace N { export const z = 1; }\nexport default function d() {}\n",
			want: "I,T,E,N,N.z,d",
		},
		{
			name: "rust trait enum and impl-for",
			ext:  ".rs",
			src:  "struct Config {}\ntrait Render { fn draw(&self); }\nenum Kind { A }\nimpl Render for Config { fn draw(&self) {} }\n",
			want: "Config,Render,Render::draw,Kind,Kind::A,impl Render for Config,Config::draw",
		},
		{
			name: "toml quoted keys",
			ext:  ".toml",
			src:  "\"quoted\" = 1\n'also' = 2\n",
			want: "quoted,also",
		},
		{
			name: "json top-level array is not a keyed object",
			ext:  ".json",
			src:  "[1, 2]\n",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lang, ok := ForExtension(tt.ext)
			if !ok {
				t.Fatalf("ForExtension(%q) not registered", tt.ext)
			}
			order, err := DeclOrder(lang, []byte(tt.src))
			if err != nil {
				t.Fatalf("DeclOrder: %v", err)
			}
			if got := strings.Join(order, ","); got != tt.want {
				t.Fatalf("DeclOrder = %q; want %q", got, tt.want)
			}
			for _, anchor := range order {
				if _, err := Resolve(lang, []byte(tt.src), anchor); err != nil {
					t.Errorf("Resolve(%q) = %v", anchor, err)
				}
			}
		})
	}
}

func TestResolve_ImportsPseudoOnStatementLanguages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ext  string
		src  string
		want string
	}{
		{".py", "import os\nfrom sys import path\n\ndef foo():\n    pass\n", "import os\nfrom sys import path"},
		{".ts", "import { x } from './x';\nimport y from './y';\nexport function foo() {}\n", "import { x } from './x';\nimport y from './y';"},
		{".rs", "use std::io;\nuse std::fmt;\n\nfn main() {}\n", "use std::io;\nuse std::fmt;"},
		{".css", "@import \"x.css\";\n.parent { color: red; }\n", "@import \"x.css\";"},
	}
	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			t.Parallel()
			lang, ok := ForExtension(tt.ext)
			if !ok {
				t.Fatalf("ForExtension(%q) not registered", tt.ext)
			}
			res, err := Resolve(lang, []byte(tt.src), "@imports")
			if err != nil {
				t.Fatalf("Resolve(@imports) = %v", err)
			}
			if !res.Pseudo {
				t.Error("Resolve(@imports).Pseudo = false; want true")
			}
			if got := string(srcAt([]byte(tt.src), res.Extent)); got != tt.want {
				t.Errorf("Resolve(@imports) = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestFile_CloseNil(t *testing.T) {
	t.Parallel()
	var f *File
	f.Close()
}
