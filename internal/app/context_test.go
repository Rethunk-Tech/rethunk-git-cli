package app

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

func TestRun_ContextHelpAndUsage(t *testing.T) {
	t.Run("--help", func(t *testing.T) {
		t.Chdir(t.TempDir())
		stdout, _, code := runApp(t, "context", "--help")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "usage: rgit context"))
	})

	// TODO.md's own guardrail: "a command with options becomes git status
	// with extra steps" -- context takes no flags or targets beyond
	// --help, so any argument at all is refused rather than quietly
	// growing a flag surface.
	t.Run("any argument is refused", func(t *testing.T) {
		chdirTempRepo(t)
		for _, args := range [][]string{
			{"context", "extra"},
			{"context", "--porcelain"},
			{"context", "a.go"},
		} {
			_, stderr, code := runApp(t, args...)
			qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
			qt.Assert(t, qt.StringContains(stderr, "takes no arguments"))
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

// TestRun_ContextEmitsCommitsBeforeDiffRows is the token case TODO.md
// accepted this command against: one invocation reports recent commit
// subjects AND the same per-symbol diffstat `rgit diff` itself reports,
// as one stream, commits first.
func TestRun_ContextEmitsCommitsBeforeDiffRows(t *testing.T) {
	dir := chdirTempRepo(t) // "chore: initial" commits a.go with A and B

	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	writeAppFile(t, dir, "new.txt", "untracked content\n")

	stdout, _, code := runApp(t, "context")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	qt.Assert(t, qt.StringContains(stdout, "chore: initial"))
	qt.Assert(t, qt.StringContains(stdout, "F\ta.go\tA\tMOD\t"))
	qt.Assert(t, qt.StringContains(stdout, "new.txt"))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "X\tTRUNCATED")))

	commitIdx := strings.Index(stdout, "C\t")
	diffIdx := strings.Index(stdout, "F\t")
	qt.Assert(t, qt.IsTrue(commitIdx >= 0))
	qt.Assert(t, qt.IsTrue(diffIdx >= 0))
	qt.Assert(t, qt.IsTrue(commitIdx < diffIdx))
}

// TestRun_ContextRecordsAreTabSeparatedWithExpectedFieldCounts pins the
// three record shapes docs/CODES.md commits to: C has 3 fields, F has 6
// (the same 5 rgit diff --porcelain emits, plus the leading type tag),
// and neither ever carries a header.
func TestRun_ContextRecordsAreTabSeparatedWithExpectedFieldCounts(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	stdout, _, code := runApp(t, "context")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	for line := range strings.SplitSeq(strings.TrimRight(stdout, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		switch fields[0] {
		case "C":
			qt.Assert(t, qt.Equals(len(fields), 3))
		case "F":
			qt.Assert(t, qt.Equals(len(fields), 6))
		case "X":
			qt.Assert(t, qt.Equals(len(fields), 3))
		default:
			t.Fatalf("unrecognized record type %q in line %q", fields[0], line)
		}
	}
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
		records := []string{"12345\n", "12345\n", "12345\n"} // 6 bytes each
		got := buildContextStream(records, 13)               // room for two, not three
		qt.Assert(t, qt.Equals(got, "12345\n12345\nX\tTRUNCATED\t1\n"))
	})

	t.Run("no records is the empty string", func(t *testing.T) {
		qt.Assert(t, qt.Equals(buildContextStream(nil, 4096), ""))
	})
}
