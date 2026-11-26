package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gittest"
)

func TestLandSummaryWording(t *testing.T) {
	t.Parallel()
	if got := summaryPushed("/r", "abc"); got != "/r: pushed abc" {
		t.Errorf("summaryPushed = %q", got)
	}
	if got := summarySkippedAlreadyUpstream("/r"); got != "/r: skipped (already upstream)" {
		t.Errorf("summarySkippedAlreadyUpstream = %q", got)
	}
	if got := summaryPR("/r", "https://example/pr/1", "queued"); got != "/r: PR https://example/pr/1 (queued)" {
		t.Errorf("summaryPR queued = %q", got)
	}
	if got := summaryPR("/r", "https://example/pr/1", "auto-merge"); got != "/r: PR https://example/pr/1 (auto-merge)" {
		t.Errorf("summaryPR auto-merge = %q", got)
	}
	if got := summaryPR("/r", "https://example/pr/1", "merged"); got != "/r: PR https://example/pr/1 (merged)" {
		t.Errorf("summaryPR merged = %q", got)
	}
}

func TestLandSummaryRed(t *testing.T) {
	t.Parallel()
	logPath := filepath.Join(t.TempDir(), "gate.log")
	if err := os.WriteFile(logPath, []byte("ok pkg\n\n--- FAIL: TestFoo (0.01s)\nFAIL\nFAIL\tpkg\t0.02s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bosun := "exit status 1\n────\nsummary: (done in 51.56 seconds)\n✗ gates (48.03 seconds)\nerror: failed to push some refs ...\n"
	cases := []struct {
		name, repo, out, want string
	}{
		{
			name: "lefthook summary box",
			repo: "bosun",
			out:  bosun,
			want: "bosun: red (gates | error: failed to push some refs ...)",
		},
		{
			name: "gate verdict",
			repo: "bosun",
			out: "────\nsummary: (done in 51.56 seconds)\n✗ gates (48.03 seconds)\ngate: failed: go test -race -short ./... -> " +
				logPath + "\nerror: failed to push some refs ...\n",
			want: "bosun: red (go test -race -short ./... | --- FAIL: TestFoo (0.01s) | FAIL | FAIL\tpkg\t0.02s)",
		},
		{
			name: "plain git rejection",
			repo: "repo",
			out: "Enumerating objects: 5, done.\nTo github.com:org/repo.git\n" +
				" ! [rejected] main -> main (non-fast-forward)\n" +
				"error: failed to push some refs to 'origin'\n" +
				"hint: Updates were rejected\nhint: git pull before pushing again\n",
			want: "repo: red (To github.com:org/repo.git | ! [rejected] main -> main (non-fast-forward) | " +
				"error: failed to push some refs to 'origin' | hint: Updates were rejected | " +
				"hint: git pull before pushing again)",
		},
		{
			name: "ANSI coloured lefthook",
			repo: "bosun",
			out:  "\x1b[31mexit status 1\x1b[0m\n\x1b[90m────\x1b[0m\nsummary: (done in 51.56 seconds)\n\x1b[31m✗ gates (48.03 seconds)\x1b[0m\n\x1b[1;31merror: failed to push some refs ...\x1b[0m\n",
			want: "bosun: red (gates | error: failed to push some refs ...)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := summaryRed(tc.repo, tc.out); got != tc.want {
				t.Errorf("summaryRed = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestLandJudgePRState(t *testing.T) {
	t.Parallel()
	cases := []struct {
		info prInfo
		want string
	}{
		{prInfo{State: "MERGED"}, "merged"},
		{prInfo{Merged: true}, "merged"},
		{prInfo{State: "OPEN", HasQueue: true}, "queued"},
		{prInfo{State: "OPEN", HasAutoMerge: true}, "auto-merge"},
		{prInfo{State: "OPEN", HasQueue: true, HasAutoMerge: true}, "queued"},
		{prInfo{State: "OPEN"}, ""},
	}
	for _, tc := range cases {
		if got := judgePRState(tc.info); got != tc.want {
			t.Errorf("judgePRState(%+v) = %q; want %q", tc.info, got, tc.want)
		}
	}
}

func TestLandRuleDetection(t *testing.T) {
	t.Parallel()
	types := parseRuleTypes("pull_request\nmerge_queue\n")
	need, mq := rulesNeedPR(types)
	if !need || !mq {
		t.Errorf("line types need=%v mq=%v; want both true", need, mq)
	}
	types = parseRuleTypes(`[{"type":"pull_request"},{"type":"required_status_checks"}]`)
	need, mq = rulesNeedPR(types)
	if !need || mq {
		t.Errorf("json types need=%v mq=%v; want need true mq false", need, mq)
	}
	need, mq = rulesNeedPR(parseRuleTypes("required_signatures\n"))
	if need || mq {
		t.Errorf("unrelated types need=%v mq=%v; want both false", need, mq)
	}
}

func TestLandAlreadyUpstream(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gittest.Write(t, dir, "ahead.txt", "local\n")
	gittest.Commit(t.Context(), t, dir, "local patch")
	sha := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	other := t.TempDir()
	gitOut(t, other, "clone", bare, ".")
	gitOut(t, other, "config", "user.name", "test")
	gitOut(t, other, "config", "user.email", "test@test")
	gitOut(t, other, "remote", "add", "local", dir)
	gitOut(t, other, "fetch", "-q", "local")
	gitOut(t, other, "cherry-pick", sha)
	gitOut(t, other, "push", "origin", "HEAD")

	d := testLandDeps(t)
	res := landOne(dir, d)
	if res.Kind != landSkipped {
		t.Fatalf("kind = %v lines=%v; want skipped", res.Kind, res.Lines)
	}
	if len(res.Lines) != 1 || res.Lines[0] != dir+": skipped (already upstream)" {
		t.Errorf("lines = %v", res.Lines)
	}
}

func TestLandPushedLocalBare(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")

	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	code := landRepos(t.Context(), []string{dir}, false, false, false, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	want := dir + ": pushed " + strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")) + "\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q; want %q", stdout.String(), want)
	}
	assertNoFleetTouch(t, d)
}

func TestLandPlainGate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		gate     func(dir string) (string, error)
		wantCode exitcode.Code
		pushed   bool
		line     string
	}{
		{"green", func(string) (string, error) { return "", nil }, exitcode.Success, true, ": pushed "},
		{"red", func(string) (string, error) {
			return "gate: failed: unit -> /x.log\n", errors.New("exit 1")
		}, 1, false, ": red (gate failed: unit; log "},
		{"gate leaves untracked file", func(dir string) (string, error) {
			gittest.Write(t, dir, "coverage.out", "x\n")
			return "", nil
		}, exitcode.Success, true, ": pushed "},
		{"moved", func(dir string) (string, error) {
			gittest.Write(t, dir, "more.txt", "m\n")
			gitOut(t, dir, "add", "more.txt")
			gitOut(t, dir, "commit", "-q", "-m", "during")
			return "", nil
		}, 1, false, "HEAD moved during the gate; nothing pushed, re-run"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir, bare := repoWithOrigin(t)
			gittest.Write(t, dir, "ahead.txt", "x\n")
			gittest.Commit(t.Context(), t, dir, "ahead")
			before := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD"))
			d := testLandDeps(t)
			d.gate = func(dir string, _ bool) (string, error) { return c.gate(dir) }
			var stdout strings.Builder
			d.stdout = &stdout
			code := landRepos(t.Context(), []string{dir}, false, false, false, d)
			if code != c.wantCode || !strings.Contains(stdout.String(), c.line) {
				t.Fatalf("code = %v stdout=%q; want %v with %q", code, stdout.String(), c.wantCode, c.line)
			}
			if after := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD")); (after != before) != c.pushed {
				t.Errorf("origin moved = %v; want %v", after != before, c.pushed)
			}
		})
	}
}

// TestLandSkipsUmbrellaWhenSubmoduleRepoRed pins that an umbrella repo is not pushed
// while a repo it may point into failed its gate.
func TestLandSkipsUmbrellaWhenSubmoduleRepoRed(t *testing.T) {
	t.Parallel()
	sub, _ := repoWithOrigin(t)
	umb, umbBare := repoWithOrigin(t)
	gittest.Write(t, umb, ".gitmodules", "")
	gittest.Commit(t.Context(), t, umb, "umbrella")
	gittest.Write(t, sub, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, sub, "ahead")
	before := strings.TrimSpace(gitOut(t, umbBare, "rev-parse", "HEAD"))

	d := testLandDeps(t)
	d.gate = func(dir string, _ bool) (string, error) {
		if dir == sub {
			return "gate: failed: unit -> /x.log\n", errors.New("exit 1")
		}
		return "", nil
	}
	var stdout strings.Builder
	d.stdout = &stdout
	if code := landRepos(t.Context(), []string{umb, sub}, false, false, false, d); code != 1 {
		t.Fatalf("code = %v stdout=%q; want 1", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), umb+": skipped (a submodule repo is red)") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if after := strings.TrimSpace(gitOut(t, umbBare, "rev-parse", "HEAD")); after != before {
		t.Errorf("umbrella origin moved to %s", after)
	}
}

func TestLandInstallAfterDirectPush(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, "Makefile", ".PHONY: install\ninstall:\n\t@echo test-bin > installed.marker\n\t@echo test-bin\n")
	gittest.Commit(t.Context(), t, dir, "add install")

	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	code := runLandWith(t.Context(), dir, []string{"--install"}, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, dir+": pushed ") {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(out, dir+": installed test-bin\n") {
		t.Errorf("missing installed line: %q", out)
	}
	body, err := os.ReadFile(filepath.Join(dir, "installed.marker")) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "test-bin\n" {
		t.Errorf("marker = %q", body)
	}
}

func TestLandInstallSkipsWithoutMakefile(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")

	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	code := runLandWith(t.Context(), dir, []string{"--install"}, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), dir+": skipped (no Makefile)\n") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestLandInstallSkipsWithoutTarget(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, "Makefile", "all:\n\t@true\n")
	gittest.Commit(t.Context(), t, dir, "makefile without install")

	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	code := runLandWith(t.Context(), dir, []string{"--install"}, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), dir+": skipped (no install target)\n") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestLandInstallFailureExit5(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, "Makefile", ".PHONY: install\ninstall:\n\t@echo boom\n\t@false\n")
	gittest.Commit(t.Context(), t, dir, "add failing install")

	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	code := runLandWith(t.Context(), dir, []string{"--install"}, d)
	if code != 5 {
		t.Fatalf("code = %v stdout=%q; want 5", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, dir+": pushed ") {
		t.Errorf("stdout = %q", out)
	}
	if strings.Contains(out, ": installed ") {
		t.Errorf("printed installed on failure: %q", out)
	}
	if !strings.Contains(out, "boom") && !strings.Contains(out, "Error") {
		t.Errorf("missing failing tail: %q", out)
	}
}

func TestLandInstallSkipsPR(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gitOut(t, dir, "remote", "set-url", "origin", "git@github.com:acme/demo.git")
	gitOut(t, dir, "remote", "set-url", "--push", "origin", bare)
	gittest.Write(t, dir, "Makefile", ".PHONY: install\ninstall:\n\t@echo test-bin > installed.marker\n\t@echo test-bin\n")
	gittest.Commit(t.Context(), t, dir, "ahead")

	gh := &fakeGH{
		rules:  []string{"pull_request"},
		squash: true,
		url:    "https://github.com/acme/demo/pull/4",
		number: 4,
		prs:    []prInfo{{State: "OPEN", HasAutoMerge: true, URL: "https://github.com/acme/demo/pull/4"}},
	}
	d := testLandDeps(t)
	d.gh = gh
	d.clock = &fakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	var stdout strings.Builder
	d.stdout = &stdout
	code := runLandWith(t.Context(), dir, []string{"--install"}, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "installed.marker")); !os.IsNotExist(err) {
		t.Errorf("PR route ran install: %v", err)
	}
	if strings.Contains(stdout.String(), "installed ") {
		t.Errorf("PR route printed install: %q", stdout.String())
	}
}

func TestLandWatchFastForwardsCleanDefault(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	other := t.TempDir()
	gitOut(t, other, "clone", bare, ".")
	gitOut(t, other, "config", "user.name", "test")
	gitOut(t, other, "config", "user.email", "test@test")
	gittest.Write(t, other, "merged.txt", "from-pr\n")
	gittest.Commit(t.Context(), t, other, "merged via pr")
	gitOut(t, other, "push", "origin", "HEAD")
	originTip := strings.TrimSpace(gitOut(t, other, "rev-parse", "HEAD"))
	localBefore := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	if localBefore == originTip {
		t.Fatal("origin did not advance past the local default")
	}

	d := testLandDeps(t)
	d.gh = &fakeGH{
		prs: []prInfo{{State: "MERGED", MergeSHA: originTip, URL: "https://github.com/acme/demo/pull/51"}},
		run: workflowRun{Status: "completed", Conclusion: "success"},
	}
	res := watchUntilDone(landWatchJob{
		Repo:    dir,
		Slug:    "acme/demo",
		PR:      51,
		URL:     "https://github.com/acme/demo/pull/51",
		Default: "main",
	}, d)
	if res.State != "merged" {
		t.Fatalf("state = %q", res.State)
	}
	got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	if got != originTip {
		t.Errorf("local HEAD = %s; want origin tip %s", got, originTip)
	}
}

