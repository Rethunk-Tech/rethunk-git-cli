// No t.Parallel here: every case changes directory (app_test.go's own
// package comment explains why), which t.Chdir forbids combining with it.
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

func TestShow_UsageRefusals(t *testing.T) {
	assertAnchorUsageRefusals(t, "show")
}

// TestShow_PrintsExtentVerbatim pins the contract the command exists for:
// stdout is the anchor's own bytes and nothing else -- no header, no added
// newline -- and it is the worktree's bytes, not HEAD's, so what show prints
// is what commit would stage.
func TestShow_PrintsExtentVerbatim(t *testing.T) {
	dir := chdirTempRepo(t)

	stdout, stderr, code := runApp(t, "show", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(stdout, "// A returns one.\nfunc A() int {\n\treturn 1\n}"))

	// Uncommitted worktree edits are what the default source shows.
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns two.\nfunc A() int {\n\treturn 2\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	stdout, _, code = runApp(t, "show", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stdout, "// A returns two.\nfunc A() int {\n\treturn 2\n}"))
}

// TestShow_SourceRevisionReadsThatBlob covers the capability nothing else in
// rgit offers: the symbol as it stood at a revision, not as it stands now.
// Both git spellings of the flag are pinned -- "--source rev" and
// "--source=rev" -- because the shared arg loop handles them on two separate
// branches.
func TestShow_SourceRevisionReadsThatBlob(t *testing.T) {
	dir := chdirTempRepo(t)
	head := strings.TrimSpace(gittest.Git(t, dir, "rev-parse", "HEAD"))

	writeAppFile(t, dir, "a.go", "package a\n\n// A returns two.\nfunc A() int {\n\treturn 2\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gittest.Commit(t, dir, "feat: A returns two")

	const original = "// A returns one.\nfunc A() int {\n\treturn 1\n}"
	for _, args := range [][]string{
		{"show", "--source", "HEAD~1", "a.go:A"},
		{"show", "--source=HEAD~1", "a.go:A"},
		{"show", "--source", head, "a.go:A"},
		// After the positional, not only before it.
		{"show", "a.go:A", "--source", "HEAD~1"},
	} {
		stdout, stderr, code := runApp(t, args...)
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.Equals(stdout, original))
	}

	// The default still reads the new bytes, so --source is genuinely
	// selecting the blob rather than the test reading a stale worktree.
	stdout, _, code := runApp(t, "show", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stdout, "// A returns two.\nfunc A() int {\n\treturn 2\n}"))
}

// TestShow_SourceDistinguishesBadRevisionFromAbsentPath is the reason
// --source verifies the revision before reading: git's own cat-file answers
// "no such object" to both, so without the check a typo'd revision would be
// reported as a missing symbol.
func TestShow_SourceDistinguishesBadRevisionFromAbsentPath(t *testing.T) {
	dir := chdirTempRepo(t)

	_, stderr, code := runApp(t, "show", "--source", "nosuchrev", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.GitFailure))
	qt.Assert(t, qt.StringContains(stderr, "not a valid revision: nosuchrev"))

	writeAppFile(t, dir, "b.go", "package a\n\nfunc C() int {\n\treturn 3\n}\n")
	gittest.Commit(t, dir, "feat: add b.go")

	_, stderr, code = runApp(t, "show", "--source", "HEAD~1", "b.go:C")
	qt.Assert(t, qt.Equals(code, exitcode.AnchorUnresolvable))
	qt.Assert(t, qt.StringContains(stderr, "is absent at HEAD~1"))

	_, stderr, code = runApp(t, "show", "--source")
	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.StringContains(stderr, "--source requires a value"))
}

// TestShow_MissingWorktreeFileFallsBackToHEAD matches blame's own default:
// a deleted worktree copy resolves against the HEAD blob rather than
// refusing, and a path in neither says so.
func TestShow_MissingWorktreeFileFallsBackToHEAD(t *testing.T) {
	dir := chdirTempRepo(t)
	qt.Assert(t, qt.IsNil(os.Remove(filepath.Join(dir, "a.go"))))

	stdout, stderr, code := runApp(t, "show", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(stdout, "// A returns one.\nfunc A() int {\n\treturn 1\n}"))
}
