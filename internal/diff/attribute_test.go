package diff

import (
	"errors"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// fakeDeclResolver is the double resolveRegions' own doc comment names: no
// real resolve.Language can make DeclOrder emit a name Resolve then
// rejects (the invariant is enforced inside resolve's own buildIndex,
// independent of any adapter), so this is the only way to exercise that
// branch at all. It implements exactly declResolver, nothing more --
// var _ declResolver = (*resolve.File)(nil) beside the interface is what
// would catch it drifting from the real type's signatures.
type fakeDeclResolver struct {
	names []string
	err   error
}

func (f *fakeDeclResolver) DeclOrder() []string { return f.names }

func (f *fakeDeclResolver) Resolve(anchor string) (*resolve.Resolution, error) {
	return nil, f.err
}

// TestResolveRegions_DeclOrderNameRejectedByResolveIsAnError guards against
// treating this condition as a legitimate-input edge case: it is an
// internal inconsistency, so it must surface as a loud error rather than
// silently shrinking the region set.
func TestResolveRegions_DeclOrderNameRejectedByResolveIsAnError(t *testing.T) {
	t.Parallel()
	lang, ok := resolve.ForExtension(".go")
	if !ok {
		t.Fatal("resolve: no adapter registered for .go")
	}

	fake := &fakeDeclResolver{
		names: []string{"Foo"},
		err:   errors.New("boom"),
	}

	_, err := resolveRegions(lang, nil, fake)
	if err == nil {
		t.Fatal("resolveRegions: want an error when Resolve rejects a DeclOrder name, got nil")
	}
	if !strings.Contains(err.Error(), "Foo") || !strings.Contains(err.Error(), "internal inconsistency") {
		t.Errorf("resolveRegions error = %q; want it to name the anchor and call out the inconsistency", err.Error())
	}
}

func TestTopLevelComma(t *testing.T) {
	t.Parallel()
	// want is the byte index of the separating comma, or -1 for none. The
	// commas inside strings, calls, and brackets below are all decoys.
	tests := []struct {
		input string
		want  int
	}{
		{"const a = 1, b = 2", 11},
		{"const a = \"hello, world\", b = 2", 24},
		{"const a = `hello, world`, b = 2", 24},
		{"const a = 'hello, world', b = 2", 24},
		{"const a = \"hello\\\"\", b = 2", 19},
		{"const a = fn(1, 2), b = 3", 18},
		{"const a = [1, 2], b = 3", 16},
		{"const a = {x: 1, y: 2}, b = 3", 22},
		{"const a = 1", -1},
	}

	for _, tt := range tests {
		got := topLevelComma([]byte(tt.input))
		if got != tt.want {
			t.Errorf("topLevelComma(%q) = %d; want %d", tt.input, got, tt.want)
		}
	}
}

// TestAttributeSymbols_GoInlineMultiNameConstFallsBackUnanchorable guards
// the diff-layer half of lang_go.go's goSpecNameDeclarations fix: "a" and
// "b" in "const a, b = 1, 2" now both resolve, sharing one extent, since
// isMultiDeclaratorLang (attribute.go) now includes "go". exclusiveText's
// own containment check treats two regions with the identical extent as
// fully nested in each other, so both empty out rather than double-counting
// the line or mis-attributing the change to whichever name sorts first --
// the change must fall to (unanchorable), honoring this package's "every
// row sums to the file's true total" invariant, and neither "a" nor "b" may
// carry a row of their own.
func TestAttributeSymbols_GoInlineMultiNameConstFallsBackUnanchorable(t *testing.T) {
	t.Parallel()
	lang, ok := resolve.ForExtension(".go")
	if !ok {
		t.Fatal("resolve: no adapter registered for .go")
	}

	oldSrc := []byte("package p\n\nconst a, b = 1, 2\n")
	newSrc := []byte("package p\n\nconst a, b = 1, 3\n")

	rows, err := attributeSymbols(lang, oldSrc, newSrc, 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 1 || rows[0].Status != StatusUnanchorable {
		t.Fatalf("rows = %+v; want exactly one UNANCHORABLE row", rows)
	}
	if rows[0].Added != "1" || rows[0].Deleted != "1" {
		t.Errorf("UNANCHORABLE row = %+v; want Added=1 Deleted=1", rows[0])
	}
}

func TestNarrowMultiDeclarator(t *testing.T) {
	t.Parallel()
	src := []byte("const a = 1, b = 2")
	ext := resolve.Extent{Start: 0, End: uint(len(src))}
	got := narrowMultiDeclarator(src, ext)
	if got.Start != 0 || got.End != 11 {
		t.Errorf("narrowMultiDeclarator got range [%d, %d]; want [0, 11]", got.Start, got.End)
	}
}

// TestAttributeSymbols_MarkdownSectionOwnParagraphSurvivesNestedSetext
// guards against a misattribution: a Markdown section containing a setext
// heading (which never opens its own nested section, lang_markdown.go's
// sectionDeclarations) must not vanish from the region set just because
// dropSpanningRegions sees it "span" the setext heading's own tiny
// declaration -- editing the section's own paragraph, untouched by the
// setext heading itself, must attribute to the enclosing section, not fall
// all the way to (unanchorable).
func TestAttributeSymbols_MarkdownSectionOwnParagraphSurvivesNestedSetext(t *testing.T) {
	t.Parallel()
	lang, ok := resolve.ForExtension(".md")
	if !ok {
		t.Fatal("resolve: no adapter registered for .md")
	}

	// Two "## Options" under one "# Usage": the second's own body is a
	// paragraph, then a setext heading, then another paragraph.
	oldSrc := []byte("# Usage\n\n## Options\n\nFirst options.\n\n## Options\n\n" +
		"Second options paragraph.\n\nSetext Title\n============\n\nBody after.\n")
	newSrc := []byte("# Usage\n\n## Options\n\nFirst options.\n\n## Options\n\n" +
		"Second options paragraph, edited.\n\nSetext Title\n============\n\nBody after.\n")

	rows, err := attributeSymbols(lang, oldSrc, newSrc, 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	var found *Row
	for i := range rows {
		if rows[i].Symbol == "usage.options#2" {
			found = &rows[i]
		}
		if rows[i].Status == StatusUnanchorable {
			t.Errorf("editing the section's own paragraph must not fall back to (unanchorable): %+v", rows[i])
		}
	}
	if found == nil {
		t.Fatalf("want a row for usage.options#2, got %+v", rows)
	}
	if found.Added != "1" || found.Deleted != "1" {
		t.Errorf("usage.options#2 row = %+v; want Added=1 Deleted=1", found)
	}
}

// attributeSymbols opens both sides itself, for a test with nothing already
// parsed to hand in. Production always has a *resolve.File open already and
// calls attributeSymbolsOpen directly.
func attributeSymbols(lang resolve.Language, oldSrc, newSrc []byte, totalAdded, totalDeleted int) ([]Row, error) {
	oldFile, err := resolve.Open(lang, oldSrc)
	if err != nil {
		return nil, err
	}
	defer oldFile.Close()
	newFile, err := resolve.Open(lang, newSrc)
	if err != nil {
		return nil, err
	}
	defer newFile.Close()
	rows, _, err := attributeSymbolsOpen(lang, oldSrc, newSrc, oldFile, newFile, totalAdded, totalDeleted)
	return rows, err
}
