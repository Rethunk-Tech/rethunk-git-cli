// No t.Parallel here: every case changes directory (app_test.go's own
// package comment explains why), which t.Chdir forbids combining with it.
package app

import (
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsptest"
)

func TestRun_ContextHelpAndUsage(t *testing.T) {
	t.Run("--help", func(t *testing.T) {
		t.Chdir(t.TempDir())
		stdout, stderr, code := runApp(t, "context", "--help")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stdout, contextHelp))
		qt.Assert(t, qt.Equals(stderr, ""))
	})

	// docs/USAGE.md § Context's own guardrail: "a command with options
	// becomes git status with extra steps" -- context takes no flags or
	// targets beyond --help, so any argument at all is refused rather than
	// quietly growing a flag surface.
	t.Run("any argument is refused", func(t *testing.T) {
		chdirTempRepo(t)
		for _, args := range [][]string{
			{"context", "extra"},
			{"context", "--porcelain"},
			{"context", "a.go"},
		} {
			_, stderr, code := runApp(t, args...)
			qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
			// n8: context and doctor now share refuseExtraArgs (shared.go),
			// unifying on doctor's own "unrecognized argument %q" wording.
			qt.Assert(t, qt.StringContains(stderr, "unrecognized argument"))
		}
	})
}

// TestRun_ContextEmptyRepoEmitsNothing pins the empty case: a fresh
// repository with no commits and nothing to report emits no records at
// all and nothing on stderr -- no live language server is ever consulted
// when there is nothing to cross-check.
func TestRun_ContextEmptyRepoEmitsNothing(t *testing.T) {
	dir, _ := gittest.New(t)
	t.Chdir(dir)

	stdout, stderr, code := runApp(t, "context")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stdout, ""))
	qt.Assert(t, qt.Equals(stderr, ""))
}

// TestRun_ContextEmitsDiffRowsBeforeCommits is the token case
// specs/design.md § Commands accepted this command against: one
// invocation reports recent commit subjects AND the same per-symbol
// diffstat `rgit diff` itself reports, as one stream, diff rows first --
// on a busy branch the unbounded, actionable F rows must survive budget
// truncation before the cheap, bounded C rows do (docs/CODES.md#output-records).
func TestRun_ContextEmitsDiffRowsBeforeCommits(t *testing.T) {
	dir := chdirTempRepo(t) // "chore: initial" commits a.go with A and B
	t.Setenv("PATH", isolatedPATHWithGopls(t))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(t.TempDir(), "missing-runtime"))

	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	writeAppFile(t, dir, "new.txt", "untracked content\n")

	stdout, stderr, code := runApp(t, "context")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	qt.Assert(t, qt.StringContains(stdout, "W\tts-only\n"))
	qt.Assert(t, qt.StringContains(stderr, tsOnlyNotice))
	qt.Assert(t, qt.StringContains(stdout, "chore: initial"))
	qt.Assert(t, qt.StringContains(stdout, "F\ta.go\tA\tMOD\t"))
	qt.Assert(t, qt.StringContains(stdout, "new.txt"))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "X\tTRUNCATED")))

	commitIdx := strings.Index(stdout, "C\t")
	diffIdx := strings.Index(stdout, "F\t")
	qt.Assert(t, qt.IsTrue(commitIdx >= 0))
	qt.Assert(t, qt.IsTrue(diffIdx >= 0))
	qt.Assert(t, qt.IsTrue(diffIdx < commitIdx))
}

