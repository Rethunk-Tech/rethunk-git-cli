package app

import (
	"context"
	"os"
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
			dir, _ := gittest.New(t)
			gittest.Write(t, dir, tc.path, tc.before)
			gittest.Commit(t, dir, "chore: add structured data")
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
	dir, _ := gittest.New(t)
	gittest.Write(t, dir, "package.json", `{"name": "before"}`+"\n")
	gittest.Commit(t, dir, "chore: add structured data")
	t.Chdir(dir)

	stdout, stderr, code := runApp(t, "symbols", "package.json")
	if code != exitcode.Success {
		t.Fatalf("runApp symbols = %v; want exitcode.Success; stderr: %s", code, stderr)
	}
	if !containsString(strings.Fields(stdout), "name") {
		t.Errorf("symbols stdout = %q; want discrete key token %q", stdout, "name")
	}

	gittest.Write(t, dir, "package.json", `{"name": "after"}`+"\n")
	stdout, stderr, code = runApp(t, "commit", "-m", "chore: bump", "package.json:name")
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

	stdout, stderr, code = runApp(t, "commit", "-m", "chore: bump", "package.json")
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
// rather than staying dead code. An untracked file on an unborn branch is
// a real, reproducible case where `git diff --numstat HEAD` genuinely
// fails -- there is no HEAD yet -- which is exactly the "line counts
// could not be fully computed" case CountingWarnings exists for, not a
// fake or injected error.
func TestRunCommit_CountingWarningsReachStderr(t *testing.T) {
	dir, _ := gittest.New(t)
	gittest.Write(t, dir, "new.txt", "hello\n")
	t.Chdir(dir)

	var stdout, stderr strings.Builder
	code := runCommit(context.Background(), "", []string{"-m", "chore: add new.txt", "--dry-run", "new.txt"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runCommit --dry-run = %v; stdout: %s; stderr: %s", code, stdout.String(), stderr.String())
	}

	got := stderr.String()
	if !strings.Contains(got, "[warning]") || !strings.Contains(got, "tracked line counts unavailable") {
		t.Errorf("stderr = %q; want a [warning] line about unavailable tracked line counts", got)
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
	_, err := commitTargets("/repo", "", []cli.Classification{{Kind: cli.KindRevision, Revision: "HEAD"}}, nil, nil)
	if err == nil {
		t.Fatal("commitTargets: want an error for an unhandled classification kind, got nil")
	}
	if !strings.Contains(err.Error(), "unhandled classification kind") {
		t.Errorf("commitTargets error = %q; want it to name the unhandled kind", err.Error())
	}
}

func TestRunCommit_IndexOnlyPathSymbolAnchor(t *testing.T) {
	dir, _ := gittest.New(t)
	gittest.Write(t, dir, "new.go", "package p\n\nfunc New() {}\n")
	gittest.Git(t, dir, "add", "new.go")
	if err := os.Remove(dir + "/new.go"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	_, stderr, code := runApp(t, "commit", "-m", "feat(p): add New", "new.go:New")
	if code != exitcode.Success {
		t.Fatalf("runApp commit FILE:SYMBOL = %v; want exitcode.Success; stderr: %s", code, stderr)
	}
	if got := gitOut(t, dir, "cat-file", "-p", "HEAD:new.go"); !strings.Contains(got, "func New()") {
		t.Errorf("HEAD:new.go = %q; want New function", got)
	}
}
