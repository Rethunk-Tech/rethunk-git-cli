package app

import (
	"encoding/json"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
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

	for _, cmd := range []string{"diff", "commit", "show", "blame", "completion"} {
		_, stderr, code := runApp(t, "-C", cwd, "--json", cmd)
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage), qt.Commentf("%s", cmd))
		qt.Assert(t, qt.StringContains(stderr, "rgit: "+cmd+" does not support --json"))
		qt.Assert(t, qt.StringContains(stderr, usageLine))
	}
}
