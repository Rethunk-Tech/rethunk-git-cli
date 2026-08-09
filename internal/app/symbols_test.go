package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

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
