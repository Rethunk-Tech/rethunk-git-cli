package diff

import (
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

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
