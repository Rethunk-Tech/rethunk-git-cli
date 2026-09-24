package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

// TestRunCommit_RefusesSymbolAnchorOnStructuredData pins the end-to-end
// behaviour of the structured-data guard: a FILE:SYMBOL anchor into a
// JSON, YAML, or TOML file is refused at exit 12 naming the file, and the
// identical change committed by path still works -- the guard must never make
// a structured-data file uncommittable, only unaddressable by symbol.
func TestRunCommit_RefusesSymbolAnchorOnStructuredData(t *testing.T) {
	for _, tc := range []struct {
		name       string
		path       string
		before     string
		after      string
		anchorName string
	}{
		{
			name:       "JSON",
			path:       "package.json",
			before:     `{"name": "before"}` + "\n",
			after:      `{"name": "after"}` + "\n",
			anchorName: "name",
		},
		{
			name:       "YAML",
			path:       "config.yaml",
			before:     "name: before\n",
			after:      "name: after\n",
			anchorName: "name",
		},
		{
			name:       "YML",
			path:       "config.yml",
			before:     "name: before\n",
			after:      "name: after\n",
			anchorName: "name",
		},
		{
			name:       "TOML",
			path:       "config.toml",
			before:     "title = \"before\"\n",
			after:      "title = \"after\"\n",
			anchorName: "title",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := gittest.RepoWithFile(t.Context(), t, tc.path, tc.before, "chore: add structured data")
			gittest.Write(t, dir, tc.path, tc.after)
			t.Chdir(dir)

			var stdout, stderr strings.Builder
			anchor := tc.path + ":" + tc.anchorName
			code := runCommit(context.Background(), "", []string{"-m", "chore: bump", anchor}, &stdout, &stderr)
			if code != exitcode.StructuredDataAnchorRefused {
				t.Fatalf("runCommit FILE:SYMBOL = %v; want exitcode.StructuredDataAnchorRefused; stderr: %s", code, stderr.String())
			}
			wantStderr := "rgit: " + tc.path + ": structured-data file; commit it by path instead of a symbol anchor (e.g. rgit commit -m ... " + tc.path + ")\n"
			if stderr.String() != wantStderr {
				t.Errorf("stderr = %q; want %q", stderr.String(), wantStderr)
			}
			if stdout.String() != "" {
				t.Errorf("stdout = %q; want empty", stdout.String())
			}

			stdout.Reset()
			stderr.Reset()
			code = runCommit(context.Background(), "", []string{"-m", "chore: bump", tc.path}, &stdout, &stderr)
			if code != exitcode.Success {
				t.Fatalf("runCommit by path = %v; want exitcode.Success; stderr: %s", code, stderr.String())
			}
			if stderr.String() != "" {
				t.Errorf("stderr = %q; want empty", stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.path) {
				t.Errorf("stdout = %q; want package listing", stdout.String())
			}
			if got := gitOut(t, dir, "cat-file", "-p", "HEAD:"+tc.path); !strings.Contains(got, strings.TrimRight(tc.after, "\n")) {
				t.Errorf("HEAD:%s = %q; want updated content", tc.path, got)
			}
		})
	}
}

