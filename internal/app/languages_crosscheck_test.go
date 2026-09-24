package app

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

func TestRun_LanguagesHelpEquality(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	for _, arg := range []string{"--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, code := runApp(t, "-C", cwd, "languages", arg)
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			qt.Assert(t, qt.Equals(stdout, languagesHelp))
			qt.Assert(t, qt.Equals(stderr, ""))
		})
	}
}

func TestRun_LanguagesPorcelainCrossCheckColumn(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	stdout, stderr, code := runApp(t, "-C", cwd, "languages", "--porcelain")
	qt.Assert(t, qt.Equals(code, 0))
	qt.Assert(t, qt.Equals(stderr, ""))

	want := map[string]string{
		"css":        "wired",
		"go":         "wired",
		"html":       "wired",
		"json":       "wired",
		"markdown":   "wired",
		"python":     "wired",
		"rust":       "wired",
		"shell":      "wired",
		"sql":        "ts-only",
		"toml":       "ts-only",
		"tsx":        "wired",
		"typescript": "wired",
		"yaml":       "wired",
	}
	compiled := map[string]bool{}
	for _, language := range resolve.Languages() {
		compiled[language.Name] = true
	}

	seen := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimRight(stdout, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		qt.Assert(t, qt.Equals(len(fields), 4))
		expected, ok := want[fields[0]]
		if !ok {
			t.Fatalf("unexpected language row %q", fields[0])
		}
		qt.Assert(t, qt.Equals(fields[3], expected))
		seen[fields[0]] = true
	}

	for name := range compiled {
		qt.Assert(t, qt.IsTrue(seen[name]))
	}
}
