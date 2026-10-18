package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

func TestRun_DoctorHelpAndUsage(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	for _, arg := range []string{"--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, code := runApp(t, "-C", cwd, "doctor", arg)
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			qt.Assert(t, qt.Equals(stdout, doctorHelp))
			qt.Assert(t, qt.Equals(stderr, ""))
		})
	}

	stdout, stderr, code := runApp(t, "-C", cwd, "doctor", "extra")
	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.Equals(stdout, ""))
	wantUsage := "rgit: doctor: unrecognized argument \"extra\"\n" + doctorHelp
	qt.Assert(t, qt.Equals(stderr, wantUsage))
}

// TestRun_DoctorJSON pins that --json emits one object whose checks are the
// same rows --porcelain prints, including every check runEnvironmentChecks
// contributes, and that the two output modes refuse to combine.
func TestRun_DoctorJSON(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	stdout, stderr, code := runApp(t, "-C", cwd, "doctor", "--json")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "Environment:")))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "Grammars compiled in")))

	var top map[string]json.RawMessage
	qt.Assert(t, qt.IsNil(json.Unmarshal([]byte(stdout), &top)))
	qt.Assert(t, qt.DeepEquals(mapKeys(top), []string{"checks", "schema"}))
	qt.Assert(t, qt.Equals(strings.TrimSpace(string(top["schema"])), "1"))
	qt.Assert(t, qt.StringContains(stdout, "{\n  \"schema\": 1"))

	var checks []map[string]string
	qt.Assert(t, qt.IsNil(json.Unmarshal(top["checks"], &checks)))

	porcelain, _, pcode := runApp(t, "-C", cwd, "doctor", "--porcelain")
	qt.Assert(t, qt.Equals(pcode, exitcode.Success))
	rows := strings.Split(strings.TrimRight(porcelain, "\n"), "\n")
	qt.Assert(t, qt.Equals(len(checks), len(rows)))
	for i, line := range rows {
		fields := strings.Split(line, "\t")
		qt.Assert(t, qt.Equals(len(fields), 4))
		qt.Assert(t, qt.DeepEquals(mapKeys(checks[i]), []string{"detail", "kind", "name", "status"}))
		qt.Assert(t, qt.Equals(checks[i]["kind"], fields[0]))
		qt.Assert(t, qt.Equals(checks[i]["name"], fields[1]))
		qt.Assert(t, qt.Equals(checks[i]["status"], fields[2]))
		qt.Assert(t, qt.Equals(checks[i]["detail"], fields[3]))
	}

	envChecks, _ := runEnvironmentChecks(context.Background())
	for _, want := range envChecks {
		status := "ok"
		if !want.OK {
			status = "MISSING"
		}
		found := false
		for _, got := range checks {
			if got["kind"] == "env" && got["name"] == want.Name {
				qt.Assert(t, qt.Equals(got["status"], status))
				qt.Assert(t, qt.Equals(got["detail"], want.Detail))
				found = true
			}
		}
		qt.Assert(t, qt.IsTrue(found), qt.Commentf("missing env check %q", want.Name))
	}

	for _, args := range [][]string{
		{"-C", cwd, "doctor", "--json", "--porcelain"},
		{"-C", cwd, "doctor", "--porcelain", "--json"},
	} {
		_, stderr, code = runApp(t, args...)
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "--porcelain and --json are mutually exclusive"))
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
