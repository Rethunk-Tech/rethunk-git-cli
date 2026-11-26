package app

import (
	"encoding/json"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/prereq"
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
	wantUsage := "rgit: doctor: unrecognized argument \"extra\"\n" + usageHint(doctorHelp)
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
	assertPorcelainRows(t, porcelain, checks, "kind", "name", "status", "detail")

	envChecks, _ := runEnvironmentChecks(t.Context())
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

// assertPorcelainRows checks that each porcelain line carries cols, in order,
// as the values of the matching JSON row, and that each row has exactly those keys.
func assertPorcelainRows(t *testing.T, porcelain string, rows []map[string]string, cols ...string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(porcelain, "\n"), "\n")
	qt.Assert(t, qt.Equals(len(rows), len(lines)))
	want := slices.Sorted(slices.Values(cols))
	for i, line := range lines {
		fields := strings.Split(line, "\t")
		qt.Assert(t, qt.Equals(len(fields), len(cols)))
		qt.Assert(t, qt.DeepEquals(mapKeys(rows[i]), want))
		for j, col := range cols {
			qt.Assert(t, qt.Equals(rows[i][col], fields[j]))
		}
	}
}

func mapKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// TestRunEnvironmentChecksNamesLandTools pins that doctor reports the tools rgit land shells out to.
func TestRunEnvironmentChecksNamesLandTools(t *testing.T) {
	t.Parallel()
	checks, _ := runEnvironmentChecks(t.Context())
	var names []string
	for _, c := range checks {
		names = append(names, c.Name)
	}
	for _, want := range []string{"gate", "gh"} {
		if !slices.Contains(names, want) {
			t.Errorf("checks %v lack %q", names, want)
		}
	}
}

// TestWithGHAuth pins that doctor says whether gh holds a login, without changing the check's OK.
func TestWithGHAuth(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, bin, want string
	}{
		{"logged in", "true", "(authenticated)"},
		{"logged out", "false", "(not authenticated"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			// A shim written here can hit ETXTBSY when a parallel test forks while its fd is open.
			gh, err := exec.LookPath(c.bin)
			if err != nil {
				t.Skipf("no %s on PATH", c.bin)
			}
			got := withGHAuth(t.Context(), prereq.Check{Name: "gh", OK: true, Detail: gh})
			if !got.OK || !strings.Contains(got.Detail, c.want) {
				t.Errorf("check = %+v; want OK with %q", got, c.want)
			}
		})
	}
}