func TestLandWatchLeavesDirtyDefault(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	other := t.TempDir()
	gitOut(t, other, "clone", bare, ".")
	gitOut(t, other, "config", "user.name", "test")
	gitOut(t, other, "config", "user.email", "test@test")
	gittest.Write(t, other, "merged.txt", "from-pr\n")
	gittest.Commit(t.Context(), t, other, "merged via pr")
	gitOut(t, other, "push", "origin", "HEAD")
	originTip := strings.TrimSpace(gitOut(t, other, "rev-parse", "HEAD"))
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	gittest.Write(t, dir, "base.txt", "dirty\n")

	d := testLandDeps(t)
	var stderr strings.Builder
	d.stderr = &stderr
	d.gh = &fakeGH{
		prs: []prInfo{{State: "MERGED", MergeSHA: originTip, URL: "https://github.com/acme/demo/pull/51"}},
		run: workflowRun{Status: "completed", Conclusion: "success"},
	}
	res := watchUntilDone(landWatchJob{
		Repo:    dir,
		Slug:    "acme/demo",
		PR:      51,
		URL:     "https://github.com/acme/demo/pull/51",
		Default: "main",
	}, d)
	if res.State != "merged" {
		t.Fatalf("state = %q", res.State)
	}
	got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	if got != before {
		t.Errorf("dirty HEAD moved %s -> %s", before, got)
	}
	body, err := os.ReadFile(filepath.Join(dir, "base.txt")) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "dirty\n" {
		t.Errorf("dirty worktree = %q", body)
	}
	if !strings.Contains(stderr.String(), "dirty tracked files") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestLandPRViaRules(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gitOut(t, dir, "remote", "set-url", "origin", "git@github.com:acme/demo.git")
	gitOut(t, dir, "remote", "set-url", "--push", "origin", bare)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")

	gh := &fakeGH{
		rules:  []string{"pull_request"},
		squash: true,
		url:    "https://github.com/acme/demo/pull/4",
		number: 4,
		prs:    []prInfo{{State: "OPEN", HasAutoMerge: true, URL: "https://github.com/acme/demo/pull/4"}},
	}
	d := testLandDeps(t)
	d.gh = gh
	d.clock = &fakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	var stdout strings.Builder
	d.stdout = &stdout
	code := landRepos(t.Context(), []string{dir}, false, false, false, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, dir+": PR https://github.com/acme/demo/pull/4 (auto-merge)") {
		t.Errorf("stdout = %q", out)
	}
	if strings.Contains(out, "watching") {
		t.Errorf("land still announces a watcher: %q", out)
	}
	if len(gh.merges) < 2 || gh.merges[0] != "plain" || gh.merges[1] != "auto:--squash" {
		t.Errorf("merges = %#v; want plain then auto:--squash", gh.merges)
	}
	assertNoFleetTouch(t, d)
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD")); got != "main" {
		t.Errorf("HEAD = %q; want main after reset --keep", got)
	}
}

func TestLandHelp(t *testing.T) {
	t.Parallel()
	var stdout, stderr strings.Builder
	code := runLand(t.Context(), "", []string{"--help"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("code = %v", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "usage: rgit land") {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(out, "--branch") {
		t.Errorf("help missing --branch: %q", out)
	}
	if !strings.Contains(out, "--install") {
		t.Errorf("help missing --install: %q", out)
	}
}

func TestLandBranchCleanMainFFPush(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	tip := strings.TrimSpace(gitOut(t, wt, "rev-parse", "HEAD"))

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := strings.TrimSpace(gitOut(t, bare, "log", "-1", "--pretty=%s")); got != "feat" {
		t.Errorf("origin subject = %q", got)
	}
	if _, err := d.git.run(dir, "show-ref", "--verify", "--quiet", "refs/heads/feat"); err == nil {
		t.Error("clean-main left branch feat")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("clean-main left worktree: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "removed worktree "+wt+"\n") {
		t.Errorf("missing worktree removal: %q", out)
	}
	if !strings.Contains(out, "deleted branch feat\n") {
		t.Errorf("missing branch deletion: %q", out)
	}
	last := lastNonEmptyLine(out)
	got := strings.Fields(last)
	if len(got) != 5 || got[0] != dir || got[1] != "feat" || got[2] != tip || got[4] != "direct" {
		t.Errorf("summary = %q", last)
	}
	if !strings.Contains(got[3], "land-branch") {
		t.Errorf("gate log path = %q", got[3])
	}
	assertNoFleetTouch(t, d)
}

func TestLandBranchKeepsWorktreeWithIgnoredFiles(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, ".gitignore", "*.local\n")
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", ".gitignore", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	gittest.Write(t, wt, "secrets.local", "s\n")

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	if code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d); code != exitcode.Success {
		t.Fatalf("code = %v stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(wt, "secrets.local")); err != nil {
		t.Errorf("ignored file deleted: %v", err)
	}
}

func TestLandBranchLocalLeavesOrigin(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	tip := strings.TrimSpace(gitOut(t, wt, "rev-parse", "HEAD"))
	originBefore := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD"))

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD")); got != originBefore {
		t.Errorf("origin HEAD = %s; want %s", got, originBefore)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != tip {
		t.Errorf("local default = %s; want %s", got, tip)
	}
	last := lastNonEmptyLine(stdout.String())
	got := strings.Fields(last)
	if len(got) != 5 || got[0] != dir || got[1] != "feat" || got[2] != tip || got[4] != "local" {
		t.Errorf("summary = %q", last)
	}
	if !strings.Contains(got[3], "land-branch") {
		t.Errorf("gate log path = %q", got[3])
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree %s still present: %v", wt, err)
	}
	if out := gitOut(t, dir, "branch", "--list", "feat"); strings.TrimSpace(out) != "" {
		t.Errorf("branch feat still present: %q", out)
	}
}

func TestLandBranchDirtyMainLeavesMain(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	gittest.Write(t, dir, "base.txt", "base\ndirty\n")

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != before {
		t.Errorf("dirty main moved HEAD %s -> %s", before, got)
	}
	body, err := os.ReadFile(filepath.Join(dir, "base.txt")) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "base\ndirty\n" {
		t.Errorf("dirty main worktree = %q", body)
	}
	if got := strings.TrimSpace(gitOut(t, bare, "log", "-1", "--pretty=%s")); got != "feat" {
		t.Errorf("origin subject = %q", got)
	}
	if _, err := d.git.run(dir, "show-ref", "--verify", "--quiet", "refs/heads/feat"); err == nil {
		t.Error("dirty-main left branch feat")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("dirty-main left worktree: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "removed worktree "+wt+"\n") {
		t.Errorf("missing worktree removal: %q", out)
	}
	if !strings.Contains(out, "deleted branch feat\n") {
		t.Errorf("missing branch deletion: %q", out)
	}
	last := lastNonEmptyLine(out)
	if !strings.HasSuffix(last, " direct") {
		t.Errorf("summary = %q", last)
	}
}

func TestLandBranchConflictExit2(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "base.txt", "branch\n")
	gitOut(t, wt, "add", "base.txt")
	gitOut(t, wt, "commit", "-q", "-m", "branch")
	gittest.Write(t, dir, "base.txt", "mainline\n")
	gittest.Commit(t.Context(), t, dir, "mainline")
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d)
	if code != 2 {
		t.Fatalf("code = %v stdout=%q stderr=%q; want 2", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "conflict: hand to solver") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != before {
		t.Errorf("conflict moved main")
	}
	if _, err := d.git.run(dir, "show-ref", "--verify", "--quiet", "refs/heads/feat"); err != nil {
		t.Error("conflict deleted branch feat")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("conflict removed worktree: %v", err)
	}
}

func TestLandBranchRedGateExit3(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	d := testLandDeps(t)
	d.gate = func(string, bool) (string, error) {
		return "stub-gate-fail\ngate: failed: unit -> /x.log\n", errors.New("exit 1")
	}
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d)
	if code != 3 {
		t.Fatalf("code = %v stdout=%q stderr=%q; want 3", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "stub-gate-fail") {
		t.Errorf("stderr missing log body: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), filepath.Join(d.cacheHome(), "land-branch")) {
		t.Errorf("stderr missing log path: %q", stderr.String())
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != before {
		t.Error("red gate moved main")
	}
	if _, err := d.git.run(dir, "show-ref", "--verify", "--quiet", "refs/heads/feat"); err != nil {
		t.Error("red gate deleted branch feat")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("red gate removed worktree: %v", err)
	}
}

func TestLandBranchDirtyWorktreeLeavesBoth(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	gittest.Write(t, wt, "scratch.txt", "untracked\n")

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := strings.TrimSpace(gitOut(t, bare, "log", "-1", "--pretty=%s")); got != "feat" {
		t.Errorf("origin subject = %q", got)
	}
	if _, err := d.git.run(dir, "show-ref", "--verify", "--quiet", "refs/heads/feat"); err != nil {
		t.Error("dirty worktree deleted branch feat")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("dirty worktree removed worktree: %v", err)
	}
	if strings.Contains(stdout.String(), "removed worktree") || strings.Contains(stdout.String(), "deleted branch") {
		t.Errorf("dirty worktree printed removal: %q", stdout.String())
	}
}

func TestLandBranchRedPushLeavesBoth(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	gitOut(t, dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d)
	if code != exitcode.LandRed {
		t.Fatalf("code = %v stdout=%q stderr=%q; want LandRed", code, stdout.String(), stderr.String())
	}
	if _, err := d.git.run(dir, "show-ref", "--verify", "--quiet", "refs/heads/feat"); err != nil {
		t.Error("red push deleted branch feat")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("red push removed worktree: %v", err)
	}
}

func TestLandBranchQueuedPRLeavesBoth(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gitOut(t, dir, "remote", "set-url", "origin", "git@github.com:acme/demo.git")
	gitOut(t, dir, "remote", "set-url", "--push", "origin", bare)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")

	gh := &fakeGH{
		rules:  []string{"merge_queue"},
		url:    "https://github.com/acme/demo/pull/4",
		number: 4,
		prs:    []prInfo{{State: "OPEN", HasQueue: true, URL: "https://github.com/acme/demo/pull/4"}},
	}
	d := testLandDeps(t)
	d.gh = gh
	d.clock = &fakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := d.git.run(dir, "show-ref", "--verify", "--quiet", "refs/heads/feat"); err != nil {
		t.Error("queued PR deleted branch feat")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("queued PR removed worktree: %v", err)
	}
	if strings.Contains(stdout.String(), "removed worktree") || strings.Contains(stdout.String(), "deleted branch") {
		t.Errorf("queued PR printed removal: %q", stdout.String())
	}
	last := lastNonEmptyLine(stdout.String())
	if !strings.HasSuffix(last, " queue") {
		t.Errorf("summary = %q", last)
	}
}

func TestGateLandBranchLinksIgnoredEnvLocal(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, ".gitignore", ".env.local\n")
	gittest.Commit(t.Context(), t, dir, "ignore env.local")
	envDir := filepath.Join(dir, "apps", "web")
	if err := os.MkdirAll(envDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, ".env.local"), []byte("NEXT_PUBLIC_SITE_URL=https://example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)

	d := testLandDeps(t)
	d.gate = func(gateDir string, _ bool) (string, error) {
		if _, err := os.ReadFile(filepath.Join(gateDir, "apps", "web", ".env.local")); err != nil { //nolint:gosec // test reads the linked path, not the secret
			return "missing env", err
		}
		return "ok\n", nil
	}
	prep := landBranchPrep{repo: dir, branch: "feat", def: "main", wt: wt}
	if _, code := gateLandBranch(prep, d); code != exitcode.Success {
		t.Fatalf("code = %v; want success", code)
	}
	if _, err := os.Lstat(filepath.Join(wt, "apps", "web", ".env.local")); !os.IsNotExist(err) {
		t.Errorf("env link still present: %v", err)
	}
}

func TestGateLandBranchLeavesExistingEnvLocal(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, ".gitignore", ".env.local\n")
	gittest.Commit(t.Context(), t, dir, "ignore env.local")
	if err := os.MkdirAll(filepath.Join(dir, "apps", "web"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "apps", "web", ".env.local"), []byte("from-main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	if err := os.MkdirAll(filepath.Join(wt, "apps", "web"), 0o750); err != nil {
		t.Fatal(err)
	}
	wtEnv := filepath.Join(wt, "apps", "web", ".env.local")
	if err := os.WriteFile(wtEnv, []byte("from-worktree\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	d := testLandDeps(t)
	d.gate = func(gateDir string, _ bool) (string, error) {
		body, err := os.ReadFile(filepath.Join(gateDir, "apps", "web", ".env.local")) //nolint:gosec // test fixture path
		if err != nil {
			return "missing env", err
		}
		if string(body) != "from-worktree\n" {
			return "replaced", errors.New("replaced")
		}
		return "ok\n", nil
	}
	prep := landBranchPrep{repo: dir, branch: "feat", def: "main", wt: wt}
	if _, code := gateLandBranch(prep, d); code != exitcode.Success {
		t.Fatalf("code = %v; want success", code)
	}
	fi, err := os.Lstat(wtEnv)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("existing worktree env file was replaced with a symlink")
	}
	body, err := os.ReadFile(wtEnv) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "from-worktree\n" {
		t.Error("existing worktree env file contents changed")
	}
}

// TestLandBranchRedGateRunsOnceAndStops pins that a red --branch gate is not retried: one gate run, exit 3, nothing pushed.
func TestLandBranchRedGateRunsOnceAndStops(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	before := strings.TrimSpace(gitOut(t, bare, "rev-parse", "main"))

	n := 0
	d := testLandDeps(t)
	d.gate = func(string, bool) (string, error) {
		n++
		return "gate: failed: unit -> /x.log\n", errors.New("exit 1")
	}
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	if code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d); code != exitcode.LandGateRed {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if n != 1 {
		t.Errorf("gate ran %d times; want 1", n)
	}
	if _, err := os.Stat(filepath.Join(d.cacheHome(), "land-branch", "flakes.tsv")); err == nil {
		t.Error("flakes.tsv written")
	}
	if got := strings.TrimSpace(gitOut(t, bare, "rev-parse", "main")); got != before {
		t.Errorf("origin main moved to %s", got)
	}
}

func TestGHCLIBranchRuleTypes(t *testing.T) {
	t.Parallel()
	var got []string
	g := &ghCLI{runFn: func(dir string, args ...string) (string, error) {
		if dir != "" {
			t.Errorf("dir = %q; want empty", dir)
		}
		got = args
		return "pull_request\nmerge_queue\n", nil
	}}
	types, err := g.branchRuleTypes("acme/demo", "main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(types, ",") != "pull_request,merge_queue" {
		t.Errorf("types = %v", types)
	}
	want := []string{"api", "repos/acme/demo/rules/branches/main", "-q", ".[].type"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %#v; want %#v", got, want)
	}
	g.runFn = func(string, ...string) (string, error) { return "", errors.New("gh down") }
	if _, err := g.branchRuleTypes("acme/demo", "main"); err == nil {
		t.Error("error not propagated")
	}
}

func TestGHCLIMergeFlags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ out, flag string }{{"true", "squashMergeAllowed"}, {"true", "mergeCommitAllowed"}} {
		var got []string
		g := &ghCLI{runFn: func(_ string, args ...string) (string, error) {
			got = args
			return tc.out + "\n", nil
		}}
		check := g.squashMergeAllowed
		if tc.flag == "mergeCommitAllowed" {
			check = g.mergeCommitAllowed
		}
		ok, err := check("acme/demo")
		if err != nil || !ok {
			t.Errorf("%s = %v, %v; want true", tc.flag, ok, err)
		}
		want := []string{"repo", "view", "acme/demo", "--json", tc.flag, "-q", "." + tc.flag}
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("argv = %#v; want %#v", got, want)
		}
		g.runFn = func(string, ...string) (string, error) { return "", errors.New("gh down") }
		if _, err := check("acme/demo"); err == nil {
			t.Errorf("%s error not propagated", tc.flag)
		}
	}
}

func TestGHCLICreatePR(t *testing.T) {
	t.Parallel()
	var got []string
	g := &ghCLI{runFn: func(dir string, args ...string) (string, error) {
		if dir != "/src/r" {
			t.Errorf("dir = %q", dir)
		}
		got = args
		return "Creating pull request\nhttps://github.com/acme/demo/pull/4\n", nil
	}}
	url, err := g.createPR("/src/r", "land/x", "- subject")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://github.com/acme/demo/pull/4" {
		t.Errorf("url = %q", url)
	}
	want := []string{"pr", "create", "--fill", "--head", "land/x", "--body", "- subject"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %#v; want %#v", got, want)
	}
	g.runFn = func(string, ...string) (string, error) { return "  boom  \n", errors.New("exit 1") }
	if _, err := g.createPR("/src/r", "land/x", ""); err == nil || err.Error() != "boom" {
		t.Errorf("err = %v; want boom", err)
	}
}

func TestGHCLIMergePRandNumber(t *testing.T) {
	t.Parallel()
	cases := []struct {
		auto     bool
		strategy string
		want     []string
	}{
		{false, "", []string{"pr", "merge", "land/x"}},
		{true, "", []string{"pr", "merge", "land/x", "--auto"}},
		{true, "--squash", []string{"pr", "merge", "land/x", "--auto", "--squash"}},
		{false, "--merge", []string{"pr", "merge", "land/x", "--merge"}},
	}
	for _, tc := range cases {
		var got []string
		g := &ghCLI{runFn: func(_ string, args ...string) (string, error) {
			got = args
			return "", nil
		}}
		_ = g.mergePR("/src/r", "land/x", tc.auto, tc.strategy)
		if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
			t.Errorf("argv = %#v; want %#v", got, tc.want)
		}
	}

	var got []string
	g := &ghCLI{runFn: func(_ string, args ...string) (string, error) {
		got = args
		return "42\n", nil
	}}
	n, err := g.prNumber("/src/r", "land/x")
	if err != nil || n != 42 {
		t.Errorf("prNumber = %d, %v; want 42", n, err)
	}
	want := []string{"pr", "view", "land/x", "--json", "number", "-q", ".number"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %#v; want %#v", got, want)
	}
	g.runFn = func(string, ...string) (string, error) { return "", errors.New("no pr") }
	if _, err := g.prNumber("/src/r", "land/x"); err == nil {
		t.Error("run error not propagated")
	}
	g.runFn = func(string, ...string) (string, error) { return "not-a-number", nil }
	if _, err := g.prNumber("/src/r", "land/x"); err == nil {
		t.Error("atoi error not propagated")
	}
}

func TestGHCLIPullRequestAndWorkflow(t *testing.T) {
	t.Parallel()
	var got []string
	g := &ghCLI{runFn: func(_ string, args ...string) (string, error) {
		got = args
		return `{"state":"OPEN","url":"u","auto":true,"queue":false,"sha":"s"}`, nil
	}}
	info, err := g.pullRequest("acme", "demo", 7)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "OPEN" || info.URL != "u" || !info.HasAutoMerge || info.MergeSHA != "s" {
		t.Errorf("info = %+v", info)
	}
	want := []string{
		"api", "graphql", "-f",
		`query={repository(owner:"acme",name:"demo"){pullRequest(number:7){state url autoMergeRequest{enabledAt} mergeQueueEntry{state} mergeCommit{oid}}}}`,
		"-q", `.data.repository.pullRequest | {state: .state, url: .url, auto: (.autoMergeRequest != null), queue: (.mergeQueueEntry != null), sha: .mergeCommit.oid}`,
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %#v; want %#v", got, want)
	}
	g.runFn = func(string, ...string) (string, error) { return "", errors.New("graphql down") }
	if _, err := g.pullRequest("acme", "demo", 7); err == nil {
		t.Error("pullRequest error not propagated")
	}

	g.runFn = func(_ string, args ...string) (string, error) {
		got = args
		return `{"status":"completed","conclusion":"success"}`, nil
	}
	run, err := g.latestWorkflowRun("acme", "demo", "main", "abc")
	if err != nil || run.Status != "completed" || run.Conclusion != "success" {
		t.Errorf("run = %+v, %v", run, err)
	}
	want = []string{
		"api", "repos/acme/demo/actions/runs?branch=main&head_sha=abc&per_page=1", "-q",
		".workflow_runs[0] | {status: .status, conclusion: .conclusion}",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %#v; want %#v", got, want)
	}
	g.runFn = func(string, ...string) (string, error) { return "", errors.New("api down") }
	if _, err := g.latestWorkflowRun("acme", "demo", "main", "abc"); err == nil {
		t.Error("latestWorkflowRun error not propagated")
	}
}

func TestCacheHome(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/xdg/cache")
	if got := cacheHome(); got != "/xdg/cache" {
		t.Errorf("cacheHome = %q; want the XDG override", got)
	}
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	if got := cacheHome(); got != filepath.Join(os.TempDir(), "rgit-cache") {
		t.Errorf("cacheHome without HOME = %q; want the temp fallback", got)
	}
}

func TestLandRootsRelativeRepos(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	base := filepath.Join(t.TempDir(), "work")
	git := &fakeGitRunner{respond: func(string, []string) (string, error) { return "", errors.New("no repo") }}
	d.git = git
	var stdout strings.Builder
	d.stdout = &stdout
	if code := runLandWith(t.Context(), base, []string{"one", "two"}, d); code != exitcode.LandSkipped {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	one, two := filepath.Join(base, "one"), filepath.Join(base, "two")
	// Each repo is probed once by the pin scan, then once by the catch-up and once by the land.
	want := []string{one, two, one, one, two, two}
	if strings.Join(git.dirs, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("dirs = %#v; want %#v", git.dirs, want)
	}
	for _, r := range []string{one, two} {
		if !strings.Contains(stdout.String(), r+": skipped (not a git repo)\n") {
			t.Errorf("stdout missing %q: %q", r, stdout.String())
		}
	}
}

func TestLandOneSkipReasons(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)

	loose := t.TempDir()
	if res := landOne(loose, d); res.Kind != landSkipped || res.Lines[0] != loose+": skipped (not a git repo)" {
		t.Errorf("loose dir = %+v", res)
	}

	dirty, _ := repoWithOrigin(t)
	gittest.Write(t, dirty, "base.txt", "changed\n")
	if res := landOne(dirty, d); res.Kind != landSkipped || res.Lines[0] != dirty+": skipped (dirty tracked files: base.txt)" {
		t.Errorf("dirty repo = %+v", res)
	}

	noUpstream, _ := gittest.RepoWithFile(t.Context(), t, "base.txt", "base\n", "base")
	if res := landOne(noUpstream, d); res.Kind != landSkipped || res.Lines[0] != noUpstream+": skipped (no upstream)" {
		t.Errorf("no upstream = %+v", res)
	}

	even, _ := repoWithOrigin(t)
	if res := landOne(even, d); res.Kind != landSkipped || res.Lines[0] != even+": skipped (not ahead)" {
		t.Errorf("not ahead = %+v", res)
	}
}

func TestLandOneNotesUnqueuedPR(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gitOut(t, dir, "remote", "set-url", "origin", "git@github.com:acme/demo.git")
	gitOut(t, dir, "remote", "set-url", "--push", "origin", bare)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")

	d := testLandDeps(t)
	d.gh = &fakeGH{
		rules: []string{"pull_request"},
		url:   "https://github.com/acme/demo/pull/4",
		prs:   []prInfo{{State: "OPEN", URL: "https://github.com/acme/demo/pull/4"}},
	}
	res := landOne(dir, d)
	if res.Kind != landPR {
		t.Fatalf("kind = %v lines=%v", res.Kind, res.Lines)
	}
	want := dir + ": note: PR open but neither queued nor set to auto-merge"
	if len(res.Lines) == 0 || res.Lines[0] != want {
		t.Errorf("lines = %#v; want first %q", res.Lines, want)
	}
	if !res.NotEnqueued || landExit(false, false, false, res.NotEnqueued) != exitcode.LandNotEnqueued {
		t.Errorf("NotEnqueued = %v", res.NotEnqueued)
	}
	if n := strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "origin/main..HEAD")); n != "1" {
		t.Errorf("unqueued PR still reset main: %s commits ahead", n)
	}
}

