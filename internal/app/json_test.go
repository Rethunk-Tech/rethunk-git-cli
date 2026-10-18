package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

func TestTakeJSON(t *testing.T) {
	t.Parallel()

	rest, on := takeJSON([]string{"--json", "languages"})
	qt.Assert(t, qt.IsTrue(on))
	qt.Assert(t, qt.DeepEquals(rest, []string{"languages"}))

	rest, on = takeJSON([]string{"languages", "--json"})
	qt.Assert(t, qt.IsTrue(on))
	qt.Assert(t, qt.DeepEquals(rest, []string{"languages"}))

	rest, on = takeJSON([]string{"symbols", "--", "--json"})
	qt.Assert(t, qt.IsFalse(on))
	qt.Assert(t, qt.DeepEquals(rest, []string{"symbols", "--", "--json"}))
}

// TestRun_JSONBeforeOrAfterCommand is the case that fails when --json is
// still parsed only inside each subcommand: before the command it was an
// unknown command, not the same encoding.
func TestRun_JSONBeforeOrAfterCommand(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	before, stderr, code := runApp(t, "-C", cwd, "--json", "languages")
	qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("%s", stderr))
	after, stderr, code := runApp(t, "-C", cwd, "languages", "--json")
	qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("%s", stderr))
	qt.Assert(t, qt.Equals(before, after))
	qt.Assert(t, qt.StringContains(before, "{\n  \"schema\": 1"))

	var top map[string]json.RawMessage
	qt.Assert(t, qt.IsNil(json.Unmarshal([]byte(before), &top)))
	qt.Assert(t, qt.DeepEquals(mapKeys(top), []string{"languages", "schema"}))
}

func TestRun_JSONUnsupportedCommand(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	for _, cmd := range []string{"commit", "show", "blame", "completion"} {
		_, stderr, code := runApp(t, "-C", cwd, "--json", cmd)
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage), qt.Commentf("%s", cmd))
		qt.Assert(t, qt.StringContains(stderr, "rgit: "+cmd+" does not support --json"))
		qt.Assert(t, qt.StringContains(stderr, usageLine))
	}
}

// TestRun_DiffJSONAgreesWithPorcelain pins that --json lists the same
// FILE/SYMBOL/STATUS/ADDED/DELETED rows --porcelain prints, including an
// empty symbol, as one object. Conflicts with --porcelain, --patch, and
// --quiet are 129; --exit-code still exits 1 when committable.
func TestRun_DiffJSONAgreesWithPorcelain(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gittest.Write(t, dir, "notes.txt", "hello\n")

	porcelain, stderr, code := runApp(t, "-C", dir, "diff", "--porcelain")
	qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("%s", stderr))

	before, stderr, code := runApp(t, "-C", dir, "--json", "diff")
	qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("%s", stderr))
	after, stderr, code := runApp(t, "-C", dir, "diff", "--json")
	qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("%s", stderr))
	qt.Assert(t, qt.Equals(before, after))

	var top map[string]json.RawMessage
	qt.Assert(t, qt.IsNil(json.Unmarshal([]byte(after), &top)))
	qt.Assert(t, qt.DeepEquals(mapKeys(top), []string{"diffs", "schema"}))
	qt.Assert(t, qt.Equals(strings.TrimSpace(string(top["schema"])), "1"))
	qt.Assert(t, qt.StringContains(after, "{\n  \"schema\": 1"))

	var rows []map[string]string
	qt.Assert(t, qt.IsNil(json.Unmarshal(top["diffs"], &rows)))
	lines := strings.Split(strings.TrimRight(porcelain, "\n"), "\n")
	qt.Assert(t, qt.Equals(len(rows), len(lines)))
	sawEmptySymbol := false
	for i, line := range lines {
		fields := strings.Split(line, "\t")
		qt.Assert(t, qt.Equals(len(fields), 5))
		qt.Assert(t, qt.DeepEquals(mapKeys(rows[i]), []string{"added", "deleted", "file", "status", "symbol"}))
		qt.Assert(t, qt.Equals(rows[i]["file"], fields[0]))
		qt.Assert(t, qt.Equals(rows[i]["symbol"], fields[1]))
		qt.Assert(t, qt.Equals(rows[i]["status"], fields[2]))
		qt.Assert(t, qt.Equals(rows[i]["added"], fields[3]))
		qt.Assert(t, qt.Equals(rows[i]["deleted"], fields[4]))
		if fields[1] == "" {
			sawEmptySymbol = true
			qt.Assert(t, qt.Equals(rows[i]["symbol"], ""))
		}
	}
	qt.Assert(t, qt.IsTrue(sawEmptySymbol))

	stdout, _, code := runApp(t, "-C", dir, "diff", "--json", "--exit-code")
	qt.Assert(t, qt.Equals(code, exitcode.Code(1)))
	qt.Assert(t, qt.Equals(stdout, after))

	for _, args := range [][]string{
		{"diff", "--json", "--porcelain"},
		{"diff", "--porcelain", "--json"},
		{"diff", "--json", "--patch"},
		{"diff", "--json", "--quiet"},
	} {
		_, stderr, code = runApp(t, append([]string{"-C", dir}, args...)...)
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage), qt.Commentf("%v\n%s", args, stderr))
		qt.Assert(t, qt.StringContains(stderr, "mutually exclusive"))
	}
}
