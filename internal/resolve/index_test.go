package resolve

import (
	"errors"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/exitcode"
)

// goTwoGetters is the ambiguity fixture: two methods named Get, each on its
// own receiver, so the bare name resolves to neither of them.
const goTwoGetters = `package sample

type A struct{}

func (a *A) Get() int { return 1 }

type B struct{}

func (b *B) Get() int { return 2 }
`

// TestResolve_BareNameMatchingSeveralContainersIsAmbiguous pins the exit-4
// answer for a bare name that exists only inside containers. Reporting it as
// absent instead would send the caller looking for a spelling that does not
// exist, when the honest remedy is to qualify with one of the candidates.
func TestResolve_BareNameMatchingSeveralContainersIsAmbiguous(t *testing.T) {
	t.Parallel()

	lang := langForExt(t, ".go")
	_, err := Resolve(lang, []byte(goTwoGetters), "Get")

	rerr, ok := AsResolveError(err)
	if !ok {
		t.Fatalf("Resolve(Get) error = %v; want *ResolveError", err)
	}
	if rerr.Code != exitcode.AnchorAmbiguous {
		t.Errorf("Resolve(Get) code = %v; want %v", rerr.Code, exitcode.AnchorAmbiguous)
	}
	if got, want := strings.Join(rerr.Candidates, ","), "A.Get,B.Get"; got != want {
		t.Errorf("Resolve(Get) candidates = %q; want %q", got, want)
	}
	if !strings.Contains(rerr.Error(), "qualify with one of: A.Get, B.Get") {
		t.Errorf("Resolve(Get) message = %q; want the qualification instruction", rerr.Error())
	}
	if _, ambiguous := AsAmbiguous(err); !ambiguous {
		t.Error("AsAmbiguous(exit-4 error) = false; want true")
	}

	// Each candidate the error names must itself resolve, or the suggested
	// remedy would be as unusable as the bare name was.
	for _, anchor := range rerr.Candidates {
		res, err := Resolve(lang, []byte(goTwoGetters), anchor)
		if err != nil {
			t.Errorf("Resolve(%q) (a suggested candidate) = %v; want success", anchor, err)
			continue
		}
		want := "func (" + strings.ToLower(anchor[:1]) + " *" + anchor[:1] + ") Get() int"
		if !strings.HasPrefix(string(srcAt([]byte(goTwoGetters), res.Extent)), want) {
			t.Errorf("Resolve(%q) extent = %q; want it to start at %q", anchor, srcAt([]byte(goTwoGetters), res.Extent), want)
		}
	}
}

// TestResolve_SuggestsTheContainerQualifiedSpelling pins suggest's own reason
// for measuring distance against the bare name rather than the qualified one:
// a typo in a name that exists only inside a container must still produce a
// candidate, and that candidate has to be the qualified spelling -- the bare
// one would not resolve.
func TestResolve_SuggestsTheContainerQualifiedSpelling(t *testing.T) {
	t.Parallel()

	const src = `package sample

type Box struct {
	sizeX int
}
`
	lang := langForExt(t, ".go")
	_, err := Resolve(lang, []byte(src), "sizY")

	rerr, ok := AsResolveError(err)
	if !ok {
		t.Fatalf("Resolve(sizY) error = %v; want *ResolveError", err)
	}
	if rerr.Code != exitcode.AnchorUnresolvable {
		t.Errorf("Resolve(sizY) code = %v; want %v", rerr.Code, exitcode.AnchorUnresolvable)
	}
	if got, want := strings.Join(rerr.Candidates, ","), "Box.sizeX"; got != want {
		t.Errorf("Resolve(sizY) candidates = %q; want %q", got, want)
	}
	if !strings.Contains(rerr.Error(), "did you mean: Box.sizeX?") {
		t.Errorf("Resolve(sizY) message = %q; want the did-you-mean suffix", rerr.Error())
	}
}

// TestResolve_RawHeadingTextResolvesOnlyForMarkdown pins the fallback's own
// gate: a heading's raw title resolves in a markdown file, and the identical
// anchor in a Go file stays unresolvable rather than being slugified into
// some unrelated declaration.
func TestResolve_RawHeadingTextResolvesOnlyForMarkdown(t *testing.T) {
	t.Parallel()

	const md = "Intro text.\n\n# Diff Scope\n\nBody.\n"
	markdown := langForExt(t, ".md")

	slug, err := Resolve(markdown, []byte(md), "diff-scope")
	if err != nil {
		t.Fatalf("Resolve(diff-scope) = %v; want the slug to resolve", err)
	}
	raw, err := Resolve(markdown, []byte(md), "Diff Scope")
	if err != nil {
		t.Fatalf("Resolve(Diff Scope) = %v; want a heading's raw text to resolve", err)
	}
	if raw.Extent != slug.Extent {
		t.Errorf("raw heading extent = %+v; want the slug's own %+v", raw.Extent, slug.Extent)
	}

	const goSrc = "package sample\n\nfunc install() {}\n"
	goLang := langForExt(t, ".go")
	_, err = Resolve(goLang, []byte(goSrc), "Install")
	rerr, ok := AsResolveError(err)
	if !ok || rerr.Code != exitcode.AnchorUnresolvable {
		t.Fatalf("Resolve(Install) in Go = %v; want an unresolvable *ResolveError", err)
	}
	if _, ambiguous := AsAmbiguous(err); ambiguous {
		t.Error("AsAmbiguous(unresolvable error) = true; want false")
	}
}