func prRouteDeps(t *testing.T, dir, bare string) (landDeps, *fakeGH) {
	t.Helper()
	gitOut(t, dir, "remote", "set-url", "origin", "git@github.com:acme/demo.git")
	gitOut(t, dir, "remote", "set-url", "--push", "origin", bare)
	d := testLandDeps(t)
	f := &fakeGH{
		rules: []string{"pull_request"},
		url:   "https://github.com/acme/demo/pull/4",
		prs:   []prInfo{{State: "OPEN", HasAutoMerge: true, URL: "https://github.com/acme/demo/pull/4"}},
	}
	d.gh = f
	return d, f
}

func TestLandOnePRRouteKeepsSiblingCommit(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	d, f := prRouteDeps(t, dir, bare)
	f.onCreate = func() {
		gittest.Write(t, dir, "sib.txt", "s\n")
		gittest.Commit(t.Context(), t, dir, "sibling")
	}
	res := landOne(dir, d)
	if res.Kind != landPR {
		t.Fatalf("kind = %v lines=%v", res.Kind, res.Lines)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s")); got != "sibling" {
		t.Errorf("sibling commit dropped, HEAD = %q", got)
	}
}

func TestLandOnePRRouteReportsFailedReset(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gittest.Write(t, dir, "base.txt", "ahead\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	d, f := prRouteDeps(t, dir, bare)
	f.number = 4
	f.onCreate = func() { gittest.Write(t, dir, "base.txt", "sibling edit\n") }
	res := landOne(dir, d)
	if res.Kind != landPR {
		t.Fatalf("kind = %v lines=%v", res.Kind, res.Lines)
	}
	if got := strings.Join(res.Lines, "\n"); !strings.Contains(got, dir+": warning: local main not reset (") || !strings.Contains(got, "git reset --keep origin/main") {
		t.Errorf("lines = %q; want a not-reset warning", got)
	}
}

func TestLandOnePushesGatedCommit(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	gated := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	d := testLandDeps(t)
	f := &fakeGH{onRules: func() {
		gittest.Write(t, dir, "bad.txt", "b\n")
		gittest.Commit(t.Context(), t, dir, "ungated")
	}}
	d.gh = f
	gitOut(t, dir, "remote", "set-url", "origin", "git@github.com:acme/demo.git")
	gitOut(t, dir, "remote", "set-url", "--push", "origin", bare)
	res := landOne(dir, d)
	if res.Kind != landPushed || res.SHA != gated {
		t.Fatalf("kind = %v sha = %q want %q lines=%v", res.Kind, res.SHA, gated, res.Lines)
	}
	if got := strings.TrimSpace(gitOut(t, bare, "rev-parse", "main")); got != gated {
		t.Errorf("origin main = %s, want gated %s", got, gated)
	}
}

func TestLandInstallSkipsDirtyTracked(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, "Makefile", ".PHONY: install\ninstall:\n\t@echo installed\n")
	gittest.Commit(t.Context(), t, dir, "add install")
	gittest.Write(t, dir, "base.txt", "changed\n")

	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	code := runLandWith(t.Context(), dir, []string{"--install"}, d)
	if code != exitcode.LandSkipped {
		t.Fatalf("code = %v stdout=%q; a dirty skip needs action", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), dir+": skipped (dirty tracked files: base.txt)\n") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestInstallOneSkipsDirtyTracked(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(".PHONY: install\ninstall:\n\t@echo installed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Write(t, dir, "base.txt", "changed\n")

	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	if failed := installOne(t.Context(), dir, d); failed {
		t.Error("dirty tracked repo reported a failed install")
	}
	if !strings.Contains(stdout.String(), dir+": skipped (dirty tracked files: base.txt)\n") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "base.txt")); err != nil {
		t.Errorf("installOne removed the dirty file: %v", err)
	}
}

