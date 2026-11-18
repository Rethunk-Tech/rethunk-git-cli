package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gittest"
)

func TestLandSummaryWording(t *testing.T) {
	t.Parallel()
	if got := summaryPushed("/r"); got != "/r: pushed" {
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
	want := dir + ": pushed\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q; want %q", stdout.String(), want)
	}
	assertFleetTouch(t, d, dir, "1")
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
	if !strings.Contains(out, dir+": pushed\n") {
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
	if !strings.Contains(out, dir+": pushed\n") {
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

func TestLandWatcherResultAndNotify(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: time.Date(2026, 11, 18, 7, 0, 0, 0, time.UTC)}
	gh := &fakeGH{
		prs: []prInfo{
			{State: "OPEN", URL: "https://github.com/o/r/pull/9"},
			{State: "MERGED", MergeSHA: "abc", URL: "https://github.com/o/r/pull/9"},
		},
		run: workflowRun{Status: "completed", Conclusion: "success"},
	}
	var notified [][]string
	d := testLandDeps(t)
	d.clock = clock
	d.gh = gh
	d.lookPath = func(string) (string, error) { return "/bin/nomad-hud-notify", nil }
	d.notify = func(argv []string) error { notified = append(notified, append([]string{}, argv...)); return nil }

	job := landWatchJob{
		ID:      "o-r-9-id",
		Parent:  "sess-1",
		Repo:    "/src/r",
		Slug:    "o/r",
		PR:      9,
		URL:     "https://github.com/o/r/pull/9",
		Default: "main",
	}
	res := watchUntilDone(job, d)
	if res.State != "merged" || res.CI != "success" || res.Parent != "sess-1" {
		t.Fatalf("result = %+v", res)
	}
	writeWatchResult(res, d)
	notifyWatch(res, d)

	path := filepath.Join(d.cacheHome(), "nomad-hud", "land", job.ID+".json")
	body, err := os.ReadFile(path) //nolint:gosec // path is under the test cache dir
	if err != nil {
		t.Fatal(err)
	}
	var got landWatchResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != job.ID || got.Parent != "sess-1" || got.Repo != "o/r" || got.PR != 9 || got.State != "merged" || got.CI != "success" {
		t.Errorf("file = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(d.cacheHome(), "nomad-hud", "land", job.ID+".tmp")); !os.IsNotExist(err) {
		t.Errorf("tmp file still present: %v", err)
	}
	if len(notified) != 1 {
		t.Fatalf("notify calls = %d; want 1", len(notified))
	}
	argv := notified[0]
	want := []string{
		"/bin/nomad-hud-notify",
		"--source", "rgit-land",
		"--key", "land-o-r-9",
		"--level", "info",
		"--url", "https://github.com/o/r/pull/9",
		"r #9: merged, CI success",
	}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %#v; want %#v", argv, want)
	}
}

func TestLandWatcherNotifyErrorOnClosed(t *testing.T) {
	t.Parallel()
	var notified [][]string
	d := testLandDeps(t)
	d.clock = &fakeClock{now: time.Date(2026, 11, 18, 7, 0, 0, 0, time.UTC)}
	d.gh = &fakeGH{prs: []prInfo{{State: "CLOSED", URL: "https://github.com/o/r/pull/3"}}}
	d.lookPath = func(string) (string, error) { return "/bin/nomad-hud-notify", nil }
	d.notify = func(argv []string) error { notified = append(notified, append([]string{}, argv...)); return nil }
	res := watchUntilDone(landWatchJob{ID: "id", Slug: "o/r", PR: 3, URL: "https://github.com/o/r/pull/3"}, d)
	notifyWatch(res, d)
	if res.State != "closed" {
		t.Fatalf("state = %q", res.State)
	}
	if len(notified) != 1 || notified[0][6] != "error" {
		t.Errorf("argv = %#v", notified)
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
	var spawned [][]string
	d.startWatch = func(argv []string, logPath string) error {
		spawned = append(spawned, append([]string{}, argv...))
		if !strings.HasSuffix(logPath, "watch.log") {
			t.Errorf("logPath = %q", logPath)
		}
		return nil
	}
	d.parent = "parent-sid"
	code := landRepos(t.Context(), []string{dir}, false, false, false, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, dir+": PR https://github.com/acme/demo/pull/4 (auto-merge)") {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(out, "watching: ") {
		t.Errorf("missing watching line: %q", out)
	}
	if len(spawned) != 1 {
		t.Fatalf("spawned = %d", len(spawned))
	}
	if !containsAll(spawned[0], "land", "--watch", "--parent", "parent-sid") {
		t.Errorf("watch argv = %#v", spawned[0])
	}
	if len(gh.merges) < 2 || gh.merges[0] != "plain" || gh.merges[1] != "auto:--squash" {
		t.Errorf("merges = %#v; want plain then auto:--squash", gh.merges)
	}
	assertFleetTouch(t, d, dir, "0")
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
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
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
	assertFleetTouch(t, d, dir, "1")
}

func TestLandBranchNoPushLeavesOrigin(t *testing.T) {
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
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat", "--no-push"}, d)
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
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
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
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
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
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
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
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
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
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
	if code != 1 {
		t.Fatalf("code = %v stdout=%q stderr=%q; want 1", code, stdout.String(), stderr.String())
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
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
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

func TestLandBranchFlakeLoggedAndLands(t *testing.T) {
	t.Parallel()
	dir, bare := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-b", "feat", wt)
	gittest.Write(t, wt, "feat.txt", "feat\n")
	gitOut(t, wt, "add", "feat.txt")
	gitOut(t, wt, "commit", "-q", "-m", "feat")

	n := 0
	d := testLandDeps(t)
	d.gate = func(string, bool) (string, error) {
		n++
		if n == 1 {
			return "gate: failed: unit -> /x.log\n", errors.New("exit 1")
		}
		return "ok\n", nil
	}
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "flake: unit passed on re-run") {
		t.Errorf("stderr = %q", stderr.String())
	}
	body, err := os.ReadFile(filepath.Join(d.cacheHome(), "land-branch", "flakes.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "2026-11-18T00:00:00Z\t" + filepath.Base(dir) + "\tfeat\tunit\n"
	if string(body) != want {
		t.Errorf("flakes.tsv = %q; want %q", body, want)
	}
	if got := strings.TrimSpace(gitOut(t, bare, "log", "-1", "--pretty=%s")); got != "feat" {
		t.Errorf("origin subject = %q", got)
	}
	if last := lastNonEmptyLine(stdout.String()); !strings.HasSuffix(last, " direct") {
		t.Errorf("summary = %q", last)
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

func TestStartDetachedWatch(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh as the detached child")
	}
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := startDetachedWatch([]string{"/bin/sh"}, filepath.Join(blocker, "watch.log")); err == nil {
		t.Error("log dir under a file: want error")
	}
	logDir := filepath.Join(base, "logdir")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := startDetachedWatch([]string{"/bin/sh"}, logDir); err == nil {
		t.Error("log path is a directory: want error")
	}
	if err := startDetachedWatch([]string{filepath.Join(base, "missing-bin")}, filepath.Join(base, "start.log")); err == nil {
		t.Error("missing binary: want error")
	}

	logPath := filepath.Join(base, "nested", "watch.log")
	if err := startDetachedWatch([]string{"/bin/sh", "-c", "echo detached-ok"}, logPath); err != nil {
		t.Fatalf("startDetachedWatch: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		body, err := os.ReadFile(logPath) //nolint:gosec // path under the test temp dir
		if err == nil && strings.Contains(string(body), "detached-ok") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watch log = %q, %v; want detached-ok", body, err)
		}
		time.Sleep(10 * time.Millisecond)
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
	if code := runLandWith(t.Context(), base, []string{"one", "two"}, d); code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	want := []string{filepath.Join(base, "one"), filepath.Join(base, "two")}
	if strings.Join(git.dirs, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("dirs = %#v; want %#v", git.dirs, want)
	}
	for _, r := range want {
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
	if res := landOne(dirty, d); res.Kind != landSkipped || res.Lines[0] != dirty+": skipped (dirty tracked files)" {
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
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), dir+": skipped (dirty tracked files)\n") {
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
	if !strings.Contains(stdout.String(), dir+": skipped (dirty tracked files)\n") {
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
	out, err := runGH(work, "repo", "view")
	if err != nil {
		t.Fatalf("runGH: %v out=%q", err, out)
	}
	if strings.TrimSpace(out) != work {
		t.Errorf("runGH ran in %q; want %q", strings.TrimSpace(out), work)
	}
	if _, err := runGH(filepath.Join(work, "absent"), "repo", "view"); err == nil {
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
	if code := runLandWith(t.Context(), dir, []string{"--branch", "feat"}, d); code != 1 {
		t.Fatalf("code = %v stdout=%q stderr=%q; want 1", code, stdout.String(), stderr.String())
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
	if got := defaultBranch(dir, d); got != "main" {
		t.Errorf("defaultBranch on main = %q", got)
	}
	gitOut(t, dir, "branch", "-M", "master")
	if got := defaultBranch(dir, d); got != "master" {
		t.Errorf("defaultBranch on master = %q", got)
	}
	if got, ok := defaultBranchChecked(dir, d); !ok || got != "master" {
		t.Errorf("defaultBranchChecked on master = %q,%v", got, ok)
	}
	gitOut(t, dir, "branch", "-M", "trunk")
	if got := defaultBranch(dir, d); got != "master" {
		t.Errorf("defaultBranch without main or master = %q", got)
	}
	if got, ok := defaultBranchChecked(dir, d); ok || got != "" {
		t.Errorf("defaultBranchChecked without main or master = %q,%v", got, ok)
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

func TestPushRetry(t *testing.T) {
	t.Parallel()
	var calls []string
	git := &fakeGitRunner{respond: func(_ string, args []string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}}
	d := testLandDeps(t)
	d.git = git
	if _, err := pushRetry("/src/r", "main", d); err != nil {
		t.Fatal(err)
	}
	if calls[0] != "push origin HEAD:main" {
		t.Errorf("push argv = %q; want origin HEAD:main", calls[0])
	}

	calls = nil
	git.respond = func(_ string, args []string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "push" {
			return " ! [rejected] (fetch first)", errors.New("exit 1")
		}
		return "", errors.New("pull failed")
	}
	if _, err := pushRetry("/src/r", "main", d); err == nil {
		t.Fatal("want the original push error")
	}
	if !containsAll(calls, "rebase --abort") {
		t.Errorf("calls = %#v; want rebase --abort after a failed pull", calls)
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
		"git@github.com:acme/demo.git":     "acme/demo",
		"git@github.com:acme/demo":         "acme/demo",
		"https://github.com/acme/demo.git": "acme/demo",
		"http://example.com/acme/demo":     "acme/demo",
		"ssh://git.example.com/acme/demo":  "acme/demo",
		"http://example.com":               "",
		"/local/checkout/demo.git":         "",
		"":                                 "",
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

func TestRunLandWatchLoadsState(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	var stderr strings.Builder
	d.stderr = &stderr
	if code := runLandWatch("absent-id", "sess", d); code != 1 {
		t.Errorf("missing state code = %v; want 1", code)
	}
	if !strings.Contains(stderr.String(), "absent-id") {
		t.Errorf("stderr = %q", stderr.String())
	}
	dir := filepath.Join(d.cacheHome(), "rgit", "land", "bad-id")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runLandWatch("bad-id", "sess", d); code != 1 {
		t.Errorf("broken state code = %v; want 1", code)
	}

	good := filepath.Join(d.cacheHome(), "rgit", "land", "good-id")
	if err := os.MkdirAll(good, 0o700); err != nil {
		t.Fatal(err)
	}
	state := `{"id":"stale","parent":"stale","repo":"/src/r","slug":"o/r","pr":7,"url":"u","default":"main"}`
	if err := os.WriteFile(filepath.Join(good, "state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	d.gh = &fakeGH{
		prs: []prInfo{{State: "MERGED", MergeSHA: "abc", URL: "u"}},
		run: workflowRun{Status: "completed", Conclusion: "success"},
	}
	if code := runLandWatch("good-id", "sess", d); code != exitcode.Success {
		t.Errorf("code = %v; want success", code)
	}
	body, err := os.ReadFile(filepath.Join(d.cacheHome(), "nomad-hud", "land", "good-id.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got landWatchResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "good-id" || got.Parent != "sess" || got.State != "merged" || got.CI != "success" {
		t.Errorf("watch result = %+v", got)
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
	if code := runLandWith(withJSONFlag(t.Context(), true), absent, []string{"--json", "--wait"}, d); code != exitcode.Success {
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

func TestLandNoPushNeedsBranch(t *testing.T) {
	t.Parallel()
	d := testLandDeps(t)
	var stdout, stderr strings.Builder
	d.stdout = &stdout
	d.stderr = &stderr
	if code := runLandWith(t.Context(), "", []string{"--no-push"}, d); code != exitcode.InvalidUsage {
		t.Errorf("code = %v; want InvalidUsage", code)
	}
	if !strings.Contains(stderr.String(), "--no-push needs --branch") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestLandWatchAndParentNeedValues(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{landWatchFlag, "--parent", "--branch"} {
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

func TestRandIntInRange(t *testing.T) {
	t.Parallel()
	for range 20 {
		if n := randInt(); n < 0 || n > 0xffff {
			t.Fatalf("randInt = %d; want 0..65535", n)
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
	rules  []string
	squash bool
	merge  bool
	url    string
	err    error
	number int
	prs    []prInfo
	pi     int
	run    workflowRun
	merges []string
}

func (f *fakeGH) branchRuleTypes(string, string) ([]string, error) { return f.rules, nil }
func (f *fakeGH) squashMergeAllowed(string) (bool, error)          { return f.squash, nil }
func (f *fakeGH) mergeCommitAllowed(string) (bool, error)          { return f.merge, nil }
func (f *fakeGH) createPR(string, string, string) (string, error) {
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
	d := defaultLandDeps(&strings.Builder{}, &strings.Builder{})
	d.cacheHome = func() string { return cache }
	d.lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	d.notify = func([]string) error { return nil }
	d.startWatch = func([]string, string) error { return nil }
	d.gate = func(string, bool) (string, error) { return "", nil }
	d.executable = func() (string, error) { return "/bin/rgit", nil }
	d.clock = &fakeClock{now: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)}
	d.randN = func() int { return 1 }
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

func assertFleetTouch(t *testing.T, d landDeps, repo, pushed string) {
	t.Helper()
	dir := filepath.Join(d.cacheHome(), "nomad-hud", "fleet-touch")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tsv") {
			b, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // names come from the test cache dir listing
			if err != nil {
				t.Fatal(err)
			}
			found = string(b)
		}
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover tmp %s", e.Name())
		}
	}
	want := repo + "\t" + pushed + "\n"
	if found != want {
		t.Errorf("fleet touch = %q; want %q", found, want)
	}
}

func containsAll(argv []string, needles ...string) bool {
	joined := strings.Join(argv, "\x00")
	for _, n := range needles {
		if !strings.Contains(joined, n) {
			return false
		}
	}
	return true
}