// TestResolve_GoplsReceiverSpelling covers the input normalization: an
// anchor copied out of gopls's outline, "(*A).Get" or "(A).Get", names the
// same declaration rgit emits as "A.Get".
func TestResolve_GoplsReceiverSpelling(t *testing.T) {
	t.Parallel()

	lang := langForExt(t, ".go")
	want, err := Resolve(lang, []byte(goTwoGetters), "A.Get")
	if err != nil {
		t.Fatalf("Resolve(A.Get) = %v", err)
	}
	for _, anchor := range []string{"(*A).Get", "(A).Get", "( A ).Get"} {
		got, err := Resolve(lang, []byte(goTwoGetters), anchor)
		if err != nil {
			t.Errorf("Resolve(%q) = %v; want the normalized A.Get", anchor, err)
			continue
		}
		if got.Extent != want.Extent {
			t.Errorf("Resolve(%q) extent = %+v; want A.Get's own %+v", anchor, got.Extent, want.Extent)
		}
	}

	// An unclosed receiver is not a gopls spelling; normalizing it must not
	// invent a name out of the dangling text.
	for _, anchor := range []string{"(A.Get", "()"} {
		if _, err := Resolve(lang, []byte(goTwoGetters), anchor); err == nil {
			t.Errorf("Resolve(%q) = nil error; want unresolvable", anchor)
		}
	}
}

// TestResolve_OrdinalsSelectOneOfRepeatedNames pins the positional form the
// resolver emits when a repeated bare name has no container to disambiguate
// it: the bare name stays ambiguous, and each emitted ordinal resolves to
// its own declaration.
func TestResolve_OrdinalsSelectOneOfRepeatedNames(t *testing.T) {
	t.Parallel()

	const src = "package sample\n\nfunc init() {}\n\nfunc init() {}\n"
	lang := langForExt(t, ".go")

	order, err := DeclOrder(lang, []byte(src))
	if err != nil {
		t.Fatalf("DeclOrder: %v", err)
	}
	if got, want := strings.Join(order, ","), "init#1,init#2"; got != want {
		t.Fatalf("DeclOrder = %q; want %q", got, want)
	}

	first, err := Resolve(lang, []byte(src), "init#1")
	if err != nil {
		t.Fatalf("Resolve(init#1) = %v", err)
	}
	second, err := Resolve(lang, []byte(src), "init#2")
	if err != nil {
		t.Fatalf("Resolve(init#2) = %v", err)
	}
	if first.Extent.Start >= second.Extent.Start {
		t.Errorf("init#1 starts at %d, init#2 at %d; want them in source order", first.Extent.Start, second.Extent.Start)
	}

	_, err = Resolve(lang, []byte(src), "init")
	rerr, ok := AsResolveError(err)
	if !ok || rerr.Code != exitcode.AnchorAmbiguous {
		t.Fatalf("Resolve(init) = %v; want ambiguous", err)
	}
}

// TestResolve_PseudoAnchors covers the three pseudo-anchors against a real
// Go file, and the graceful "nothing to claim" answer in a file that has no
// such region -- degraded, not an error for @imports, but exit 3 for an
// anchor nothing names.
func TestResolve_PseudoAnchors(t *testing.T) {
	t.Parallel()

	const src = "// Package sample documents it.\npackage sample\n\nimport \"fmt\"\n\nfunc A() { fmt.Println() }\n"
	lang := langForExt(t, ".go")

	tests := []struct {
		anchor string
		want   string
	}{
		{"@header", "// Package sample documents it.\npackage sample"},
		{"@imports", "import \"fmt\""},
		{"@toplevel", "func A() { fmt.Println() }"},
	}
	for _, tt := range tests {
		t.Run(tt.anchor, func(t *testing.T) {
			t.Parallel()
			res, err := Resolve(lang, []byte(src), tt.anchor)
			if err != nil {
				t.Fatalf("Resolve(%s) = %v", tt.anchor, err)
			}
			if !res.Pseudo {
				t.Errorf("Resolve(%s).Pseudo = false; want true", tt.anchor)
			}
			if got := string(srcAt([]byte(src), res.Extent)); got != tt.want {
				t.Errorf("Resolve(%s) extent = %q; want %q", tt.anchor, got, tt.want)
			}
		})
	}

	bare := []byte("package sample\n")
	for _, anchor := range []string{"@imports", "@toplevel"} {
		_, err := Resolve(lang, bare, anchor)
		rerr, ok := AsResolveError(err)
		if !ok || rerr.Code != exitcode.AnchorUnresolvable {
			t.Errorf("Resolve(%s) of a bare package clause = %v; want an unresolvable *ResolveError", anchor, err)
		}
	}
}