func TestInstallOneQuietFailure(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("recipe kills make with a POSIX signal")
	}
	dir, _ := repoWithOrigin(t)
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(".PHONY: install\ninstall:\n\t@kill -9 $$PPID\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	if failed := installOne(t.Context(), dir, d); !failed {
		t.Fatal("make install killed by a signal not reported as failed")
	}
	out := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(out, dir+": ") || !strings.Contains(out, "signal") {
		t.Errorf("stdout = %q; want %s: <signal error>", out, dir)
	}
}

func TestRunGHRunsInDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a shell script")
	}
	bin := t.TempDir()
	script := filepath.Join(bin, "gh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\npwd\n"), 0o700); err != nil { //nolint:gosec // test fixture executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	work := t.TempDir()
	out, err := runGH(t.Context(), work, "repo", "view")
	if err != nil {
		t.Fatalf("runGH: %v out=%q", err, out)
	}
	if strings.TrimSpace(out) != work {
		t.Errorf("runGH ran in %q; want %q", strings.TrimSpace(out), work)
	}
	if _, err := runGH(t.Context(), filepath.Join(work, "absent"), "repo", "view"); err == nil {
		t.Error("runGH in a missing dir: want error")
	}
}

func TestPrepareLandBranchWorktreeAddFailure(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gitOut(t, dir, "branch", "feat")
	if err := os.WriteFile(filepath.Join(dir, ".git", "worktrees"), []byte("not a dir\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	if code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d); code != exitcode.GitFailure {
		t.Fatalf("code = %v stdout=%q stderr=%q; want 128", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "error: ") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestIsIgnoredEnvName(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		".env":           true,
		".env.local":     true,
		".env.dev.local": true,
		".env..local":    false,
		".env.dev":       false,
		".envrc":         false,
		"env.local":      false,
	}
	for name, want := range cases {
		if got := isIgnoredEnvName(name); got != want {
			t.Errorf("isIgnoredEnvName(%q) = %v; want %v", name, got, want)
		}
	}
}

func TestDefaultBranchFallbacks(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	dir, _ := gittest.RepoWithFile(t.Context(), t, "base.txt", "base\n", "base")
	if got, ok := defaultBranchChecked(dir, d); !ok || got != "main" {
		t.Errorf("defaultBranchChecked on main = %q,%v", got, ok)
	}
	gitOut(t, dir, "branch", "-M", "master")
	if got, ok := defaultBranchChecked(dir, d); !ok || got != "master" {
		t.Errorf("defaultBranchChecked on master = %q,%v", got, ok)
	}
	gitOut(t, dir, "branch", "-M", "trunk")
	if got, ok := defaultBranchChecked(dir, d); ok || got != "" {
		t.Errorf("defaultBranchChecked without main or master = %q,%v", got, ok)
	}
}

func TestLandUnknownDefaultBranchSkips(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.RepoWithFile(t.Context(), t, "base.txt", "base\n", "base")
	gitOut(t, dir, "branch", "-M", "trunk")
	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	code := runLandWith(t.Context(), dir, []string{"--no-gate"}, d)
	if code != exitcode.LandSkipped || !strings.Contains(stdout.String(), "skipped (cannot determine default branch)") {
		t.Errorf("code = %v stdout = %q", code, stdout.String())
	}
}

func TestLastNLinesTail(t *testing.T) {
	t.Parallel()
	if got := lastNLines("a\nb\nc\n", 2); strings.Join(got, ",") != "b,c" {
		t.Errorf("lastNLines tail = %v", got)
	}
	if got := lastNLines("a\nb\nc\n", 9); strings.Join(got, ",") != "a,b,c" {
		t.Errorf("lastNLines all = %v", got)
	}
	if got := lastNLines("\n", 2); got != nil {
		t.Errorf("lastNLines blank = %v", got)
	}
}

func TestMergeStrategyOrder(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	d.gh = &fakeGH{squash: true, merge: true}
	if got := mergeStrategy("acme/demo", d); got != "--squash" {
		t.Errorf("mergeStrategy = %q; want --squash", got)
	}
	d.gh = &fakeGH{merge: true}
	if got := mergeStrategy("acme/demo", d); got != "--merge" {
		t.Errorf("mergeStrategy = %q; want --merge", got)
	}
	d.gh = &fakeGH{}
	if got := mergeStrategy("acme/demo", d); got != "--rebase" {
		t.Errorf("mergeStrategy = %q; want --rebase", got)
	}
}

func TestSlugFromOriginForms(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"git@github.com:acme/demo.git":            "acme/demo",
		"git@github.com:acme/demo":                "acme/demo",
		"https://github.com/acme/demo.git":        "acme/demo",
		"http://example.com/acme/demo":            "acme/demo",
		"ssh://git.example.com/acme/demo":         "acme/demo",
		"http://example.com":                      "",
		"ssh://git@host/o/r":                      "o/r",
		"ssh://git@host:22/o/r.git":               "o/r",
		"https://user@host/o/r":                   "o/r",
		"https://x-access-token:tok@host/o/r.git": "o/r",
		"/local/checkout/demo.git":                "",
		"":                                        "",
	}
	for origin, want := range cases {
		if got := slugFromOrigin(origin); got != want {
			t.Errorf("slugFromOrigin(%q) = %q; want %q", origin, got, want)
		}
	}
}

func TestParsePRQueryJSON(t *testing.T) {
	t.Parallel()
	info := parsePRQuery(`{"state":"CLOSED","url":"u","auto":false,"queue":true,"sha":""}`)
	if !info.Closed || info.Merged || !info.HasQueue || info.URL != "u" {
		t.Errorf("closed info = %+v", info)
	}
	info = parsePRQuery(`{"state":"MERGED","sha":"abc"}`)
	if !info.Merged || info.MergeSHA != "abc" {
		t.Errorf("merged info = %+v", info)
	}
	if info := parsePRQuery(`{"url":"u"}`); info != (prInfo{}) {
		t.Errorf("stateless json = %+v; want empty", info)
	}
	if info := parsePRQuery(" merged \n"); info.State != "MERGED" {
		t.Errorf("string form = %+v", info)
	}
}

func TestParseWorkflowQueryJSON(t *testing.T) {
	t.Parallel()
	run := parseWorkflowQuery(`{"status":"in_progress","conclusion":""}`)
	if run.Status != "in_progress" || run.Conclusion != "" {
		t.Errorf("run = %+v", run)
	}
	if run := parseWorkflowQuery("not json"); run != (workflowRun{}) {
		t.Errorf("non-json = %+v; want empty", run)
	}
}

func TestSummaryPREmptyState(t *testing.T) {
	t.Parallel()
	if got := summaryPR("/r", "https://example/pr/1", ""); got != "/r: PR https://example/pr/1" {
		t.Errorf("summaryPR empty state = %q", got)
	}
}

func TestGateVerdictsBlankVerdict(t *testing.T) {
	t.Parallel()
	cmds, logs := gateVerdicts("gate: FAIL\n  \n")
	if len(cmds) != 0 || len(logs) != 0 {
		t.Errorf("cmds = %#v logs = %#v; want none", cmds, logs)
	}
	cmds, _ = gateVerdicts("gate: FAIL go test ./... (1.20s)\n")
	if len(cmds) != 1 || cmds[0] != "go test ./..." {
		t.Errorf("cmds = %#v; want the timing stripped", cmds)
	}
}

func TestWaitForCITimeoutWithRunningCI(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	d.clock = &fakeClock{now: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)}
	d.gh = &fakeGH{run: workflowRun{Status: "in_progress"}}
	res := waitForCI(landWatchResult{Repo: "o/r", CI: "none", State: "merged"}, "o", "r", true, "main", "abc", d)
	if res.State != "timeout" || res.CI != "running" {
		t.Errorf("result = %+v; want timeout with running CI", res)
	}
}

func TestCIFromRun(t *testing.T) {
	t.Parallel()
	cases := []struct {
		run  workflowRun
		want string
	}{
		{workflowRun{Status: "completed", Conclusion: "success"}, "success"},
		{workflowRun{Status: "completed", Conclusion: "failure"}, "failure"},
		{workflowRun{Status: "queued"}, "running"},
		{workflowRun{}, "running"},
	}
	for _, tc := range cases {
		if got := ciFromRun(tc.run); got != tc.want {
			t.Errorf("ciFromRun(%+v) = %q; want %q", tc.run, got, tc.want)
		}
	}
}

func TestFinalWaitLine(t *testing.T) {
	t.Parallel()
	pr := landResult{Repo: "/r", Kind: landPR, URL: "https://example/pr/1"}
	if got := finalWaitLine(pr, landWatchResult{State: "merged", CI: "success"}); got != "/r: PR https://example/pr/1 (merged); CI success" {
		t.Errorf("PR line = %q", got)
	}
	skipped := landResult{Repo: "/r", Kind: landSkipped, Lines: []string{"first", "last"}}
	if got := finalWaitLine(skipped, landWatchResult{}); got != "last" {
		t.Errorf("line = %q; want the last line", got)
	}
	bare := landResult{Repo: "/r", Kind: landPushed}
	if got := finalWaitLine(bare, landWatchResult{}); got != "/r" {
		t.Errorf("line = %q; want the repo", got)
	}
}

type fakeGitRunner struct {
	dirs    []string
	respond func(dir string, args []string) (string, error)
}

func (f *fakeGitRunner) run(dir string, args ...string) (string, error) {
	f.dirs = append(f.dirs, dir)
	return f.respond(dir, args)
}

func TestLandPollPRStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		info prInfo
		want string
	}{
		{"merged state", prInfo{State: "MERGED"}, "merged"},
		{"merged flag", prInfo{Merged: true}, "merged"},
		{"closed state", prInfo{State: "CLOSED"}, "closed"},
		{"closed flag", prInfo{Closed: true}, "closed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := testLandDeps(t)
			d.clock = &fakeClock{now: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)}
			info := tc.info
			d.gh = &ghSeq{infos: []prInfo{info}}
			got, state := pollPR("o", "r", true, landWatchJob{PR: 7}, d)
			if state != tc.want {
				t.Errorf("state = %q; want %q", state, tc.want)
			}
			if got.Merged != info.Merged || got.State != info.State {
				t.Errorf("info = %+v; want %+v", got, info)
			}
		})
	}
}

func TestLandPollPRTimeoutWithoutSlug(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	d.clock = &fakeClock{now: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)}
	d.gh = &fakeGH{}
	info, state := pollPR("", "", false, landWatchJob{PR: 7}, d)
	if state != "timeout" || info != (prInfo{}) {
		t.Errorf("state = %q info = %+v; want timeout with no PR info", state, info)
	}
	if got := d.clock.Now().Sub(time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)); got < landPollCap {
		t.Errorf("waited %v; want at least %v", got, landPollCap)
	}
}

func TestLandWaitForCITimesOutWithNoCI(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	d.clock = &fakeClock{now: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)}
	d.gh = &fakeGH{}
	res := waitForCI(landWatchResult{Repo: "o/r", CI: "none", State: "merged"}, "o", "r", false, "", "", d)
	if res.State != "merged" || res.CI != "none" {
		t.Errorf("result = %+v; want merged with no CI", res)
	}
}

