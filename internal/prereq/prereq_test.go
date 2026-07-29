package prereq

import (
	"bytes"
	"testing"

	qt "github.com/go-quicktest/qt"
)

func TestLookPath(t *testing.T) {
	t.Parallel()

	t.Run("found reports the resolved path", func(t *testing.T) {
		t.Parallel()
		// git is this repo's own hard requirement (CONTRIBUTING.md,
		// internal/gittest shells out to it already), so it is always on
		// PATH in a test environment -- the real dependency, not a double.
		c := LookPath("git", "git", "")
		qt.Assert(t, qt.Equals(c.Name, "git"))
		qt.Assert(t, qt.IsTrue(c.OK))
		qt.Assert(t, qt.Not(qt.Equals(c.Detail, "")))
	})

	t.Run("missing carries the caller's own note", func(t *testing.T) {
		t.Parallel()
		c := LookPath("nonexistent-tool", "rgit-prereq-test-does-not-exist", "optional -- see docs")
		qt.Assert(t, qt.IsFalse(c.OK))
		qt.Assert(t, qt.Equals(c.Detail, "optional -- see docs"))
	})

	// doctor's and cmd/rgit-install's git checks report an empty detail on
	// failure (no note at all); a LookPath that silently substituted
	// something else here would change both commands' output.
	t.Run("missing with no note reports an empty detail", func(t *testing.T) {
		t.Parallel()
		c := LookPath("nonexistent-tool", "rgit-prereq-test-does-not-exist", "")
		qt.Assert(t, qt.IsFalse(c.OK))
		qt.Assert(t, qt.Equals(c.Detail, ""))
	})
}

// TestPrint pins both alignment rules at once, since either one alone still
// leaves a jagged column: the status field is padded so [ok] and [MISSING]
// start the name at the same column, and the name field is padded to the
// width Width reported for the whole group rather than a constant a long
// language-server label overruns.
func TestPrint(t *testing.T) {
	t.Parallel()

	group := []Check{
		{Name: "git", OK: true, Detail: "/usr/bin/git"},
		{Name: "tree-sitter CLI", OK: false, Detail: "optional"},
		{Name: "vscode-json-language-server (json)", OK: true, Detail: "/home/u/.bun/bin/vscode-json-language-server"},
	}
	width := Width(group...)

	var buf bytes.Buffer
	for _, c := range group {
		Print(&buf, width, c)
	}

	want := "" +
		"  [ok]      git                                /usr/bin/git\n" +
		"  [MISSING] tree-sitter CLI                    optional\n" +
		"  [ok]      vscode-json-language-server (json) /home/u/.bun/bin/vscode-json-language-server\n"
	qt.Assert(t, qt.Equals(buf.String(), want))
}

// TestWidth_FloorKeepsShortGroupsFromCollapsing guards cmd/rgit-install's
// own output, whose five names are all short: without a floor the detail
// column would slide left to hug them, churning a layout that already reads
// well.
func TestWidth_FloorKeepsShortGroupsFromCollapsing(t *testing.T) {
	t.Parallel()

	qt.Assert(t, qt.Equals(Width(Check{Name: "go"}, Check{Name: "git"}), minNameWidth))
	qt.Assert(t, qt.Equals(Width(), minNameWidth))

	long := Check{Name: "vscode-json-language-server (json)"}
	qt.Assert(t, qt.Equals(Width(long), len(long.Name)))
}
