package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gittest"
)

func TestStripControl(t *testing.T) {
	t.Parallel()
	in := "a\x1b]0;x\x07b\r\tc\r\n\x00\xc2\x9bd\xc3\xa9\x7f"
	if got, want := string(stripControl([]byte(in))), "a]0;xb\tc\r\n\x00dé"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// A C1 control or a CRLF split across two writes must be judged whole.
func TestControlWriterJoinsSplitWrites(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := &controlWriter{w: &buf}
	for _, part := range []string{"a\xc2", "\x9bb\r", "\nc\xc2", "\xa9d\r"} {
		n, err := w.Write([]byte(part))
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(n, len(part)))
	}
	qt.Assert(t, qt.IsNil(w.flush()))
	qt.Assert(t, qt.Equals(buf.String(), "ab\r\nc\xc2\xa9d"))
}

func TestRun_DiffStripsControlBytesFromPaths(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "e\x1b]0;PWNED\x07n.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runApp(t, "-C", dir, "diff")

	qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.StringContains(stdout, "PWNED"))
	qt.Assert(t, qt.IsFalse(strings.ContainsAny(stdout+stderr, "\x1b\x07")))
}

// TestRun_SourceRevisionReadsAPathGoneFromHEAD pins the FILE half's extra
// existence probe: with --source, a path has to be looked for at that
// revision too, not only in the worktree, index, or HEAD. A file deleted by
// a later commit exists in none of those three, so show, blame, and log all
// refused an anchor naming it as unclassifiable (129) instead of reading the
// blob the caller pointed them at.
func TestRun_SourceRevisionReadsAPathGoneFromHEAD(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	writeAppFile(t, dir, "gone.go", "package gone\n\nfunc Gone() int {\n\treturn 1\n}\n")
	gittest.Commit(t.Context(), t, dir, "feat: add gone.go")
	gittest.Git(t.Context(), t, dir, "rm", "-q", "gone.go")
	gittest.Commit(t.Context(), t, dir, "chore: drop gone.go")

	t.Run("show", func(t *testing.T) {
		t.Parallel()
		stdout, stderr, code := runApp(t, "-C", dir, "show", "--source", "HEAD~1", "gone.go:Gone")
		qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("stderr: %s", stderr))
		qt.Assert(t, qt.Equals(stdout, "func Gone() int {\n\treturn 1\n}"))
	})

	t.Run("blame", func(t *testing.T) {
		t.Parallel()
		stdout, stderr, code := runApp(t, "-C", dir, "blame", "--source", "HEAD~1", "gone.go:Gone")
		qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("stderr: %s", stderr))
		qt.Assert(t, qt.StringContains(stdout, "func Gone() int {"))
	})

	t.Run("log", func(t *testing.T) {
		t.Parallel()
		stdout, stderr, code := runApp(t, "-C", dir, "log", "--source", "HEAD~1", "gone.go:Gone")
		qt.Assert(t, qt.Equals(code, exitcode.Success), qt.Commentf("stderr: %s", stderr))
		qt.Assert(t, qt.StringContains(stdout, "feat: add gone.go"))
	})

	// The probe must not turn a typo'd revision into the anchor codes:
	// docs/CODES.md pins an unknown --source revision at 128.
	t.Run("an unknown revision stays 128", func(t *testing.T) {
		t.Parallel()
		_, stderr, code := runApp(t, "-C", dir, "show", "--source", "nosuchrev", "gone.go:Gone")
		qt.Assert(t, qt.Equals(code, exitcode.GitFailure), qt.Commentf("stderr: %s", stderr))
		qt.Assert(t, qt.StringContains(stderr, "not a valid revision"))
	})
}

// No t.Parallel: the case needs a working directory outside any repository.
func TestRun_CommitOutsideARepoHintsTheDashCSpelling(t *testing.T) {
	dir := tempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	top, err := filepath.EvalSymlinks(dir)
	qt.Assert(t, qt.IsNil(err))
	t.Chdir(t.TempDir())

	_, stderr, code := runApp(t, "commit", "-m", "fix(a): x", filepath.Join(dir, "a.go")+":A")

	qt.Assert(t, qt.Not(qt.Equals(code, exitcode.Success)))
	qt.Assert(t, qt.StringContains(stderr, "run: rgit -C "+top+" commit … a.go:A\n"))
}