func TestLandArgNames(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	d.git = &fakeGitRunner{respond: func(string, []string) (string, error) { return "", errors.New("no repo") }}
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	absent := filepath.Join(t.TempDir(), "absent")
	if code := runLandWith(withJSONFlag(t.Context(), true), absent, []string{"--json", "--wait"}, d); code != exitcode.LandSkipped {
		t.Errorf("--json --wait = %v stderr=%q", code, stderr.String())
	}
	var jr landJSON
	if err := json.Unmarshal([]byte(stdout.String()), &jr); err != nil {
		t.Fatalf("json output = %q: %v", stdout.String(), err)
	}
	if len(jr.Repos) != 1 || jr.Repos[0].Status != "skipped" {
		t.Errorf("json = %+v", jr)
	}
	for _, args := range [][]string{{"--repos"}, {"--wait", "--repos"}} {
		stderr.Reset()
		if code := runLandWith(t.Context(), absent, args, d); code != exitcode.InvalidUsage {
			t.Errorf("%v code = %v; want InvalidUsage", args, code)
		}
		if !strings.Contains(stderr.String(), "--repos needs a directory") {
			t.Errorf("%v stderr = %q", args, stderr.String())
		}
	}
	stderr.Reset()
	if code := runLandWith(t.Context(), absent, []string{"--bogus"}, d); code != exitcode.InvalidUsage {
		t.Errorf("unknown flag code = %v; want InvalidUsage", code)
	}
	if !strings.Contains(stderr.String(), "unknown argument: --bogus") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestLandPushNeedsBranch(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	if code := runLandWith(t.Context(), "", []string{"--push"}, d); code != exitcode.InvalidUsage {
		t.Errorf("code = %v; want InvalidUsage", code)
	}
	if !strings.Contains(stderr.String(), "--push needs --branch") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestLandFlagsNeedValues(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"--sha", "--branch"} {
		d := testLandDeps(t)
		var stdout, stderr strings.Builder
		d.stdout = &stdout
		d.stderr = &stderr
		if code := runLandWith(t.Context(), "", []string{flag}, d); code != exitcode.InvalidUsage {
			t.Errorf("%s alone code = %v; want InvalidUsage", flag, code)
		}
		if !strings.Contains(stderr.String(), flag+" needs") {
			t.Errorf("%s stderr = %q", flag, stderr.String())
		}
	}
}

type ghSeq struct {
	infos []prInfo
	i     int
}

func (g *ghSeq) branchRuleTypes(string, string) ([]string, error) { return nil, nil }
func (g *ghSeq) squashMergeAllowed(string) (bool, error)          { return false, nil }
func (g *ghSeq) mergeCommitAllowed(string) (bool, error)          { return false, nil }
func (g *ghSeq) createPR(string, string, string) (string, error)  { return "", nil }
func (g *ghSeq) mergePR(string, string, bool, string) error       { return nil }
func (g *ghSeq) prNumber(string, string) (int, error)             { return 0, nil }
func (g *ghSeq) pullRequest(string, string, int) (prInfo, error) {
	if g.i >= len(g.infos) {
		return g.infos[len(g.infos)-1], nil
	}
	info := g.infos[g.i]
	g.i++
	return info, nil
}

func (g *ghSeq) latestWorkflowRun(string, string, string, string) (workflowRun, error) {
	return workflowRun{}, nil
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(d time.Duration) {
	c.now = c.now.Add(d)
}

type fakeGH struct {
	rules    []string
	rulesErr error
	squash   bool
	merge    bool
	url      string
	err      error
	number   int
	prs      []prInfo
	pi       int
	run      workflowRun
	merges   []string
	prErr    error
	// onRules and onCreate run mid-land, where a concurrent session could commit.
	onRules, onCreate func()
}

func (f *fakeGH) branchRuleTypes(string, string) ([]string, error) {
	if f.onRules != nil {
		f.onRules()
	}
	return f.rules, f.rulesErr
}
func (f *fakeGH) squashMergeAllowed(string) (bool, error) { return f.squash, nil }
func (f *fakeGH) mergeCommitAllowed(string) (bool, error) { return f.merge, nil }
func (f *fakeGH) createPR(string, string, string) (string, error) {
	if f.onCreate != nil {
		f.onCreate()
	}
	if f.err != nil {
		return "", f.err
	}
	return f.url, nil
}

func (f *fakeGH) mergePR(_, _ string, auto bool, strategy string) error {
	if auto {
		f.merges = append(f.merges, "auto:"+strategy)
	} else {
		f.merges = append(f.merges, "plain")
	}
	return nil
}
func (f *fakeGH) prNumber(string, string) (int, error) { return f.number, nil }
func (f *fakeGH) pullRequest(string, string, int) (prInfo, error) {
	if f.prErr != nil {
		return prInfo{}, f.prErr
	}
	if len(f.prs) == 0 {
		return prInfo{}, nil
	}
	if f.pi < len(f.prs) {
		info := f.prs[f.pi]
		f.pi++
		return info, nil
	}
	return f.prs[len(f.prs)-1], nil
}

func (f *fakeGH) latestWorkflowRun(string, string, string, string) (workflowRun, error) {
	return f.run, nil
}

func testLandDeps(t *testing.T) landDeps {
	t.Helper()
	cache := t.TempDir()
	d := defaultLandDeps(t.Context(), &strings.Builder{}, &strings.Builder{})
	d.cacheHome = func() string { return cache }
	d.gate = func(string, bool) (string, error) { return "", nil }
	d.passed = func(string, string) bool { return false }
	d.clock = &fakeClock{now: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)}
	d.pid = 1
	return d
}

func repoWithOrigin(t *testing.T) (dir, bare string) {
	t.Helper()
	dir, _ = gittest.RepoWithFile(t.Context(), t, "base.txt", "base\n", "base")
	bare = initBareOrigin(t, dir)
	return dir, bare
}

func initBareOrigin(t *testing.T, dir string) string {
	t.Helper()
	bare := t.TempDir()
	gitOut(t, bare, "init", "--bare", "-b", "main")
	gitOut(t, dir, "remote", "add", "origin", bare)
	gitOut(t, dir, "push", "-u", "origin", "HEAD")
	gitOut(t, dir, "fetch", "origin")
	br := strings.TrimSpace(gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD"))
	gitOut(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+br)
	return bare
}

// assertNoFleetTouch pins that land leaves no fleet-touch inbox behind: nothing reads it any more.
func assertNoFleetTouch(t *testing.T, d landDeps) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(d.cacheHome(), "nomad-hud")); !os.IsNotExist(err) {
		t.Errorf("land wrote under the cache's nomad-hud dir (stat err %v)", err)
	}
}

func TestLandBranchLocalDirtyUnrelatedFileLands(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	tip := strings.TrimSpace(gitOut(t, wt, "rev-parse", "HEAD"))
	originBefore := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD"))
	gittest.Write(t, dir, "base.txt", "base\ndirty\n")

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != tip {
		t.Errorf("local default = %s; want %s", got, tip)
	}
	if got := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD")); got != originBefore {
		t.Errorf("origin moved %s -> %s", originBefore, got)
	}
	body, err := os.ReadFile(filepath.Join(dir, "base.txt")) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "base\ndirty\n" {
		t.Errorf("dirty file = %q", body)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree %s still present: %v", wt, err)
	}
	if out := gitOut(t, dir, "branch", "--list", "feat"); strings.TrimSpace(out) != "" {
		t.Errorf("branch feat still present: %q", out)
	}
}

func TestLandBranchLocalDirtyOverlapExitsDirtyOverlap(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "base.txt", "branch\n")
	gitOut(t, wt, "add", "base.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat touches base")
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	originBefore := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD"))
	gittest.Write(t, dir, "base.txt", "base\ndirty\n")

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
	if code != exitcode.DirtyOverlap {
		t.Fatalf("code = %v stdout=%q stderr=%q; want %v", code, stdout.String(), stderr.String(), exitcode.DirtyOverlap)
	}
	if !strings.Contains(stderr.String(), "base.txt") {
		t.Errorf("stderr does not name the overlapping file: %q", stderr.String())
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != before {
		t.Errorf("HEAD moved %s -> %s", before, got)
	}
	if got := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD")); got != originBefore {
		t.Errorf("origin moved %s -> %s", originBefore, got)
	}
	body, err := os.ReadFile(filepath.Join(dir, "base.txt")) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "base\ndirty\n" {
		t.Errorf("dirty file = %q; want untouched", body)
	}
	if out := gitOut(t, dir, "branch", "--list", "feat"); strings.TrimSpace(out) == "" {
		t.Error("branch feat deleted though it never landed")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("worktree removed though it never landed: %v", err)
	}
}

func TestLandBranchLocalRebasesOntoLocalDefault(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	gittest.Write(t, dir, "local.txt", "unpushed\n")
	gittest.Commit(t.Context(), t, dir, "unpushed local work")
	localTip := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	originTip := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD"))
	if localTip == originTip {
		t.Fatal("the local default has no unpushed commit to rebase onto")
	}
	gittest.Write(t, dir, "unrelated.txt", "scratch\n")

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD~1")); got != localTip {
		t.Errorf("landed branch parent = %s; want the local unpushed tip %s", got, localTip)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s")); got != "feat" {
		t.Errorf("default subject = %q", got)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "origin/main")); got != originTip {
		t.Errorf("origin/main = %s; want unchanged %s", got, originTip)
	}
}

func TestLandShaRefusesUngatableCommit(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gittest.Write(t, dir, "one.txt", "1\n")
	gittest.Commit(t.Context(), t, dir, "one")
	sha := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	gittest.Write(t, dir, "two.txt", "2\n")
	gittest.Commit(t.Context(), t, dir, "two")
	before := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD"))
	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	d.sha = sha
	if code := landRepos(t.Context(), []string{dir}, false, false, false, d); code != 1 || !strings.Contains(stdout.String(), "gated only as HEAD") {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	if got := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD")); got != before {
		t.Error("refused --sha pushed")
	}
}

