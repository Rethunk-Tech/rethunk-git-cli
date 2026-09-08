package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

func TestRun_LanguagesInRepoUntrackedGo(t *testing.T) {
	dir, _ := gittest.New(t)
	gittest.Write(t, dir, "main.go", "package main\n")

	stdout, stderr, code := runLanguagesInRepoTest(t, dir)
	if code != exitcode.Success {
		t.Fatalf("runLanguages() code = %v, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "go") {
		t.Errorf("runLanguages() output = %q; want go", stdout)
	}
}

func TestRun_LanguagesInRepoHeadOnlyPythonShebang(t *testing.T) {
	dir, _ := gittest.RepoWithFile(t, "tool", "#!/usr/bin/env python3\nprint('ok')\n", "chore: add python script")
	if err := os.Remove(filepath.Join(dir, "tool")); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runLanguagesInRepoTest(t, dir)
	if code != exitcode.Success {
		t.Fatalf("runLanguages() code = %v, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "python") {
		t.Errorf("runLanguages() output = %q; want python", stdout)
	}
}

func TestRun_LanguagesInRepoMarkdownOmitsGo(t *testing.T) {
	dir, _ := gittest.New(t)
	gittest.Write(t, dir, "README.md", "# docs\n")

	stdout, stderr, code := runLanguagesInRepoTest(t, dir)
	if code != exitcode.Success {
		t.Fatalf("runLanguages() code = %v, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "go") {
		t.Errorf("runLanguages() output = %q; want no go", stdout)
	}
}

func runLanguagesInRepoTest(t *testing.T, dir string) (stdout, stderr string, code exitcode.Code) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = runLanguages(context.Background(), dir, []string{"--in-repo"}, &out, &errOut)
	return out.String(), errOut.String(), code
}