// TestRun_CommitRefusesStructuredDataSymbolViaRunApp pins runApp dispatch:
// symbols lists the resolvable key name, while commit refuses package.json:name.
func TestRun_CommitRefusesStructuredDataSymbolViaRunApp(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.RepoWithFile(t.Context(), t, "package.json", `{"name": "before"}`+"\n", "chore: add structured data")

	stdout, stderr, code := runApp(t, "-C", dir, "symbols", "package.json")
	if code != exitcode.Success {
		t.Fatalf("runApp symbols = %v; want exitcode.Success; stderr: %s", code, stderr)
	}
	if !containsString(strings.Fields(stdout), "name") {
		t.Errorf("symbols stdout = %q; want discrete key token %q", stdout, "name")
	}

	gittest.Write(t, dir, "package.json", `{"name": "after"}`+"\n")
	stdout, stderr, code = runApp(t, "-C", dir, "commit", "-m", "chore: bump", "package.json:name")
	if code != exitcode.StructuredDataAnchorRefused {
		t.Fatalf("runApp commit FILE:SYMBOL = %v; want exitcode.StructuredDataAnchorRefused; stderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q; want empty", stdout)
	}
	wantStderr := "rgit: package.json: structured-data file; commit it by path instead of a symbol anchor (e.g. rgit commit -m ... package.json)\n"
	if stderr != wantStderr {
		t.Errorf("stderr = %q; want %q", stderr, wantStderr)
	}

	stdout, stderr, code = runApp(t, "-C", dir, "commit", "-m", "chore: bump", "package.json")
	if code != exitcode.Success {
		t.Fatalf("runApp commit by path = %v; want exitcode.Success; stderr: %s", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q; want empty", stderr)
	}
	if !strings.Contains(stdout, "package.json") {
		t.Errorf("stdout = %q; want package listing", stdout)
	}
	if got := gitOut(t, dir, "cat-file", "-p", "HEAD:package.json"); !strings.Contains(got, strings.TrimRight(`{"name": "after"}`+"\n", "\n")) {
		t.Errorf("HEAD:package.json = %q; want updated content", got)
	}
}

// TestRunCommit_CountingWarningsReachStderr proves synth.Plan's
// CountingWarnings actually reaches a caller now that commit.go reads it,
// rather than staying dead code. An unreadable untracked file is a real,
// reproducible case where line counts genuinely cannot be computed, not a
// fake or injected error.
func TestRunCommit_CountingWarningsReachStderr(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.New(t.Context(), t)
	gittest.Write(t, dir, "new.txt", "hello\n")
	full := filepath.Join(dir, "new.txt")
	if err := os.Chmod(full, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(full, 0o644); err != nil { //nolint:gosec // restore the test fixture mode
			t.Errorf("restore test fixture mode: %v", err)
		}
	})

	var stdout, stderr strings.Builder
	code := runCommit(context.Background(), dir, []string{"-m", "chore: add new.txt", "--dry-run", "new.txt"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runCommit --dry-run = %v; stdout: %s; stderr: %s", code, stdout.String(), stderr.String())
	}

	got := stderr.String()
	if !strings.Contains(got, "[warning] new.txt: line counts unavailable") {
		t.Errorf("stderr = %q; want a [warning] line about new.txt's unavailable line counts", got)
	}
}

// TestCommitTargets_UnhandledKindErrorsRatherThanSkips pins n10: an
// unexpected cli.Classification.Kind must return a named internal error
// rather than silently omitting the target it was classified from.
// Unreachable today through runCommit itself -- classified is always built
// with allowRevisions=false, so only KindPathspec and KindAnchor ever
// reach here -- but a future flip of that flag must fail loudly the
// moment it does, not stage a partial commit with nothing on stderr to
// say why.
func TestCommitTargets_UnhandledKindErrorsRatherThanSkips(t *testing.T) {
	t.Parallel()
	_, err := commitTargets("/repo", "", []cli.Classification{{Kind: cli.KindRevision, Revision: "HEAD"}}, nil, nil)
	if err == nil {
		t.Fatal("commitTargets: want an error for an unhandled classification kind, got nil")
	}
	if !strings.Contains(err.Error(), "unhandled classification kind") {
		t.Errorf("commitTargets error = %q; want it to name the unhandled kind", err.Error())
	}
}

func TestRunCommit_IndexOnlyPathSymbolAnchor(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.New(t.Context(), t)
	gittest.Write(t, dir, "new.go", "package p\n\nfunc New() {}\n")
	gittest.Git(t.Context(), t, dir, "add", "new.go")
	if err := os.Remove(dir + "/new.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir + "/new.go"); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(new.go) = %v; want os.IsNotExist", err)
	}
	if got := gitOut(t, dir, "show", ":new.go"); !strings.Contains(got, "func New()") {
		t.Errorf("git show :new.go = %q; want New function", got)
	}

	_, stderr, code := runApp(t, "-C", dir, "commit", "-m", "feat(p): add New", "new.go:New")
	if code != exitcode.Success {
		t.Fatalf("runApp commit FILE:SYMBOL = %v; want exitcode.Success; stderr: %s", code, stderr)
	}
	if got := gitOut(t, dir, "cat-file", "-p", "HEAD:new.go"); !strings.Contains(got, "func New()") {
		t.Errorf("HEAD:new.go = %q; want New function", got)
	}
}

// TestCommit_HookFailureRestoresPrestagedState pins the hook-rejection
// rollback: when the commit fails, the index reads exactly as before rgit
// staged anything -- the named anchor is unstaged, unrelated pre-staged
// work survives, the worktree is byte-identical, and stderr names the
// touched path with the no-worktree-changes hint. The exit code is
// unchanged: a hook rejection is still exitcode.GitFailure.
func TestCommit_HookFailureRestoresPrestagedState(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.New(t.Context(), t)
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gittest.Write(t, dir, "sibling.txt", "base\n")
	gittest.Commit(t.Context(), t, dir, "chore: initial")

	// Unrelated pre-staged work the rollback must preserve.
	gittest.Write(t, dir, "sibling.txt", "staged change\n")
	gittest.Git(t.Context(), t, dir, "add", "--", "sibling.txt")
	// The named edit to stage.
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	workA, err := os.ReadFile(filepath.Join(dir, "a.go")) //nolint:gosec // path is inside the test temp directory
	if err != nil {
		t.Fatal(err)
	}
	gittest.InstallHook(t.Context(), t, dir, "pre-commit", "#!/bin/sh\nexit 1\n")

	var stdout, stderr strings.Builder
	code := runCommit(context.Background(), dir, []string{"-m", "fix(a): update A", "a.go:A"}, &stdout, &stderr)
	if code != exitcode.GitFailure {
		t.Fatalf("runCommit = %v; want exitcode.GitFailure; stderr: %s", code, stderr.String())
	}
	if got := gittest.Git(t.Context(), t, dir, "show", ":a.go"); strings.Contains(got, "return 111") {
		t.Errorf(":a.go = %q; want HEAD content, named anchor rolled back", got)
	}
	if got := gittest.Git(t.Context(), t, dir, "show", ":sibling.txt"); !strings.Contains(got, "staged change") {
		t.Errorf(":sibling.txt = %q; want pre-staged change preserved", got)
	}
	// a.go unstaged again (worktree differs from index) while the sibling
	// stays staged exactly as it was: entries sort by path, and the
	// unstaged " M" sorts before the staged "M ".
	if got := gittest.Git(t.Context(), t, dir, "status", "--porcelain"); got != " M a.go\nM  sibling.txt\n" {
		t.Errorf("status = %q; want the unstaged a.go edit plus the pre-staged sibling", got)
	}
	for _, want := range []string{"a.go", "no worktree files were changed"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q; want it to mention %q", stderr.String(), want)
		}
	}
	if after, err := os.ReadFile(filepath.Join(dir, "a.go")); err != nil { //nolint:gosec // path is inside the test temp directory
		t.Fatal(err)
	} else if string(after) != string(workA) {
		t.Errorf("worktree a.go changed by the failed commit: before %q, after %q", workA, after)
	}
}