// TestResolve_MarkdownToplevelIsTheLede pins markdown's own @toplevel: the
// content before the first heading, not the span of declarations every other
// grammar means by it.
func TestResolve_MarkdownToplevelIsTheLede(t *testing.T) {
	t.Parallel()

	const src = "Intro text.\n\n# Install\n\nBody.\n"
	markdown := langForExt(t, ".md")

	res, err := Resolve(markdown, []byte(src), "@toplevel")
	if err != nil {
		t.Fatalf("Resolve(@toplevel) = %v", err)
	}
	if got, want := string(srcAt([]byte(src), res.Extent)), "Intro text.\n\n"; got != want {
		t.Errorf("Resolve(@toplevel) extent = %q; want the lede %q", got, want)
	}
}

// TestDeclExtents_AgreesWithResolve pins the exported table against the
// one-anchor call it is meant to be equivalent to: a caller reading extents
// off DeclExtents must get the bytes Resolve would have staged for the same
// anchor.
func TestDeclExtents_AgreesWithResolve(t *testing.T) {
	t.Parallel()

	lang := langForExt(t, ".go")
	declared, err := DeclExtents(lang, []byte(goTwoGetters))
	if err != nil {
		t.Fatalf("DeclExtents: %v", err)
	}
	order, err := DeclOrder(lang, []byte(goTwoGetters))
	if err != nil {
		t.Fatalf("DeclOrder: %v", err)
	}
	if len(declared) != len(order) {
		t.Fatalf("DeclExtents has %d entries; DeclOrder has %d", len(declared), len(order))
	}
	for i, d := range declared {
		if d.Anchor != order[i] {
			t.Errorf("DeclExtents[%d].Anchor = %q; DeclOrder[%d] = %q", i, d.Anchor, i, order[i])
		}
		res, err := Resolve(lang, []byte(goTwoGetters), d.Anchor)
		if err != nil {
			t.Errorf("Resolve(%q) = %v", d.Anchor, err)
			continue
		}
		if res.Extent != d.Extent {
			t.Errorf("Resolve(%q).Extent = %+v; DeclExtents says %+v", d.Anchor, res.Extent, d.Extent)
		}
	}
}

// TestResolveError_Error pins the message shapes callers print verbatim: the
// cross-check mismatch names both sides, and every other code reads as one
// resolution failure with its optional suggestion list.
func TestResolveError_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *ResolveError
		want string
	}{
		{
			name: "extent mismatch names both ranges",
			err:  &ResolveError{Code: exitcode.ExtentMismatch, Anchor: "A", TreeSitterRange: "L1..L3", LSPRange: "L2..L3"},
			want: `resolve: "A": language-server extent mismatch (tree-sitter L1..L3, language-server L2..L3)`,
		},
		{
			name: "unsupported language",
			err:  &ResolveError{Code: exitcode.UnsupportedLanguage, Anchor: "x.rb"},
			want: `resolve: "x.rb": unsupported language`,
		},
		{
			name: "unresolvable with no suggestions",
			err:  &ResolveError{Code: exitcode.AnchorUnresolvable, Anchor: "Nope"},
			want: `resolve: "Nope": unresolved`,
		},
		{
			name: "unresolvable with a suggestion",
			err:  &ResolveError{Code: exitcode.AnchorUnresolvable, Anchor: "sizY", Candidates: []string{"Box.sizeX"}},
			want: `resolve: "sizY": unresolved (did you mean: Box.sizeX?)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q; want %q", got, tt.want)
			}
		})
	}
}

// TestAsResolveError_ReachesWrappedErrors pins the reason this reads through
// errors.As: a caller one layer away from construction cannot assume the
// error travels unwrapped.
func TestAsResolveError_ReachesWrappedErrors(t *testing.T) {
	t.Parallel()

	inner := &ResolveError{Code: exitcode.AnchorAmbiguous, Anchor: "Get", Candidates: []string{"A.Get", "B.Get"}}
	err := wrapped{inner}
	if _, ok := AsResolveError(err); !ok {
		t.Error("AsResolveError(wrapped) = false; want true")
	}
	if _, ok := AsAmbiguous(err); !ok {
		t.Error("AsAmbiguous(wrapped) = false; want true")
	}
	if _, ok := AsResolveError(errors.New("plain")); ok {
		t.Error("AsResolveError(plain error) = true; want false")
	}
}

type wrapped struct{ err error }

func (w wrapped) Error() string { return "wrapped: " + w.err.Error() }
func (w wrapped) Unwrap() error { return w.err }

// langForExt is the adapter lookup these tests share; a missing
// registration is a wiring failure, not a resolution outcome.
func langForExt(t *testing.T, ext string) Language {
	t.Helper()
	lang, ok := ForExtension(ext)
	if !ok {
		t.Fatalf("ForExtension(%q) is not registered", ext)
	}
	return lang
}

func srcAt(src []byte, ext Extent) []byte {
	if ext.End > uint(len(src)) || ext.Start > ext.End {
		return nil
	}
	return src[ext.Start:ext.End]
}
