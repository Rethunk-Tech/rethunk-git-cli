package resolve

import "testing"

// TestIsStructuredData pins the guard `rgit commit FILE:SYMBOL` applies before
// staging: the grammars whose spliced extent may disagree with their own
// parser answer true, and every language a symbol anchor is safe in answers
// false. A false negative here is silent -- the malformed blob lands in HEAD
// looking like a normal commit.
func TestIsStructuredData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ext  string
		want bool
	}{
		{".json", true},
		{".jsonc", true},
		{".json5", true},
		{".yaml", true},
		{".yml", true},
		{".toml", true},
		{".go", false},
		{".ts", false},
		{".py", false},
		{".md", false},
		{".rs", false},
	}
	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			t.Parallel()
			lang, ok := ForExtension(tt.ext)
			if !ok {
				t.Fatalf("ForExtension(%q) is not registered", tt.ext)
			}
			if got := IsStructuredData(lang); got != tt.want {
				t.Errorf("IsStructuredData(%s) = %v; want %v", tt.ext, got, tt.want)
			}
		})
	}
}

// TestGatedTag_UnknownExtension reports no gate for a language that has no
// build tag at all, so the caller prints "unsupported" rather than sending the
// user off to rebuild with a tag that does not exist.
func TestGatedTag_UnknownExtension(t *testing.T) {
	t.Parallel()

	if tag, ok := GatedTag(".rb"); ok {
		t.Errorf("GatedTag(\".rb\") = (%q, true); want ok=false", tag)
	}
}
