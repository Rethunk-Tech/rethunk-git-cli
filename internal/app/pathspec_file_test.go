package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

func TestReadPathspecFileSkipsEmptyLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "targets")
	if err := os.WriteFile(path, []byte("a.go\n\nb.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readPathspecFile(path, false)

	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(got, []string{"a.go", "b.go"}))
}

func TestReadPathspecFileNulPreservesNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "targets")
	if err := os.WriteFile(path, []byte("a\nb.go\x00other.go\x00"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readPathspecFile(path, true)

	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(got, []string{"a\nb.go", "other.go"}))
}

func TestRunPathspecFromFileMatchesPositionals(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 222\n}\n")
	path := filepath.Join(t.TempDir(), "targets")
	if err := os.WriteFile(path, []byte("a.go:A\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, code := runApp(t, "diff", "--porcelain", "--pathspec-from-file", path)
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "a.go\tA\tMOD\t"))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "\tB\t")))

	_, _, code = runApp(t, "commit", "-m", "fix(a): bump A", "--pathspec-from-file", path)
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	head := gitOut(t, dir, "cat-file", "-p", "HEAD:a.go")
	qt.Assert(t, qt.StringContains(head, "return 111"))
	qt.Assert(t, qt.Not(qt.StringContains(head, "return 222")))
}

func TestRun_PathspecFileNulRequiresFromFile(t *testing.T) {
	chdirTempRepo(t)

	for _, command := range []string{"diff", "commit"} {
		args := []string{command, "--pathspec-file-nul"}
		if command == "commit" {
			args = append(args, "-m", "fix: reject lone nul flag")
		}
		_, stderr, code := runApp(t, args...)

		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "pathspec-file-nul"))
	}
}

func TestRun_PathspecFromFileNulMatchesPositionals(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 222\n}\n")
	path := filepath.Join(t.TempDir(), "targets")
	if err := os.WriteFile(path, []byte("a.go:A\x00"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, code := runApp(t, "diff", "--porcelain", "--pathspec-from-file", path, "--pathspec-file-nul")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "a.go\tA\tMOD\t"))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "\tB\t")))

	_, _, code = runApp(t, "commit", "-m", "fix(a): bump A", "--pathspec-from-file", path, "--pathspec-file-nul")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	head := gitOut(t, dir, "cat-file", "-p", "HEAD:a.go")
	qt.Assert(t, qt.StringContains(head, "return 111"))
	qt.Assert(t, qt.Not(qt.StringContains(head, "return 222")))
}