// TestRun_ContextEmitsWarningRecords pins report.Warnings' machine form:
// the warning body follows the W tag without the human [warning] prefix, and
// the same body remains mirrored on stderr.
func TestRun_ContextEmitsWarningRecords(t *testing.T) {
	dir := chdirTempRepo(t)
	t.Setenv("PATH", isolatedPATHWithGopls(t))
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	sockPath := filepath.Join(t.TempDir(), "gopls.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	const resultJSON = `[
		{"name":"A","kind":12,"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"selectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}},
		{"name":"B","kind":12,"range":{"start":{"line":7,"character":0},"end":{"line":9,"character":1}},"selectionRange":{"start":{"line":7,"character":0},"end":{"line":7,"character":5}}}
	]`
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func() { _ = lsptest.ServeMockLSP(conn, resultJSON, lsptest.MockServerHooks{}) }()
		}
	}()
	t.Setenv("RGIT_LSP_SOCKET", sockPath)

	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	stdout, stderr, code := runApp(t, "context")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	warning := `a.go: resolve: "A":`
	qt.Assert(t, qt.StringContains(stdout, "W\twarning\t"+warning))
	qt.Assert(t, qt.StringContains(stderr, "[warning] "+warning))
	warningIdx := strings.Index(stdout, "W\twarning\t")
	diffIdx := strings.Index(stdout, "F\t")
	qt.Assert(t, qt.IsTrue(warningIdx >= 0))
	qt.Assert(t, qt.IsTrue(diffIdx >= 0))
	qt.Assert(t, qt.IsTrue(warningIdx < diffIdx))
}

// TestRun_ContextRecordsAreTabSeparatedWithExpectedFieldCounts pins the
// record shapes docs/CODES.md commits to: B has 5 fields, C has 3, F has 6
// (the same 5 rgit diff --porcelain emits, plus the leading type tag), W has
// either 2 or 3, and none of them ever carries a header.
func TestRun_ContextRecordsAreTabSeparatedWithExpectedFieldCounts(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	stdout, _, code := runApp(t, "context")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	sawBranch := false
	for line := range strings.SplitSeq(strings.TrimRight(stdout, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		switch fields[0] {
		case "B":
			sawBranch = true
			qt.Assert(t, qt.Equals(len(fields), 5))
		case "C":
			qt.Assert(t, qt.Equals(len(fields), 3))
		case "F":
			qt.Assert(t, qt.Equals(len(fields), 6))
		case "W":
			qt.Assert(t, qt.IsTrue(len(fields) == 2 || len(fields) == 3))
		case "X":
			qt.Assert(t, qt.Equals(len(fields), 3))
		default:
			t.Fatalf("unrecognized record type %q in line %q", fields[0], line)
		}
	}
	qt.Assert(t, qt.IsTrue(sawBranch))
}

// TestRun_ContextBranchRecordSortsFirstAndReportsNoUpstream pins the B
// record's own content and position: it names the current branch, carries
// an empty UPSTREAM and 0/0 AHEAD/BEHIND when none is configured
// (chdirTempRepo never pushes anywhere), and sorts before every F and C
// record -- a single, cheap record ahead of the two budget-competing halves.
func TestRun_ContextBranchRecordSortsFirstAndReportsNoUpstream(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "new.txt", "untracked content\n")

	stdout, _, code := runApp(t, "context")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	qt.Assert(t, qt.IsTrue(len(lines) > 0))
	qt.Assert(t, qt.StringContains(lines[0], "B\t"))
	qt.Assert(t, qt.StringContains(lines[0], "\t\t0\t0"))

	branch := strings.TrimSpace(gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD"))
	qt.Assert(t, qt.StringContains(lines[0], "B\t"+branch+"\t"))
}

// TestBuildContextStream unit-tests the byte-budget truncation boundary
// directly against a tiny budget, rather than building a repository large
// enough to exceed the real 16 KiB one (specs/design.md § Commands).
func TestBuildContextStream(t *testing.T) {
	t.Parallel()

	t.Run("everything fits, no marker", func(t *testing.T) {
		records := []string{"C\th1\tsubject one\n", "F\ta.go\tA\tMOD\t1\t0\n"}
		got := buildContextStream(records, 4096)
		qt.Assert(t, qt.Equals(got, records[0]+records[1]))
	})

	t.Run("budget exceeded keeps only what fits and appends one marker", func(t *testing.T) {
		records := []string{"12345\n", "12345\n", "12345\n", "12345\n", "12345\n"} // 6 bytes each, 30 total
		got := buildContextStream(records, 26)                                     // room for two records plus the X line, not three
		qt.Assert(t, qt.Equals(got, "12345\n12345\nX\tTRUNCATED\t3\n"))
	})

	// TestBuildContextStream/"the X record itself never pushes the stream
	// past budget" is the regression pin: the trailing X record used to be
	// appended unconditionally after the budget-bounded loop, so its own
	// bytes could push the total past budget -- breaking the "capped at 16
	// KiB" contract runContext's own help text states as a hard limit. A
	// budget just past what the record content alone needs, but too tight
	// to also fit the marker alongside any of it, must still shed every
	// record rather than let the marker overrun budget.
	t.Run("the X record itself never pushes the stream past budget", func(t *testing.T) {
		records := []string{"12345\n", "12345\n", "12345\n"} // 6 bytes each, 18 total
		const budget = 17                                    // less than the 18-byte total; the old code appended the
		// X line unconditionally here and overran budget by nearly 2x (26
		// bytes for a 17-byte budget)
		got := buildContextStream(records, budget)
		qt.Assert(t, qt.IsTrue(len(got) <= budget))
		qt.Assert(t, qt.Equals(got, "X\tTRUNCATED\t3\n"))
	})

	t.Run("no records is the empty string", func(t *testing.T) {
		qt.Assert(t, qt.Equals(buildContextStream(nil, 4096), ""))
	})

	// buildContextStream itself is order-agnostic -- it keeps a prefix and
	// drops a suffix regardless of record type. Priority between F and C
	// rows is runContext's own build order (F rows first), pinned by
	// TestRun_ContextEmitsDiffRowsBeforeCommits; this fixture proves the
	// truncation mechanics honor whatever order it hands in.
	t.Run("truncation drops from the end regardless of record type", func(t *testing.T) {
		records := []string{"F\ta.go\tA\tMOD\t1\t0\n", "C\th1\tsubject one\n", "C\th2\tsubject two\n"}
		got := buildContextStream(records, 40) // room for the F row and the marker, not either C row
		qt.Assert(t, qt.StringContains(got, "F\ta.go\tA\tMOD\t1\t0\n"))
		qt.Assert(t, qt.StringContains(got, "X\tTRUNCATED\t2\n"))
		qt.Assert(t, qt.Not(qt.StringContains(got, "subject one")))
		qt.Assert(t, qt.Not(qt.StringContains(got, "subject two")))
	})

	t.Run("W diagnostics consume budget before F rows", func(t *testing.T) {
		records := []string{
			"B\tmain\t\t0\t0\n",
			"W\tts-only\n",
			"F\ta.go\tA\tMOD\t1\t0\n",
			"F\ta.go\tB\tMOD\t1\t0\n",
			"F\ta.go\tC\tMOD\t1\t0\n",
			"F\ta.go\tD\tMOD\t1\t0\n",
			"C\th1\tsubject one\n",
			"C\th2\tsubject two\n",
		}
		markerBytes := len("X\tTRUNCATED\t0\n")
		budget := len(records[0]) + len(records[2]) + len(records[3]) + markerBytes

		got := buildContextStream(records, budget)
		want := records[0] + records[1] + records[2] + "X\tTRUNCATED\t5\n"
		qt.Assert(t, qt.Equals(got, want))

		withoutW := append([]string{records[0]}, records[2:]...)
		withoutWGot := buildContextStream(withoutW, budget)
		qt.Assert(t, qt.StringContains(withoutWGot, records[3]))
	})
}
