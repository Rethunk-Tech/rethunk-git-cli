package app

import (
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/resolve"
)

func TestRenderLanguages_NoTrailingWhitespace(t *testing.T) {
	t.Parallel()
	out := renderLanguages(resolve.Languages())
	for line := range strings.SplitSeq(out, "\n") {
		if line != strings.TrimRight(line, " \t") {
			t.Errorf("line ends in whitespace: %q", line)
		}
	}
}
