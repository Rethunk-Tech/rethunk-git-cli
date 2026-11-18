package app

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	code := landRepos([]string{dir}, false, false, d)
	if code != exitcode.Success {
		t.Fatalf("code = %v stdout=%q", code, stdout.String())
	}
	want := dir + ": pushed\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q; want %q", stdout.String(), want)
	}
	assertFleetTouch(t, d, dir, "1")
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
	code := landRepos([]string{dir}, false, false, d)
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
	if !strings.Contains(stdout.String(), "usage: rgit land") {
		t.Errorf("stdout = %q", stdout.String())
	}
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