func TestRun_DashCAfterTheCommandSaysWhereItGoes(t *testing.T) {
	t.Parallel()
	_, stderr, code := runApp(t, "commit", "-C", "x", "-m", "fix(a): x", "a.go")

	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.StringContains(stderr, "-C goes before the command"))
}

func TestRun_UsageErrorsAreShortWithAPointer(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	for _, cmd := range []string{"show", "blame", "log", "diff"} {
		_, stderr, code := runApp(t, "-C", dir, cmd, "--bogus", "a.go:A")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage), qt.Commentf("%s", cmd))
		qt.Assert(t, qt.StringContains(stderr, "see rgit "+cmd+" --help\n"), qt.Commentf("%s: %s", cmd, stderr))
		qt.Assert(t, qt.IsTrue(strings.Count(stderr, "\n") < 12), qt.Commentf("%s: %s", cmd, stderr))
	}
}

func TestRun_UnresolvedAnchorNamesTheFile(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	_, stderr, code := runApp(t, "-C", dir, "show", "a.go:Nope")
	qt.Assert(t, qt.Equals(code, exitcode.AnchorUnresolvable))
	qt.Assert(t, qt.StringContains(stderr, `"a.go:Nope"`))
}

// No t.Parallel: the case needs a working directory outside any repository.
func TestRun_ReadCommandsOutsideARepoHintTheDashCSpelling(t *testing.T) {
	dir := tempRepo(t)
	top, err := filepath.EvalSymlinks(dir)
	qt.Assert(t, qt.IsNil(err))
	t.Chdir(t.TempDir())
	for _, cmd := range []string{"show", "blame", "log", "symbols"} {
		_, stderr, code := runApp(t, cmd, filepath.Join(dir, "a.go")+":A")
		qt.Assert(t, qt.Not(qt.Equals(code, exitcode.Success)))
		qt.Assert(t, qt.StringContains(stderr, "run: rgit -C "+top+" "+cmd+" … a.go:A\n"), qt.Commentf("%s: %s", cmd, stderr))
	}
}

// No t.Parallel: the cases need a working directory outside any repository.
func TestRun_DashCHintBranches(t *testing.T) {
	a, b := tempRepo(t), tempRepo(t)
	colon := filepath.Join(t.TempDir(), "d:e")
	qt.Assert(t, qt.IsNil(os.Rename(tempRepo(t), colon)))
	t.Chdir(t.TempDir())
	top := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		qt.Assert(t, qt.IsNil(err))
		return r
	}
	tests := []struct {
		name string
		args []string
		want string // "" means no hint at all
	}{
		{"targets in two repos", []string{"commit", "-m", "x", filepath.Join(a, "a.go"), filepath.Join(b, "a.go")}, ""},
		{"a repo root names itself", []string{"commit", "-m", "x", a}, "run: rgit -C " + top(a) + " commit … .\n"},
		{"a colon in the repo's own path is not an anchor", []string{"commit", "-m", "x", filepath.Join(colon, "a.go")}, "run: rgit -C " + top(colon) + " commit … a.go\n"},
		{"-C already given", []string{"-C", t.TempDir(), "commit", "-m", "x", filepath.Join(a, "a.go")}, ""},
	}
	for _, tt := range tests {
		_, stderr, code := runApp(t, tt.args...)
		qt.Assert(t, qt.Not(qt.Equals(code, exitcode.Success)), qt.Commentf("%s", tt.name))
		if tt.want == "" {
			qt.Assert(t, qt.Not(qt.StringContains(stderr, "run: rgit -C")), qt.Commentf("%s: %s", tt.name, stderr))
		} else {
			qt.Assert(t, qt.StringContains(stderr, tt.want), qt.Commentf("%s: %s", tt.name, stderr))
		}
	}
}
