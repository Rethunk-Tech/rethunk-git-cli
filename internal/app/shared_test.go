package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/exitcode"
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
