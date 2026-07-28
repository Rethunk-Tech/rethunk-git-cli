package diff

import (
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

func TestTopLevelComma(t *testing.T) {
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
	src := []byte("const a = 1, b = 2")
	ext := resolve.Extent{Start: 0, End: uint(len(src))}
	got := narrowMultiDeclarator(src, ext)
	if got.Start != 0 || got.End != 11 {
		t.Errorf("narrowMultiDeclarator got range [%d, %d]; want [0, 11]", got.Start, got.End)
	}
}
