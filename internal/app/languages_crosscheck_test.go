package app

import (
	"encoding/json"
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
		"json5":      "ts-only",
		"jsonc":      "wired",
		"markdown":   "wired",
		"python":     "wired",
		"rust":       "wired",
		"scss":       "wired",
		"shell":      "wired",
		"sql":        "ts-only",
		"toml":       "ts-only",
		"tsx":        "wired",
		"typescript": "wired",
		"yaml":       "wired",
		"zsh":        "wired",
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

// TestRun_LanguagesJSON pins that --json emits one object whose grammars are
// the same rows --porcelain prints, and that the two output modes refuse to
// combine.
func TestRun_LanguagesJSON(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	stdout, stderr, code := runApp(t, "-C", cwd, "languages", "--json")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))

	var top map[string]json.RawMessage
	qt.Assert(t, qt.IsNil(json.Unmarshal([]byte(stdout), &top)))
	qt.Assert(t, qt.DeepEquals(mapKeys(top), []string{"languages"}))

	var rows []map[string]string
	qt.Assert(t, qt.IsNil(json.Unmarshal(top["languages"], &rows)))

	porcelain, _, pcode := runApp(t, "-C", cwd, "languages", "--porcelain")
	qt.Assert(t, qt.Equals(pcode, exitcode.Success))
	lines := strings.Split(strings.TrimRight(porcelain, "\n"), "\n")
	qt.Assert(t, qt.Equals(len(rows), len(lines)))
	for i, line := range lines {
		fields := strings.Split(line, "\t")
		qt.Assert(t, qt.Equals(len(fields), 4))
		qt.Assert(t, qt.DeepEquals(mapKeys(rows[i]), []string{"cross-check", "extensions", "gated", "name"}))
		qt.Assert(t, qt.Equals(rows[i]["name"], fields[0]))
		qt.Assert(t, qt.Equals(rows[i]["extensions"], fields[1]))
		qt.Assert(t, qt.Equals(rows[i]["gated"], fields[2]))
		qt.Assert(t, qt.Equals(rows[i]["cross-check"], fields[3]))
	}

	for _, args := range [][]string{
		{"-C", cwd, "languages", "--json", "--porcelain"},
		{"-C", cwd, "languages", "--porcelain", "--json"},
	} {
		_, stderr, code = runApp(t, args...)
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "--porcelain and --json are mutually exclusive"))
	}
}
