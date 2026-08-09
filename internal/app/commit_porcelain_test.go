package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

func TestRunCommit_PorcelainEmitsCommitSHA(t *testing.T) {
	dir, _ := gittest.New(t)
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n")
	gittest.Commit(t, dir, "feat: add A")
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 2\n}\n")
	t.Chdir(dir)

	var stdout, stderr strings.Builder
	code := runCommit(context.Background(), "", []string{"--dry-run", "--porcelain", "-m", "fix: update A", "a.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("dry-run porcelain = %v; stdout: %s; stderr: %s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "H\t") {
		t.Fatalf("dry-run porcelain = %q; must not emit a commit SHA", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runCommit(context.Background(), "", []string{"--porcelain", "-m", "fix: update A", "a.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("porcelain commit = %v; stdout: %s; stderr: %s", code, stdout.String(), stderr.String())
	}

	sha := strings.TrimSpace(gittest.Git(t, dir, "rev-parse", "HEAD"))
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("porcelain output = %q; want SHA plus one target row", stdout.String())
	}
	if lines[0] != "H\t"+sha {
		t.Errorf("porcelain header = %q; want %q", lines[0], "H\t"+sha)
	}
	if !strings.HasPrefix(lines[1], "a.go\t\t") {
		t.Errorf("porcelain target row = %q; want a.go path record", lines[1])
	}

	stdout.Reset()
	stderr.Reset()
	code = runCommit(context.Background(), "", []string{"--allow-empty", "--porcelain", "-m", "chore: empty", "a.go:A"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("allow-empty porcelain = %v; stdout: %s; stderr: %s", code, stdout.String(), stderr.String())
	}
	emptyLines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(emptyLines) != 1 || !strings.HasPrefix(emptyLines[0], "H\t") {
		t.Errorf("allow-empty porcelain = %q; want one H record", stdout.String())
	}
}

func TestRunCommit_QuietSuccessEmptyStdout(t *testing.T) {
	dir, _ := gittest.New(t)
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n")
	gittest.Commit(t, dir, "feat: add A")
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 2\n}\n")
	t.Chdir(dir)

	var stdout, stderr strings.Builder
	code := runCommit(context.Background(), "", []string{"--quiet", "-m", "fix: update A", "a.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("quiet commit = %v; stdout: %s; stderr: %s", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("quiet stdout = %q; want empty", stdout.String())
	}
}
