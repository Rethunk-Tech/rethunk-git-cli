package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

func TestRun_SymbolsHelpAndUsage(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, arg := range []string{"--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			stdout, stderr, code := runApp(t, "symbols", arg)
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			qt.Assert(t, qt.Equals(stdout, symbolsHelp))
			qt.Assert(t, qt.Equals(stderr, ""))
		})
	}

	stdout, stderr, code := runApp(t, "symbols")
	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.Equals(stdout, ""))
	wantUsage := "rgit: symbols requires exactly one file argument\n" + symbolsHelp
	qt.Assert(t, qt.Equals(stderr, wantUsage))

	stdout, stderr, code = runApp(t, "symbols", "a.go", "b.go")
	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.Equals(stdout, ""))
	qt.Assert(t, qt.Equals(stderr, wantUsage))
}

func TestRun_SymbolsUnsupportedLanguage(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "main.rs", "fn main() {}\n")

	_, stderr, code := runApp(t, "symbols", "main.rs")
	if code != exitcode.UnsupportedLanguage {
		t.Fatalf("symbols unsupported-language exit code = %d, want %d", code, exitcode.UnsupportedLanguage)
	}
	want := "rgit: unsupported language for \"main.rs\"\n"
	if stderr != want {
		t.Fatalf("symbols unsupported-language stderr = %q, want %q", stderr, want)
	}
}

func TestRunSymbolsListsCleanWorktreeDeclarations(t *testing.T) {
	root := t.TempDir()
	source := []byte("package demo\n\nconst answer = 42\n\nfunc First() {}\n\ntype Thing struct{}\n\nfunc (Thing) Method() {}\n")
	symbolsTestGit(t, root, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, "main.go"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	symbolsTestGit(t, root, "add", "main.go")
	symbolsTestGit(t, root, "-c", "user.name=rgit test", "-c", "user.email=rgit@example.invalid", "commit", "--quiet", "-m", "initial")

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"main.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols() = %d, stderr = %q", code, stderr.String())
	}

	got := strings.Fields(stdout.String())
	for _, want := range []string{"answer", "First", "Thing", "Thing.Method"} {
		if !containsString(got, want) {
			t.Errorf("runSymbols() omitted %q from %q", want, got)
		}
	}
}

func TestRunSymbolsStructuredDataCommitMode(t *testing.T) {
	root := t.TempDir()
	source := []byte("{\n  \"name\": \"demo\",\n  \"enabled\": true\n}\n")
	symbolsTestGit(t, root, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, "config.json"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	symbolsTestGit(t, root, "add", "config.json")
	symbolsTestGit(t, root, "-c", "user.name=rgit test", "-c", "user.email=rgit@example.invalid", "commit", "--quiet", "-m", "initial")

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"config.json"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols() = %d, stderr = %q", code, stderr.String())
	}
	if !containsString(strings.Fields(stdout.String()), "name") {
		t.Fatalf("runSymbols() omitted structured-data symbol from %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runSymbols(context.Background(), root, []string{"--for-commit", "config.json"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols(--for-commit) = %d, stderr = %q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("runSymbols(--for-commit) = %q, want empty stdout", stdout.String())
	}
}

func symbolsTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