func TestLandShaDirectPushesOnlyThatCommit(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gittest.Write(t, dir, "one.txt", "1\n")
	gittest.Commit(t.Context(), t, dir, "one")
	sha := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	gittest.Write(t, dir, "two.txt", "2\n")
	gittest.Commit(t.Context(), t, dir, "two")
	head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	gittest.Write(t, dir, "base.txt", "dirty\n")

	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	d.sha = sha
	d.noGate = true
	if code := landRepos(t.Context(), []string{dir}, false, false, false, d); code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	if got := strings.TrimSpace(gitOut(t, bare, "rev-parse", "main")); got != sha {
		t.Errorf("remote main = %s; want %s", got, sha)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != head {
		t.Errorf("HEAD moved to %s; want %s", got, head)
	}
}

func TestLandShaPRKeepsLocalMain(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gitOut(t, dir, "remote", "set-url", "origin", "git@github.com:acme/demo.git")
	gitOut(t, dir, "remote", "set-url", "--push", "origin", bare)
	gittest.Write(t, dir, "one.txt", "1\n")
	gittest.Commit(t.Context(), t, dir, "one")
	sha := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	gittest.Write(t, dir, "two.txt", "2\n")
	gittest.Commit(t.Context(), t, dir, "two")
	head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	gh := &fakeGH{
		rules:  []string{"pull_request"},
		squash: true,
		url:    "https://github.com/acme/demo/pull/5",
		number: 5,
		prs:    []prInfo{{State: "OPEN", HasAutoMerge: true, URL: "https://github.com/acme/demo/pull/5"}},
	}
	d := testLandDeps(t)
	d.gh = gh
	d.clock = &fakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	var stdout strings.Builder
	d.stdout = &stdout
	d.sha = sha
	d.noGate = true
	if code := landRepos(t.Context(), []string{dir}, false, false, false, d); code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	if got := strings.TrimSpace(gitOut(t, bare, "rev-parse", "land/20260102-030405")); got != sha {
		t.Errorf("land branch = %s; want %s", got, sha)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != head {
		t.Errorf("HEAD = %s; want %s (no reset)", got, head)
	}
	if !strings.Contains(stdout.String(), "pull/5") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestLandShaNotAhead(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	d := testLandDeps(t)
	d.sha = "HEAD"
	if res := landOne(dir, d); res.Kind != landSkipped || res.Lines[0] != dir+": skipped (already upstream)" {
		t.Errorf("res = %#v", res)
	}
}

// TestLandBranchPushRefusesToRebaseAfterGate pins that the --branch route
// never rebases onto an origin that moved during the gate: the ungated commit
// would be pushed. Both the clean and the dirty-checkout routes are covered.
func TestLandBranchPushRefusesToRebaseAfterGate(t *testing.T) {
	t.Parallel()
	for _, dirty := range []bool{false, true} {
		t.Run(fmt.Sprintf("dirty=%v", dirty), func(t *testing.T) {
			t.Parallel()
			dir, bare := repoWithOrigin(t)
			wt := filepath.Join(t.TempDir(), "wt")
			gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
			gittest.Write(t, wt, "feat.txt", "feat\n")
			gitOut(t, wt, "add", "feat.txt")
			gitOut(t, wt, "commit", "-q", "-m", "feat")
			if dirty {
				gittest.Write(t, dir, "base.txt", "dirty\n")
			}

			d := testLandDeps(t)
			d.gate = func(string, bool) (string, error) {
				other := filepath.Join(t.TempDir(), "other")
				gitOut(t, t.TempDir(), "clone", "-q", bare, other)
				gitOut(t, other, "config", "user.email", "test@test")
				gitOut(t, other, "config", "user.name", "test")
				gittest.Write(t, other, "moved.txt", "moved\n")
				gitOut(t, other, "add", "moved.txt")
				gitOut(t, other, "commit", "-q", "-m", "moved")
				gitOut(t, other, "push", "-q", "origin", "HEAD")
				return "ok\n", nil
			}
			var stdout, stderr strings.Builder
			d.stdout = &stdout
			d.stderr = &stderr
			code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d)
			if code != 1 {
				t.Fatalf("code = %v stdout=%q stderr=%q; want 1", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "origin moved during the gate; nothing pushed, re-run") {
				t.Errorf("stderr = %q", stderr.String())
			}
			if got := strings.TrimSpace(gitOut(t, bare, "log", "-1", "--pretty=%s")); got != "moved" {
				t.Errorf("origin tip = %q; want the competing commit untouched", got)
			}
		})
	}
}

// moveBeforePush runs hook once, just before the first push, standing in for
// another client landing on origin after land's last look at it.
type moveBeforePush struct {
	gitRunner
	hook func()
	once sync.Once
}

func (m *moveBeforePush) run(dir string, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "push" {
		m.once.Do(m.hook)
	}
	return m.gitRunner.run(dir, args...)
}

// TestLandPlainRefusesToRebaseWhenOriginMoves pins that a rejected plain push
// stops with exit 1 and pushes nothing, instead of rebasing onto the new tip.
func TestLandPlainRefusesToRebaseWhenOriginMoves(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	d.git = &moveBeforePush{gitRunner: d.git, hook: func() {
		other := filepath.Join(t.TempDir(), "other")
		gitOut(t, t.TempDir(), "clone", "-q", bare, other)
		gitOut(t, other, "config", "user.email", "test@test")
		gitOut(t, other, "config", "user.name", "test")
		gittest.Write(t, other, "moved.txt", "moved\n")
		gitOut(t, other, "add", "moved.txt")
		gitOut(t, other, "commit", "-q", "-m", "moved")
		gitOut(t, other, "push", "-q", "origin", "HEAD")
	}}
	if code := runLandWith(t.Context(), dir, nil, d); code != 1 {
		t.Fatalf("code = %v stdout=%q stderr=%q; want 1", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "origin moved; nothing pushed, re-run") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if got := strings.TrimSpace(gitOut(t, bare, "log", "-1", "--pretty=%s")); got != "moved" {
		t.Errorf("origin tip = %q; want the competing commit untouched", got)
	}
}

// TestLandPushedDespiteLockedTrackingRef pins that a push which reached origin
// but failed to update the locked local tracking ref is reported as pushed.
func TestLandPushedDespiteLockedTrackingRef(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	lock := filepath.Join(dir, ".git", "refs", "remotes", "origin", "main.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	if code := runLandWith(t.Context(), dir, nil, d); code != 0 {
		t.Fatalf("code = %v stdout=%q stderr=%q; want 0", code, stdout.String(), stderr.String())
	}
	head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	if got := strings.TrimSpace(gitOut(t, bare, "rev-parse", "main")); got != head {
		t.Errorf("origin main = %s; want %s", got, head)
	}
}

// TestLandRedGateReportsReasonLogAndJSON pins the red line when no gate names match, the
// distinct log per same-named repo, and the reason and log in --json.
func TestLandRedGateReportsReasonLogAndJSON(t *testing.T) {
	t.Parallel()
	a, _ := repoWithOrigin(t)
	b, _ := repoWithOrigin(t)
	for _, dir := range []string{a, b} {
		gittest.Write(t, dir, "ahead.txt", "x\n")
		gittest.Commit(t.Context(), t, dir, "ahead")
	}
	a2 := filepath.Join(t.TempDir(), "cli")
	b2 := filepath.Join(t.TempDir(), "cli")
	if err := os.Rename(a, a2); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(b, b2); err != nil {
		t.Fatal(err)
	}

	d := testLandDeps(t)
	d.gate = func(dir string, _ bool) (string, error) {
		return "", errors.New("exec: \"gate\": executable file not found in $PATH")
	}
	var stdout strings.Builder
	d.stdout = &stdout
	if code := landRepos(t.Context(), []string{a2, b2}, false, true, false, d); code != 1 {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	var got landJSON
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil || len(got.Repos) != 2 {
		t.Fatalf("json = %q err=%v", stdout.String(), err)
	}
	if got.Repos[0].Log == got.Repos[1].Log || got.Repos[0].Log == "" {
		t.Errorf("logs = %q, %q; want two distinct paths", got.Repos[0].Log, got.Repos[1].Log)
	}
	if !strings.Contains(got.Repos[0].Detail, "executable file not found") || strings.Contains(got.Repos[0].Detail, "gate failed: ?") {
		t.Errorf("detail = %q", got.Repos[0].Detail)
	}
	if body, _ := os.ReadFile(got.Repos[0].Log); !strings.Contains(string(body), "executable file not found") {
		t.Errorf("log body = %q", body)
	}
}

func TestLandBranchRejectsNoGate(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	var stderr strings.Builder
	d.stderr = &stderr
	if code := runLandWith(t.Context(), t.TempDir(), []string{"--branch", "feat", "--no-gate"}, d); code != exitcode.InvalidUsage {
		t.Fatalf("code = %v stderr=%q; want usage error", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--no-gate does not combine with --branch") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// TestLandJSONAfterVerb pins that --json after the command emits JSON like the global form.
func TestLandJSONAfterVerb(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	if code := runLandWith(t.Context(), dir, []string{"--json"}, d); code != 0 {
		t.Fatalf("code = %v", code)
	}
	var got landJSON
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil || len(got.Repos) != 1 || got.Repos[0].Status != "skipped" {
		t.Errorf("stdout = %q err=%v; want one skipped JSON repo", stdout.String(), err)
	}
}

// failAfterPush runs a real push, then reports it failed, like git failing only the
// local tracking-ref update.
type failAfterPush struct{ gitRunner }

func (f failAfterPush) run(dir string, args ...string) (string, error) {
	out, err := f.gitRunner.run(dir, args...)
	if err == nil && len(args) > 0 && args[0] == "push" {
		return out + "error: update_ref failed", errors.New("exit status 1")
	}
	return out, err
}

// TestLandPushedWhenPushErrsAfterReachingRemote pins the ls-remote check on the plain route.
func TestLandPushedWhenPushErrsAfterReachingRemote(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	d := testLandDeps(t)
	d.git = failAfterPush{execGit{}}
	var stdout strings.Builder
	d.stdout = &stdout
	if code := landRepos(t.Context(), []string{dir}, false, false, false, d); code != 0 || !strings.Contains(stdout.String(), ": pushed ") {
		t.Fatalf("code = %v stdout=%q; want pushed", code, stdout.String())
	}
	head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	if got := strings.TrimSpace(gitOut(t, bare, "rev-parse", "main")); got != head {
		t.Errorf("origin main = %s; want %s", got, head)
	}
}

// TestLandPushesToUpstreamRemote pins that a branch tracking a remote other than origin
// is pushed, and checked, there.
func TestLandPushesToUpstreamRemote(t *testing.T) {
	t.Parallel()
	dir, originBare := repoWithOrigin(t)
	up := t.TempDir()
	gitOut(t, up, "init", "--bare", "-b", "main")
	gitOut(t, dir, "remote", "add", "up", up)
	gitOut(t, dir, "push", "-u", "up", "HEAD:main")
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	originBefore := strings.TrimSpace(gitOut(t, originBare, "rev-parse", "main"))

	d := testLandDeps(t)
	d.git = failAfterPush{execGit{}}
	var stdout strings.Builder
	d.stdout = &stdout
	if code := landRepos(t.Context(), []string{dir}, false, false, false, d); code != 0 {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	if got := strings.TrimSpace(gitOut(t, up, "rev-parse", "main")); got != head {
		t.Errorf("upstream main = %s; want %s", got, head)
	}
	if got := strings.TrimSpace(gitOut(t, originBare, "rev-parse", "main")); got != originBefore {
		t.Errorf("origin moved to %s", got)
	}
}

// TestLandPRLookupFailureIsUnknownState pins that a failed PR lookup is
// reported as unknown, not as "neither queued nor auto-merge", and that the
// queue route does not treat it as a failed enqueue.
func TestLandPRLookupFailureIsUnknownState(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gitOut(t, dir, "remote", "set-url", "origin", "git@github.com:acme/demo.git")
	gitOut(t, dir, "remote", "set-url", "--push", "origin", bare)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")

	d := testLandDeps(t)
	d.gh = &fakeGH{
		rules:  []string{"pull_request", "merge_queue"},
		url:    "https://github.com/acme/demo/pull/4",
		number: 4,
		prErr:  errors.New("api down"),
	}
	res := landOne(dir, d)
	if res.Kind != landPR || res.PRState != prStateUnknown {
		t.Fatalf("kind = %v state = %q lines=%v; want PR with unknown state", res.Kind, res.PRState, res.Lines)
	}
	if got := strings.Join(res.Lines, "\n"); !strings.Contains(got, "PR state unknown") || strings.Contains(got, "neither queued") {
		t.Errorf("lines = %q", got)
	}
}

// TestMergeOverlapFilesNonASCII pins that a dirty file whose name git would
// quote (non-ASCII) still counts as overlapping the branch's changes.
func TestMergeOverlapFilesNonASCII(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.RepoWithFile(t.Context(), t, "é ü.txt", "base\n", "base")
	gitOut(t, dir, "branch", "feat")
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", wt, "feat")
	gittest.Write(t, wt, "é ü.txt", "feat\n")
	gitOut(t, wt, "add", "é ü.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	gittest.Write(t, dir, "é ü.txt", "dirty\n")

	got := mergeOverlapFiles(dir, "feat", testLandDeps(t))
	if len(got) != 1 || got[0] != "é ü.txt" {
		t.Errorf("overlap = %q; want [é ü.txt]", got)
	}
}

// TestIsLinkedWorktree pins the git-dir vs common-dir comparison: a main
// checkout living under a directory named "worktrees" is not a linked one.
func TestIsLinkedWorktree(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	dir, _ := gittest.RepoWithFile(t.Context(), t, "a.txt", "a\n", "base")
	nested := filepath.Join(t.TempDir(), "worktrees", "main")
	gitOut(t, t.TempDir(), "clone", "-q", dir, nested)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	for _, tc := range []struct {
		path string
		want bool
	}{{dir, false}, {nested, false}, {wt, true}} {
		if got := isLinkedWorktree(tc.path, d); got != tc.want {
			t.Errorf("isLinkedWorktree(%s) = %t; want %t", tc.path, got, tc.want)
		}
	}
}

func pushMergedPR(t *testing.T, bare, subject string) (tip string) {
	t.Helper()
	other := t.TempDir()
	gitOut(t, other, "clone", bare, ".")
	gitOut(t, other, "config", "user.name", "test")
	gitOut(t, other, "config", "user.email", "test@test")
	gittest.Write(t, other, "Makefile", ".PHONY: install\ninstall:\n\t@echo caught > installed.marker\n\t@echo caught\n")
	gittest.Commit(t.Context(), t, other, subject)
	gitOut(t, other, "push", "origin", "HEAD")
	return strings.TrimSpace(gitOut(t, other, "rev-parse", "HEAD"))
}

func TestCatchUpFastForwardsAndInstalls(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	tip := pushMergedPR(t, bare, "feat: thing (#51)")
	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	if failed, _ := catchUp(t.Context(), dir, d); failed {
		t.Fatalf("install failed: %q", stdout.String())
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != tip {
		t.Errorf("HEAD = %s; want %s", got, tip)
	}
	out := stdout.String()
	if !strings.Contains(out, dir+": caught up to "+tip[:7]) || !strings.Contains(out, "(merged PR #51)\n") {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(out, dir+": installed caught\n") {
		t.Errorf("missing install line: %q", out)
	}
}

func TestCatchUpLeavesDivergedAndUnrelated(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	pushMergedPR(t, bare, "feat: thing (#52)")
	gittest.Write(t, dir, "local.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "local work")
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout, d.stderr = &stdout, &stderr
	_, _ = catchUp(t.Context(), dir, d)
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != before {
		t.Errorf("diverged HEAD moved to %s", got)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("a diverged default branch printed: stdout = %q stderr = %q", stdout.String(), stderr.String())
	}
}

func TestCatchUpRebaseMergeWithoutPRNumber(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	tip := pushMergedPR(t, bare, "chore: direct push")
	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	_, _ = catchUp(t.Context(), dir, d)
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != tip {
		t.Errorf("HEAD = %s; want %s", got, tip)
	}
	if want := dir + ": caught up to " + tip[:7] + "\n"; !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout = %q; want %q", stdout.String(), want)
	}
}

func TestCatchUpLeavesDirtyClone(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	pushMergedPR(t, bare, "chore: direct push")
	gittest.Write(t, dir, "base.txt", "dirty\n")
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	_, _ = catchUp(t.Context(), dir, d)
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != before || stdout.Len() != 0 {
		t.Errorf("dirty clone caught up: HEAD %s out %q", got, stdout.String())
	}
}

// TestLandJSONOneObject pins --json: catch-up and install lines stay off stdout, the object carries
// the pushed sha and each skip's reason.
func TestLandJSONOneObject(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	pushMergedPR(t, bare, "feat: thing (#61)")
	gitOut(t, dir, "pull", "-q", "--ff-only")
	gittest.Write(t, dir, "Makefile", ".PHONY: install\ninstall:\n\t@echo ok\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	d := testLandDeps(t)
	d.asJSON = true
	var stdout, stderr strings.Builder
	d.stdout, d.stderr = &stdout, &stderr
	loose := t.TempDir()
	if code := landRepos(t.Context(), []string{dir, loose}, false, true, true, d); code != exitcode.LandSkipped {
		t.Fatalf("code = %v stderr=%q", code, stderr.String())
	}
	var got landJSON
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%q", err, stdout.String())
	}
	head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	if len(got.Repos) != 2 || got.Repos[0].SHA != head || got.Repos[1].Detail != "not a git repo" {
		t.Errorf("repos = %+v; want sha %s and a skip reason", got.Repos, head)
	}
	if !strings.Contains(stderr.String(), ": installed ok") {
		t.Errorf("install line missing from stderr: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), ": pushed "+head) {
		t.Errorf("pushed line missing from stderr: %q", stderr.String())
	}
}

// TestGateHeadRefusesDirtyTree pins that an edit made after land's dirty check is never gated and pushed over.
func TestGateHeadRefusesDirtyTree(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, "base.txt", "sibling edit\n")
	d := testLandDeps(t)
	gated := false
	d.gate = func(string, bool) (string, error) {
		gated = true
		return "", nil
	}
	line, _, ok := gateHead(dir, d)
	if ok || gated || !strings.Contains(line, "tracked files changed before the gate (base.txt); nothing pushed") {
		t.Errorf("ok=%v gated=%v line=%q", ok, gated, line)
	}
}

// TestLandWaitKeepsPRLine pins that --wait leaves the summary PR line ahead of the final CI line.
func TestLandWaitKeepsPRLine(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	d.gh = &fakeGH{
		prs: []prInfo{{State: "OPEN", URL: "https://github.com/acme/demo/pull/4"}},
		run: workflowRun{Status: "completed", Conclusion: "success"},
	}
	var stdout strings.Builder
	d.stdout = &stdout
	res := landResult{
		Repo: t.TempDir(), Kind: landPR, PRState: "queued", PRNumber: 4, Slug: "acme/demo", Default: "main",
		URL:   "https://github.com/acme/demo/pull/4",
		Lines: []string{"r: PR https://github.com/acme/demo/pull/4 (queued)"},
	}
	finishLandRepos(t.Context(), []landResult{res}, true, false, false, false, false, false, false, d)
	out := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(out) != 2 || out[0] != res.Lines[0] || !strings.Contains(out[1], "; CI ") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestLandBranchJSON(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "f.txt", "f\n")
	gitOut(t, wt, "add", "f.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout, d.stderr = &stdout, &stderr
	if code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--json"}, d); code != exitcode.Success {
		t.Fatalf("code = %v stderr=%q", code, stderr.String())
	}
	var got landBranchJSON
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil || got.Branch != "feat" || got.Route != "local" || got.SHA == "" {
		t.Errorf("stdout = %q (%v)", stdout.String(), err)
	}
}

// TestLandHonestErrors pins the wording of a gate that edits tracked files, an origin that moved, and a dirty branch worktree.
func TestLandHonestErrors(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	d := testLandDeps(t)
	d.gate = func(dir string, _ bool) (string, error) {
		gittest.Write(t, dir, "base.txt", "gate edit\n")
		return "", nil
	}
	var stdout strings.Builder
	d.stdout = &stdout
	if code := landRepos(t.Context(), []string{dir}, false, false, false, d); code != 1 ||
		!strings.Contains(stdout.String(), "gate modified tracked files (base.txt)") || strings.Contains(stdout.String(), "HEAD moved") {
		t.Errorf("tree-only change: code=%v stdout=%q", code, stdout.String())
	}
	gitOut(t, dir, "checkout", "-q", "--", "base.txt")

	d.gate = func(string, bool) (string, error) {
		other := t.TempDir()
		gitOut(t, other, "clone", "-q", bare, "o")
		o := filepath.Join(other, "o")
		gittest.Write(t, o, "moved.txt", "m\n")
		gitOut(t, o, "add", "moved.txt")
		gitOut(t, o, "-c", "user.name=rgit test", "-c", "user.email=rgit@example.invalid", "commit", "-q", "-m", "moved")
		gitOut(t, o, "push", "-q", "origin", "HEAD")
		return "", nil
	}
	var stderr strings.Builder
	d.stdout, d.stderr = &stdout, &stderr
	if code := landRepos(t.Context(), []string{dir}, false, false, false, d); code != 1 || !strings.HasPrefix(stderr.String(), dir+": error: origin moved") {
		t.Errorf("origin moved: code=%v stderr=%q", code, stderr.String())
	}

	dir2, _ := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir2, "worktree", "add", "-b", "d1", wt)
	gittest.Write(t, wt, "base.txt", "unstaged\n")
	var stderr2 strings.Builder
	d2 := testLandDeps(t)
	d2.stderr = &stderr2
	if code := runLandWith(t.Context(), dir2, []string{"--branch", "d1"}, d2); code != exitcode.LandSkipped ||
		!strings.Contains(stderr2.String(), wt) || !strings.Contains(stderr2.String(), "base.txt") || strings.Contains(stderr2.String(), "conflict") {
		t.Errorf("dirty worktree: code=%v stderr=%q", code, stderr2.String())
	}
}

// TestLandSkipExitCodes pins that a skip the user must fix exits 14 while "not ahead" stays 0.
func TestLandSkipExitCodes(t *testing.T) {
	t.Parallel()
	even, _ := repoWithOrigin(t)
	d := testLandDeps(t)
	d.stdout = &strings.Builder{}
	if code := landRepos(t.Context(), []string{even}, false, false, false, d); code != exitcode.Success {
		t.Errorf("not ahead = %v; want 0", code)
	}
	dirty, _ := repoWithOrigin(t)
	gittest.Write(t, dirty, "base.txt", "changed\n")
	if code := landRepos(t.Context(), []string{dirty}, false, false, false, d); code != exitcode.LandSkipped {
		t.Errorf("dirty = %v; want 14", code)
	}
}

// TestLandBranchGateEditRefused pins that a gate that edits a tracked file in the
// worktree lands nothing, as plain land does.
func TestLandBranchGateEditRefused(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	d := testLandDeps(t)
	d.gate = func(dir string, _ bool) (string, error) {
		gittest.Write(t, dir, "feat.txt", "gate edit\n")
		return "", nil
	}
	var stdout, stderr strings.Builder
	d.stdout, d.stderr = &stdout, &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
	if code != exitcode.LandSkipped || !strings.Contains(stderr.String(), "gate modified tracked files (feat.txt)") {
		t.Fatalf("code = %v stderr=%q; want LandSkipped with gate-modified refusal", code, stderr.String())
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != before {
		t.Error("ungated branch landed on main")
	}
}

// TestLandShaSkips pins that --sha skips like plain land: no upstream and a dirty tree
// are skips that need action, not a green or a red.
func TestLandShaSkips(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, line string }{
		{"no upstream", ": skipped (no upstream)"},
		{"dirty tree", ": skipped (dirty tracked files: base.txt)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir, bare := repoWithOrigin(t)
			gittest.Write(t, dir, "one.txt", "1\n")
			gittest.Commit(t.Context(), t, dir, "one")
			sha := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
			if c.name == "no upstream" {
				gitOut(t, dir, "branch", "--unset-upstream")
			} else {
				gittest.Write(t, dir, "base.txt", "dirty\n")
			}
			before := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD"))
			d := testLandDeps(t)
			var stdout strings.Builder
			d.stdout = &stdout
			d.sha = sha
			code := landRepos(t.Context(), []string{dir}, false, false, false, d)
			if code != exitcode.LandSkipped || !strings.Contains(stdout.String(), c.line) {
				t.Fatalf("code = %v stdout=%q; want 14 with %q", code, stdout.String(), c.line)
			}
			if got := strings.TrimSpace(gitOut(t, bare, "rev-parse", "HEAD")); got != before {
				t.Error("skipped --sha pushed")
			}
		})
	}
}

func TestHasPrePushHook(t *testing.T) {
	dir, _ := gittest.New(t.Context(), t)
	if hasPrePushHook(t.Context(), dir) {
		t.Fatal("a repo with no hook reports one")
	}
	gittest.Write(t, dir, ".git/hooks/pre-push", "#!/bin/sh\n")
	if !hasPrePushHook(t.Context(), dir) {
		t.Fatal("an installed pre-push hook is not seen")
	}
}

// A pre-push hook that runs gate must find land's pass: both gate the same root.
func TestLandBranchPushHookReusesGatePass(t *testing.T) {
	if _, err := exec.LookPath("gate"); err != nil {
		t.Skip("gate not installed")
	}
	if help, _ := exec.CommandContext(t.Context(), "gate", "--help").CombinedOutput(); !strings.Contains(string(help), "--fuzz") {
		t.Skip("installed gate has no --fuzz")
	}
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, ".gate.toml", "[gates.ok]\nrun = \"true\"\n")
	gitOut(t, dir, "add", ".gate.toml")
	gitOut(t, dir, "commit", "-q", "-m", "gate config")
	gitOut(t, dir, "push", "-q")
	hookLog := filepath.Join(t.TempDir(), "hook.log")
	gittest.InstallHook(t.Context(), t, dir, "pre-push", "#!/bin/sh\ngate >"+hookLog+" 2>&1\n")
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")

	d := testLandDeps(t)
	d.gate = func(dir string, p bool) (string, error) { return runGateCmd(t.Context(), dir, p) }
	var stdout, stderr strings.Builder
	d.stdout, d.stderr = &stdout, &stderr
	if code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d); code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	b, err := os.ReadFile(hookLog) //nolint:gosec // path from t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "unchanged") {
		t.Errorf("hook re-ran the gate: %q", b)
	}
}

func TestLandOneRefusesUntrackedFiles(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	gittest.Write(t, dir, "helper.txt", "h\n")
	res := landOne(dir, testLandDeps(t))
	if res.Kind != landRed || !strings.Contains(strings.Join(res.Lines, "\n"), "helper.txt") {
		t.Errorf("kind = %v lines = %v; want red naming helper.txt", res.Kind, res.Lines)
	}
}

func TestLookupRulesCache(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	var stderr strings.Builder
	d.stderr = &stderr
	gh := &fakeGH{rules: []string{"pull_request"}}
	d.gh = gh

	if got := lookupRules("acme/demo", "main", d); len(got) != 1 || got[0] != "pull_request" {
		t.Fatalf("success = %v", got)
	}
	gh.rules, gh.rulesErr = nil, errors.New("offline")
	if got := lookupRules("acme/demo", "main", d); len(got) != 1 || got[0] != "pull_request" {
		t.Fatalf("cached fallback = %v", got)
	}
	if !strings.Contains(stderr.String(), "using rules cached") {
		t.Errorf("stderr = %q", stderr.String())
	}
	stderr.Reset()
	if got := lookupRules("acme/other", "main", d); got != nil {
		t.Fatalf("uncached = %v", got)
	}
	if !strings.Contains(stderr.String(), "none cached; pushing direct") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// TestLandSIGINTDuringGateExits130 pins that SIGINT while the gate is running
// exits 130 with one line naming that step, not the gate's ordinary failure code.
func TestLandSIGINTDuringGateExits130(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	started := filepath.Join(t.TempDir(), "started")
	// A stand-in gate on PATH, so the test needs no installed gate and the gate step is the one interrupted.
	bin := t.TempDir()
	gittest.Write(t, bin, "gate", "#!/bin/sh\ntouch '"+started+"'\nexec sleep 30\n")
	if err := os.Chmod(filepath.Join(bin, "gate"), 0o755); err != nil { //nolint:gosec // an executable test stub
		t.Fatal(err)
	}
	gittest.Write(t, dir, "slow.txt", "x\n")
	gitOut(t, dir, "add", "slow.txt")
	gitOut(t, dir, "commit", "-q", "-m", "slow gate")

	cmd := exec.CommandContext(t.Context(), os.Args[0]) //nolint:gosec // the test binary, intercepted in TestMain
	cmd.Env = append(os.Environ(),
		"RGIT_TEST_LAND_SIGINT_DIR="+dir,
		"XDG_CACHE_HOME="+t.TempDir(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case err := <-waited:
			t.Fatalf("land exited before the gate started: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("gate never started; stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := <-waited
	code := 0
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("wait: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
	}
	if code != int(exitcode.Interrupted) {
		t.Errorf("exit = %d; want %d; stdout=%q stderr=%q", code, exitcode.Interrupted, stdout.String(), stderr.String())
	}
	got := strings.TrimSpace(stderr.String())
	if got != "rgit land: interrupted (gate)" {
		t.Errorf("stderr = %q; want %q", got, "rgit land: interrupted (gate)")
	}
}

func TestLandCancelledContextStopsChildrenAndPolling(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d := defaultLandDeps(ctx, &strings.Builder{}, &strings.Builder{})
	if _, err := d.git.run("", "--version"); err == nil {
		t.Error("git ran under a cancelled context")
	}
	if _, err := d.gate(t.TempDir(), false); err == nil {
		t.Error("gate ran under a cancelled context")
	}
	if _, err := runGH(ctx, "", "--version"); err == nil {
		t.Error("gh ran under a cancelled context")
	}
	start := time.Now()
	d.clock.Sleep(time.Minute)
	if time.Since(start) > 5*time.Second {
		t.Error("Sleep ignored the cancelled context")
	}
	d.clock = &fakeClock{now: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)}
	d.gh = &fakeGH{}
	if _, state := pollPR("", "", false, landWatchJob{PR: 7}, d); state != "cancelled" {
		t.Errorf("state = %q; want cancelled", state)
	}
	if got := d.clock.Now().Sub(time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)); got >= landPollCap {
		t.Errorf("polled %v after cancel", got)
	}
}

// TestLandBranchPushCleanMainBehindOrigin pins that a clean main that fell behind origin is
// brought up to it before the branch is rebased, so the push is not rejected as an origin move.
func TestLandBranchPushCleanMainBehindOrigin(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	other := filepath.Join(t.TempDir(), "other")
	gitOut(t, t.TempDir(), "clone", "-q", bare, other)
	gitOut(t, other, "config", "user.email", "test@test")
	gitOut(t, other, "config", "user.name", "test")
	gittest.Write(t, other, "moved.txt", "moved\n")
	gitOut(t, other, "add", "moved.txt")
	gitOut(t, other, "commit", "-q", "-m", "moved")
	gitOut(t, other, "push", "-q", "origin", "HEAD")

	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout, d.stderr = &stdout, &stderr
	if code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d); code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := strings.TrimSpace(gitOut(t, bare, "log", "-1", "--pretty=%s")); got != "feat" {
		t.Errorf("origin tip = %q; want feat on top of the competing commit", got)
	}
}

// TestLandUnqueuedPRExitsNotEnqueued pins that a PR neither queued nor set to auto-merge exits 4
// under --sha and --branch --push, and that --branch leaves the local main where it was.
func TestLandUnqueuedPRExitsNotEnqueued(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"sha", "branch"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir, bare := repoWithOrigin(t)
			gitOut(t, dir, "remote", "set-url", "origin", "git@github.com:acme/demo.git")
			gitOut(t, dir, "remote", "set-url", "--push", "origin", bare)
			args := []string{"--sha", "HEAD"}
			var tip string
			if mode == "sha" {
				gittest.Write(t, dir, "ahead.txt", "x\n")
				gittest.Commit(t.Context(), t, dir, "ahead")
			} else {
				wt := filepath.Join(t.TempDir(), "wt")
				gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
				gittest.Write(t, wt, "feat.txt", "feat\n")
				gitOut(t, wt, "add", "feat.txt")
				gitOut(t, wt, "commit", "-q", "-m", "feat")
				args = []string{"--branch", "feat", "--push"}
			}
			d := testLandDeps(t)
			d.gh = &fakeGH{
				rules:  []string{"pull_request"},
				url:    "https://github.com/acme/demo/pull/4",
				number: 4,
				prs:    []prInfo{{State: "OPEN", URL: "https://github.com/acme/demo/pull/4"}},
			}
			var stdout, stderr strings.Builder
			d.stdout, d.stderr = &stdout, &stderr
			if code := runLandWith(t.Context(), dir, args, d); code != exitcode.LandNotEnqueued {
				t.Fatalf("code = %v stdout=%q stderr=%q; want %v", code, stdout.String(), stderr.String(), exitcode.LandNotEnqueued)
			}
			if mode == "branch" {
				tip = strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s"))
				if tip != "feat" {
					t.Errorf("local main reset to %q although the PR is not enqueued", tip)
				}
			}
		})
	}
}

// TestLandWaitSkipsUnqueuedPR pins that --wait does not poll a PR that is neither queued nor set to auto-merge.
func TestLandWaitSkipsUnqueuedPR(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gitOut(t, dir, "remote", "set-url", "origin", "git@github.com:acme/demo.git")
	gitOut(t, dir, "remote", "set-url", "--push", "origin", bare)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	d := testLandDeps(t)
	start := d.clock.Now()
	d.gh = &fakeGH{
		rules:  []string{"pull_request"},
		url:    "https://github.com/acme/demo/pull/4",
		number: 4,
		prs:    []prInfo{{State: "OPEN", URL: "https://github.com/acme/demo/pull/4"}},
	}
	var stdout, stderr strings.Builder
	d.stdout, d.stderr = &stdout, &stderr
	if code := runLandWith(t.Context(), dir, []string{"--wait"}, d); code != exitcode.LandNotEnqueued {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if waited := d.clock.Now().Sub(start); waited != 0 {
		t.Errorf("--wait polled an unqueued PR for %v", waited)
	}
}

// TestLandBranchJSONOnFailure pins that --branch --json leaves one object with the exit code and gate log on a red gate.
func TestLandBranchJSONOnFailure(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "f.txt", "f\n")
	gitOut(t, wt, "add", "f.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")
	d := testLandDeps(t)
	d.gate = func(string, bool) (string, error) { return "gate: failed: unit\n", errors.New("exit 1") }
	var stdout, stderr strings.Builder
	d.stdout, d.stderr = &stdout, &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--json"}, d)
	var got landBranchJSON
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("stdout = %q (%v)", stdout.String(), err)
	}
	if code != exitcode.LandGateRed || got.Code != int(code) || got.Branch != "feat" || got.Log == "" {
		t.Errorf("code = %v object = %+v", code, got)
	}
}

// countFetch counts the fetches a land makes.
type countFetch struct {
	gitRunner
	n *int
}

func (c countFetch) run(dir string, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "fetch" {
		*c.n++
	}
	return c.gitRunner.run(dir, args...)
}

// TestLandFetchesOncePerRepo pins that landOne reuses the fetch catchUp just made.
func TestLandFetchesOncePerRepo(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	d := testLandDeps(t)
	fetches := 0
	d.git = countFetch{d.git, &fetches}
	var stdout, stderr strings.Builder
	d.stdout, d.stderr = &stdout, &stderr
	if code := runLandWith(t.Context(), dir, nil, d); code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if fetches != 1 {
		t.Errorf("fetches = %d; want 1", fetches)
	}
}

func TestExecGitDisablesFsmonitor(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.RepoWithFile(t.Context(), t, "base.txt", "base\n", "base")
	marker := filepath.Join(t.TempDir(), "ran")
	hook := filepath.Join(t.TempDir(), "fsmonitor.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\nprintf '\\0'\n"), 0o700); err != nil { //nolint:gosec // test fixture script
		t.Fatal(err)
	}
	gitOut(t, dir, "config", "core.fsmonitor", hook)
	if _, err := (execGit{}).run(dir, "status", "--porcelain=v1", "-uno"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("land's git runner ran the repo's core.fsmonitor program")
	}
	hasPrePushHook(t.Context(), dir)
	if _, err := os.Stat(marker); err == nil {
		t.Error("hasPrePushHook ran the repo's core.fsmonitor program")
	}
}

func TestDirtySkipLineNamesRealPaths(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.RepoWithFile(t.Context(), t, "a b.txt", "x\n", "base")
	gittest.Write(t, dir, "café.txt", "x\n")
	gittest.Write(t, dir, "old.txt", "x\n")
	gitOut(t, dir, "add", ".")
	gitOut(t, dir, "commit", "-q", "-m", "more")
	gittest.Write(t, dir, "a b.txt", "changed\n")
	gittest.Write(t, dir, "café.txt", "changed\n")
	gitOut(t, dir, "mv", "old.txt", "new.txt")
	want := dir + ": skipped (dirty tracked files: a b.txt, café.txt, new.txt)"
	if got := dirtySkipLine(dir, testLandDeps(t)); got != want {
		t.Errorf("dirtySkipLine = %q; want %q", got, want)
	}
}

// TestLandFastForwardUsesUpstreamRemote pins that the post-merge fast-forward reads the remote the default branch tracks, not a hard-coded origin.
func TestLandFastForwardUsesUpstreamRemote(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gitOut(t, dir, "remote", "rename", "origin", "up")
	other := t.TempDir()
	gitOut(t, other, "clone", bare, ".")
	gitOut(t, other, "config", "user.name", "test")
	gitOut(t, other, "config", "user.email", "test@test")
	gittest.Write(t, other, "merged.txt", "from-pr\n")
	gittest.Commit(t.Context(), t, other, "merged via pr")
	gitOut(t, other, "push", "origin", "HEAD")
	tip := strings.TrimSpace(gitOut(t, other, "rev-parse", "HEAD"))
	var stderr strings.Builder
	d := testLandDeps(t)
	d.stderr = &stderr
	fastForwardDefaultAfterMerge(landWatchJob{Repo: dir, Default: "main"}, d)
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != tip {
		t.Errorf("HEAD = %s, want %s; stderr=%q", got, tip, stderr.String())
	}
}

// TestCleanupLandBranchKeepsResetWarning pins that resetToRemote's warning reaches the caller's result on the --branch PR route.
func TestCleanupLandBranchKeepsResetWarning(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	d := testLandDeps(t)
	prep := landBranchPrep{repo: dir, def: "main", gated: strings.Repeat("a", 40), wt: dir}
	res := landResult{Kind: landPR, PRState: "queued", Route: "pr"}
	cleanupLandBranch(prep, &res, false, d)
	if len(res.Lines) != 1 || !strings.Contains(res.Lines[0], "HEAD moved since the PR was opened") {
		t.Errorf("lines = %q", res.Lines)
	}
}

// TestLandBranchResetUsesUpstreamRemote pins that the --branch PR route resets to the remote the default branch tracks, not origin.
func TestLandBranchResetUsesUpstreamRemote(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	gitOut(t, dir, "remote", "rename", "origin", "up")
	head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	d := testLandDeps(t)
	prep := landBranchPrep{repo: dir, def: "main", gated: strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")), wt: dir}
	res := landResult{Kind: landPR, PRState: "queued", Route: "pr"}
	cleanupLandBranch(prep, &res, false, d)
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); got != head || len(res.Lines) != 0 {
		t.Errorf("HEAD = %s want %s; lines=%q", got, head, res.Lines)
	}
}

// TestLandNamesKeepSpacesAndAccents pins that file names in land's refusals come from -z output, not whitespace splits of git's quoted lines.
func TestLandNamesKeepSpacesAndAccents(t *testing.T) {
	t.Parallel()
	const name = "café notes.txt"
	dir, _ := repoWithOrigin(t)
	gittest.Write(t, dir, name, "x\n")
	line, _, ok := gateHead(dir, testLandDeps(t))
	if ok || !strings.Contains(line, "("+name+")") {
		t.Errorf("untracked: ok=%v line=%q", ok, line)
	}

	dir, _ = repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, name, "branch\n")
	gitOut(t, wt, "add", name)
	gitOut(t, wt, "commit", "-q", "-m", "branch")
	gittest.Write(t, dir, name, "mainline\n")
	gittest.Commit(t.Context(), t, dir, "mainline")
	d := testLandDeps(t)
	var stderr strings.Builder
	d.stderr = &stderr
	if code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--push"}, d); code != 2 {
		t.Fatalf("code = %v stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "files "+name+")") {
		t.Errorf("conflict stderr = %q", stderr.String())
	}
}

// TestLandPushRefusedWhenGateEditsTree pins that a tracked file edited while the gate runs is never pushed over.
func TestLandPushRefusedWhenGateEditsTree(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	gittest.Write(t, dir, "ahead.txt", "x\n")
	gittest.Commit(t.Context(), t, dir, "ahead")
	before := strings.TrimSpace(gitOut(t, bare, "rev-parse", "main"))
	d := testLandDeps(t)
	d.gate = func(dir string, _ bool) (string, error) {
		gittest.Write(t, dir, "base.txt", "sibling edit during the gate\n")
		return "", nil
	}
	var stdout strings.Builder
	d.stdout = &stdout
	if code := landRepos(t.Context(), []string{dir}, false, false, false, d); code != exitcode.LandRed {
		t.Errorf("code = %v stdout=%q", code, stdout.String())
	}
	if after := strings.TrimSpace(gitOut(t, bare, "rev-parse", "main")); after != before {
		t.Errorf("origin main moved to %s; want %s", after, before)
	}
}

// TestGateHeadReusesRecordedPass pins that a recorded pass of this exact HEAD skips the gate with one line, and any other HEAD gates.
func TestGateHeadReusesRecordedPass(t *testing.T) {
	t.Parallel()
	dir, _ := repoWithOrigin(t)
	head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	gated := 0
	d.gate = func(string, bool) (string, error) {
		gated++
		return "", nil
	}
	d.passed = func(_, h string) bool { return strings.TrimSpace(h) == head }
	if _, _, ok := gateHead(dir, d); !ok || gated != 0 || !strings.Contains(stdout.String(), "reused the recorded pass of "+head[:7]) {
		t.Errorf("recorded pass: ok=%v gated=%d out=%q", ok, gated, stdout.String())
	}
	d.passed = func(string, string) bool { return false }
	if _, _, ok := gateHead(dir, d); !ok || gated != 1 {
		t.Errorf("no record: ok=%v gated=%d", ok, gated)
	}
	d.passed = func(string, string) bool { return true }
	gittest.Write(t, dir, "base.txt", "dirty\n")
	if _, _, ok := gateHead(dir, d); ok || gated != 1 {
		t.Errorf("dirty tree: ok=%v gated=%d", ok, gated)
	}
}

// pinnedPair makes app, whose ahead commit pins lib's ahead commit by git+ssh SHA, and lib.
func pinnedPair(t *testing.T) (app, lib string) {
	t.Helper()
	lib, _ = repoWithOrigin(t)
	gittest.Write(t, lib, "lib.txt", "x\n")
	gittest.Commit(t.Context(), t, lib, "lib change")
	sha := strings.TrimSpace(gitOut(t, lib, "rev-parse", "HEAD"))
	app, _ = repoWithOrigin(t)
	gittest.Write(t, app, "package.json", `{"dependencies":{"lib":"git+ssh://git@github.com/acme/lib.git#`+sha+`"}}`+"\n")
	gittest.Commit(t.Context(), t, app, "pin lib")
	return app, lib
}

// TestLandPushesPinnedRepoFirst pins that a repo whose package.json pins another run repo's unpushed commit lands after it.
func TestLandPushesPinnedRepoFirst(t *testing.T) {
	t.Parallel()
	app, lib := pinnedPair(t)
	d := testLandDeps(t)
	var stdout strings.Builder
	d.stdout = &stdout
	if code := landRepos(t.Context(), []string{app, lib}, false, false, false, d); code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	out := stdout.String()
	if li, ai := strings.Index(out, lib+": pushed"), strings.Index(out, app+": pushed"); li < 0 || ai < 0 || li > ai {
		t.Errorf("lib must be pushed before app: %q", out)
	}
}

// TestLandSkipsConsumerOfRedPinnedRepo pins that a consumer is not pushed when the repo it pins went red in the run.
func TestLandSkipsConsumerOfRedPinnedRepo(t *testing.T) {
	t.Parallel()
	app, lib := pinnedPair(t)
	d := testLandDeps(t)
	d.gate = func(dir string, _ bool) (string, error) {
		if dir == lib {
			return "gate: failed: unit -> /x.log\n", errors.New("exit 1")
		}
		return "", nil
	}
	var stdout strings.Builder
	d.stdout = &stdout
	if code := landRepos(t.Context(), []string{app, lib}, false, false, false, d); code != exitcode.LandRed {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), app+": skipped (pinned repo "+lib+" is red)") || strings.Contains(stdout.String(), app+": pushed") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

// TestLandDryRunPlansWithoutActing pins --dry-run: run order, route, gate status and ahead commits, with no gate, push or catch-up.
func TestLandDryRunPlansWithoutActing(t *testing.T) {
	t.Parallel()
	app, lib := pinnedPair(t)
	libTip := strings.TrimSpace(gitOut(t, lib, "rev-parse", "--short", "HEAD"))
	d := testLandDeps(t)
	gated := false
	d.gate = func(string, bool) (string, error) {
		gated = true
		return "", nil
	}
	d.passed = func(dir, _ string) bool { return dir == lib }
	var stdout, stderr strings.Builder
	d.stdout, d.stderr = &stdout, &stderr
	if code := runLandWith(t.Context(), "", []string{"--dry-run", "--repos", app, lib}, d); code != exitcode.Success {
		t.Fatalf("code = %v stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	wantLib := "1. " + lib + ": would push (gate: would reuse pass; 1 ahead)\n   " + libTip + " lib change\n"
	if !strings.HasPrefix(out, wantLib) || !strings.Contains(out, "2. "+app+": would push (gate: would run; 1 ahead)") {
		t.Errorf("stdout = %q", out)
	}
	if gated {
		t.Error("dry run gated")
	}
	if ahead := strings.TrimSpace(gitOut(t, lib, "rev-list", "--count", "@{u}..HEAD")); ahead != "1" {
		t.Errorf("lib ahead = %s after dry run; want 1", ahead)
	}

	stdout.Reset()
	if code := runLandWith(t.Context(), "", []string{"--dry-run", "--json", "--repos", app}, d); code != exitcode.Success {
		t.Fatalf("json code = %v", code)
	}
	var got struct {
		Repos []landDryRunRepo `json:"repos"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil || len(got.Repos) != 1 || got.Repos[0].Route != "push" || got.Repos[0].Gate != "would run" || len(got.Repos[0].Ahead) != 1 {
		t.Errorf("json = %q (%v)", stdout.String(), err)
	}
	if code := runLandWith(t.Context(), "", []string{"--dry-run", "--wait", "--repos", app}, d); code != exitcode.InvalidUsage {
		t.Errorf("--dry-run --wait = %v; want usage error", code)
	}
}
