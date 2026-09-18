// No t.Parallel here: every case changes directory (app_test.go's own
// package comment explains why), which t.Chdir forbids combining with it.
package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

func TestShow_UsageRefusals(t *testing.T) {
	assertAnchorUsageRefusals(t, "show", false)
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

// TestShow_MultipleAnchorsAreLengthFramed pins the batch contract: one anchor
// stays raw bytes so it still pipes, several are framed by a
// "ANCHOR<TAB>NBYTES" line and exactly that many bytes. The length, not a
// delimiter, is what makes the stream parseable -- a symbol's own text can
// contain a line that looks like a header.
func TestShow_MultipleAnchorsAreLengthFramed(t *testing.T) {
	chdirTempRepo(t)

	const a = "// A returns one.\nfunc A() int {\n\treturn 1\n}"
	const b = "func B() int {\n\treturn 2\n}"

	stdout, stderr, code := runApp(t, "show", "a.go:A", "a.go:B")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	want := "a.go:A\t" + strconv.Itoa(len(a)) + "\n" + a + "a.go:B\t" + strconv.Itoa(len(b)) + "\n" + b
	qt.Assert(t, qt.Equals(stdout, want))

	// --with-header frames a lone anchor identically, so a caller looping
	// over an argument list need not branch on its length.
	stdout, _, code = runApp(t, "show", "--with-header", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stdout, "a.go:A\t"+strconv.Itoa(len(a))+"\n"+a))

	// The declared length is the extent's real byte count, so a reader can
	// consume the stream without scanning for a separator at all.
	qt.Assert(t, qt.IsTrue(strings.HasSuffix(stdout, a)))
}

// TestShow_ResolvesEveryAnchorBeforeWriting pins the all-or-nothing rule: a
// failure anywhere in the list leaves stdout empty rather than emitting the
// anchors that happened to come first.
func TestShow_ResolvesEveryAnchorBeforeWriting(t *testing.T) {
	chdirTempRepo(t)

	stdout, stderr, code := runApp(t, "show", "a.go:A", "a.go:NoSuchSymbol")
	qt.Assert(t, qt.Equals(code, exitcode.AnchorUnresolvable))
	qt.Assert(t, qt.Equals(stdout, ""))
	qt.Assert(t, qt.StringContains(stderr, "NoSuchSymbol"))
}

// TestShow_PorcelainFramesSingleAndMultiAnchors pins the machine-readable
// contract: --porcelain frames unconditionally, using the same
// FILE:TAB:NBYTES length framing multi-anchor output already uses. Without
// the flag, single-anchor output stays raw bytes, byte-identical to before.
func TestShow_PorcelainFramesSingleAndMultiAnchors(t *testing.T) {
	chdirTempRepo(t)

	const a = "// A returns one.\nfunc A() int {\n\treturn 1\n}"
	const b = "func B() int {\n\treturn 2\n}"

	// Single anchor under --porcelain is framed.
	stdout, stderr, code := runApp(t, "show", "--porcelain", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(stdout, "a.go:A\t"+strconv.Itoa(len(a))+"\n"+a))

	// Multi-anchor under --porcelain matches the default multi-anchor
	// framing exactly -- the flag changes when framing applies, never the
	// framing itself.
	porcelainMulti, _, code := runApp(t, "show", "--porcelain", "a.go:A", "a.go:B")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	defaultMulti, _, code := runApp(t, "show", "a.go:A", "a.go:B")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(porcelainMulti, defaultMulti))
	qt.Assert(t, qt.Equals(porcelainMulti, "a.go:A\t"+strconv.Itoa(len(a))+"\n"+a+"a.go:B\t"+strconv.Itoa(len(b))+"\n"+b))

	// Without the flag, single-anchor output is still raw bytes.
	raw, _, code := runApp(t, "show", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(raw, a))
}

// TestShow_WithHeaderIsAPorcelainAlias pins --with-header as a deprecated
// alias that still works: identical bytes to --porcelain, in both arities.
func TestShow_WithHeaderIsAPorcelainAlias(t *testing.T) {
	chdirTempRepo(t)

	singlePorcelain, _, code := runApp(t, "show", "--porcelain", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	singleHeader, _, code := runApp(t, "show", "--with-header", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(singleHeader, singlePorcelain))

	multiPorcelain, _, code := runApp(t, "show", "--porcelain", "a.go:A", "a.go:B")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	multiHeader, _, code := runApp(t, "show", "--with-header", "a.go:A", "a.go:B")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(multiHeader, multiPorcelain))
}
