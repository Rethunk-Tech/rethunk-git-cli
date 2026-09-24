package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

func TestReadPathspecFileSkipsEmptyLines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "targets")
	if err := os.WriteFile(path, []byte("a.go\n\nb.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readPathspecFile(path, false)

	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(got, []string{"a.go", "b.go"}))
}

func TestReadPathspecFileNulPreservesNewline(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "targets")
	if err := os.WriteFile(path, []byte("a\nb.go\x00other.go\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readPathspecFile(path, true)

	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(got, []string{"a\nb.go", "other.go"}))
}

func TestReadPathspecFileStdin(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		nul      bool
		want     []string
	}{
		{
			name:     "lines",
			contents: "a.go\n\nb.go\n",
			want:     []string{"a.go", "b.go"},
		},
		{
			name:     "nul",
			contents: "a\nb.go\x00other.go\x00",
			nul:      true,
			want:     []string{"a\nb.go", "other.go"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "targets")
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			stdin, err := os.Open(path) //nolint:gosec // path is inside the test temp directory
			if err != nil {
				t.Fatal(err)
			}
			originalStdin := os.Stdin
			os.Stdin = stdin
			defer func() {
				os.Stdin = originalStdin
				_ = stdin.Close()
			}()

			got, err := readPathspecFile("-", test.nul)

			qt.Assert(t, qt.IsNil(err))
			qt.Assert(t, qt.DeepEquals(got, test.want))
		})
	}
}

func TestRunPathspecFromFileMatchesPositionals(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 222\n}\n")
	path := filepath.Join(t.TempDir(), "targets")
	if err := os.WriteFile(path, []byte("a.go:A\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, _, code := runApp(t, "-C", dir, "diff", "--porcelain", "--pathspec-from-file", path)
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "a.go\tA\tMOD\t"))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "\tB\t")))

	_, _, code = runApp(t, "-C", dir, "commit", "-m", "fix(a): bump A", "--pathspec-from-file", path)
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	head := gitOut(t, dir, "cat-file", "-p", "HEAD:a.go")
	qt.Assert(t, qt.StringContains(head, "return 111"))
	qt.Assert(t, qt.Not(qt.StringContains(head, "return 222")))
}

func TestRun_PathspecFileNulRequiresFromFile(t *testing.T) {
	t.Parallel()
	cwd := tempRepo(t)

	for _, command := range []string{"diff", "commit"} {
		args := []string{command, "--pathspec-file-nul"}
		if command == "commit" {
			args = append(args, "-m", "fix: reject lone nul flag")
		}
		_, stderr, code := runApp(t, append([]string{"-C", cwd}, args...)...)

		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "pathspec-file-nul"))
	}
}

func TestRun_PathspecFromFileNulMatchesPositionals(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 222\n}\n")
	path := filepath.Join(t.TempDir(), "targets")
	if err := os.WriteFile(path, []byte("a.go:A\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, _, code := runApp(t, "-C", dir, "diff", "--porcelain", "--pathspec-from-file", path, "--pathspec-file-nul")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "a.go\tA\tMOD\t"))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "\tB\t")))

	_, _, code = runApp(t, "-C", dir, "commit", "-m", "fix(a): bump A", "--pathspec-from-file", path, "--pathspec-file-nul")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	head := gitOut(t, dir, "cat-file", "-p", "HEAD:a.go")
	qt.Assert(t, qt.StringContains(head, "return 111"))
	qt.Assert(t, qt.Not(qt.StringContains(head, "return 222")))
}
