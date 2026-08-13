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

func TestRunSymbolsHonorsCoreIgnoreCaseForExtensions(t *testing.T) {
	root := t.TempDir()
	symbolsTestGit(t, root, "init", "--quiet")
	symbolsTestGit(t, root, "config", "core.ignorecase", "true")
	if err := os.WriteFile(filepath.Join(root, "Foo.GO"), []byte("package demo\n\nfunc First() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"Foo.GO"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols(Foo.GO) with core.ignorecase=true = %d, stderr = %q", code, stderr.String())
	}
	if !containsString(strings.Fields(stdout.String()), "First") {
		t.Fatalf("runSymbols(Foo.GO) omitted First from %q", stdout.String())
	}

	symbolsTestGit(t, root, "config", "core.ignorecase", "false")
	stdout.Reset()
	stderr.Reset()
	code = runSymbols(context.Background(), root, []string{"Foo.GO"}, &stdout, &stderr)
	if code != exitcode.UnsupportedLanguage {
		t.Fatalf("runSymbols(Foo.GO) with core.ignorecase=false = %d, want %d", code, exitcode.UnsupportedLanguage)
	}
}

func TestRun_SymbolsListsHeadDeclarationsWhenWorktreeFileIsGone(t *testing.T) {
	root := t.TempDir()
	source := []byte("package demo\n\nfunc Foo() {}\n")
	symbolsTestGit(t, root, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, "a.go"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	symbolsTestGit(t, root, "add", "a.go")
	symbolsTestGit(t, root, "-c", "user.name=rgit test", "-c", "user.email=rgit@example.invalid", "commit", "--quiet", "-m", "initial")
	if err := os.Remove(filepath.Join(root, "a.go")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"a.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols() = %d, stderr = %q", code, stderr.String())
	}
	if !containsString(strings.Fields(stdout.String()), "Foo") {
		t.Fatalf("runSymbols() omitted HEAD symbol from %q", stdout.String())
	}
}

func TestRun_SymbolsMissingUntrackedFileStillErrors(t *testing.T) {
	root := t.TempDir()
	symbolsTestGit(t, root, "init", "--quiet")

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"missing.go"}, &stdout, &stderr)
	if code != exitcode.GitFailure {
		t.Fatalf("runSymbols() = %d, want %d", code, exitcode.GitFailure)
	}
	if !strings.Contains(stderr.String(), `rgit: cannot read "missing.go":`) {
		t.Fatalf("runSymbols() stderr = %q, want cannot-read error", stderr.String())
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
