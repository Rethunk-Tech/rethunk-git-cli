package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/exitcode"
)

const landUsageLine = "usage: rgit land [--repos DIR...] [--wait] [--json]"

const landHelp = landUsageLine + `

Push each repo's ahead commits the way its GitHub branch rules require: a
direct push when the default branch is open, or a timestamped land/ branch,
PR, and merge (or merge-queue) request when it is not. One summary line per
repo; exit 1 if any repo is red.

Control returns after the push or the merge/queue request. PRs not yet
merged are then watched in the background unless --wait is set, in which
case land polls inline and prints the final line per repo.

Flags:
  --repos DIR...  repositories to land (default: the current directory)
  --wait          poll each open PR until it merges or the cap is hit
  --json          one JSON object on stdout
  -h, --help      show this help and exit

Full reference: docs/USAGE.md
`

const (
	landPollInterval = 60 * time.Second
	landPollCap      = 45 * time.Minute
)

const landWatchFlag = "--watch"

type landClock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

type realClock struct{}

func (realClock) Now() time.Time        { return time.Now().UTC() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

// gitHub talks to GitHub through gh. Tests swap in a fake; nothing in this
// package dials api.github.com itself.
type gitHub interface {
	branchRuleTypes(slug, branch string) ([]string, error)
	squashMergeAllowed(slug string) (bool, error)
	mergeCommitAllowed(slug string) (bool, error)
	createPR(dir, head, body string) (url string, err error)
	mergePR(dir, head string, auto bool, strategy string) error
	prNumber(dir, head string) (int, error)
	pullRequest(owner, repo string, number int) (prInfo, error)
	latestWorkflowRun(owner, repo, branch, sha string) (workflowRun, error)
}

type prInfo struct {
	State        string
	Merged       bool
	Closed       bool
	HasAutoMerge bool
	HasQueue     bool
	MergeSHA     string
	URL          string
}

type workflowRun struct {
	Status     string
	Conclusion string
}

type gitRunner interface {
	run(dir string, args ...string) (string, error)
}

type execGit struct{}

func (execGit) run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // git argv is constructed by rgit, never a shell string
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type ghCLI struct {
	runFn func(dir string, args ...string) (string, error)
}

func newGHCLI() *ghCLI {
	return &ghCLI{runFn: runGH}
}

func runGH(dir string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...) //nolint:gosec // gh argv is constructed by rgit, never a shell string
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (g *ghCLI) branchRuleTypes(slug, branch string) ([]string, error) {
	out, err := g.runFn("", []string{"api", "repos/" + slug + "/rules/branches/" + branch, "-q", ".[].type"}...)
	if err != nil {
		return nil, err
	}
	return parseRuleTypes(out), nil
}

func (g *ghCLI) squashMergeAllowed(slug string) (bool, error) {
	out, err := g.runFn("", []string{"repo", "view", slug, "--json", "squashMergeAllowed", "-q", ".squashMergeAllowed"}...)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

func (g *ghCLI) mergeCommitAllowed(slug string) (bool, error) {
	out, err := g.runFn("", []string{"repo", "view", slug, "--json", "mergeCommitAllowed", "-q", ".mergeCommitAllowed"}...)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

func (g *ghCLI) createPR(dir, head, body string) (string, error) {
	out, err := g.runFn(dir, "pr", "create", "--fill", "--head", head, "--body", body)
	if err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(out))
	}
	return lastNonEmptyLine(out), nil
}

func (g *ghCLI) mergePR(dir, head string, auto bool, strategy string) error {
	args := []string{"pr", "merge", head}
	if auto {
		args = append(args, "--auto")
	}
	if strategy != "" {
		args = append(args, strategy)
	}
	_, err := g.runFn(dir, args...)
	return err
}

func (g *ghCLI) prNumber(dir, head string) (int, error) {
	out, err := g.runFn(dir, "pr", "view", head, "--json", "number", "-q", ".number")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (g *ghCLI) pullRequest(owner, repo string, number int) (prInfo, error) {
	query := fmt.Sprintf(
		`{repository(owner:%q,name:%q){pullRequest(number:%d){state url autoMergeRequest{enabledAt} mergeQueueEntry{state} mergeCommit{oid}}}}`,
		owner, repo, number,
	)
	out, err := g.runFn("", "api", "graphql", "-f", "query="+query, "-q",
		`.data.repository.pullRequest | {state: .state, url: .url, auto: (.autoMergeRequest != null), queue: (.mergeQueueEntry != null), sha: .mergeCommit.oid}`)
	if err != nil {
		return prInfo{}, err
	}
	return parsePRQuery(out), nil
}

func (g *ghCLI) latestWorkflowRun(owner, repo, branch, sha string) (workflowRun, error) {
	path := fmt.Sprintf("repos/%s/%s/actions/runs?branch=%s&head_sha=%s&per_page=1", owner, repo, branch, sha)
	out, err := g.runFn("", "api", path, "-q", ".workflow_runs[0] | {status: .status, conclusion: .conclusion}")
	if err != nil {
		return workflowRun{}, err
	}
	return parseWorkflowQuery(out), nil
}

type landDeps struct {
	git        gitRunner
	gh         gitHub
	clock      landClock
	lookPath   func(string) (string, error)
	notify     func(argv []string) error
	executable func() (string, error)
	startWatch func(argv []string, logPath string) error
	cacheHome  func() string
	pid        int
	randN      func() int
	parent     string
	stdout     io.Writer
	stderr     io.Writer
}

func defaultLandDeps(stdout, stderr io.Writer) landDeps {
	return landDeps{
		git:        execGit{},
		gh:         newGHCLI(),
		clock:      realClock{},
		lookPath:   exec.LookPath,
		notify:     runNotify,
		executable: os.Executable,
		startWatch: startDetachedWatch,
		cacheHome:  cacheHome,
		pid:        os.Getpid(),
		randN:      randInt,
		parent:     os.Getenv("CLAUDE_CODE_SESSION_ID"),
		stdout:     stdout,
		stderr:     stderr,
	}
}

func runNotify(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv is nomad-hud-notify plus flags rgit built
	cmd.Stdin = nil
	return cmd.Run()
}

func startDetachedWatch(argv []string, logPath string) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // logPath is under the cache dir rgit created for this watch id
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv is rgit's own executable plus land --watch flags
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return err
	}
	_ = log.Close()
	return nil
}

func cacheHome() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "rgit-cache")
	}
	return filepath.Join(home, ".cache")
}

func randInt() int {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return int(time.Now().UnixNano() & 0x7fff)
	}
	return int(b[0])<<8 | int(b[1])
}

func runLand(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	return runLandWith(ctx, dir, args, defaultLandDeps(stdout, stderr))
}

func runLandWith(ctx context.Context, dir string, args []string, d landDeps) exitcode.Code {
	wait := false
	watchID := ""
	watchParent := d.parent
	var repos []string
	reposFlag := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--help" || a == "-h":
			fmt.Fprint(d.stdout, landHelp)
			return exitcode.Success
		case a == "--wait":
			wait = true
		case a == "--json":
			// already applied via ctx; accepted here so it can sit after the command
		case a == "--repos":
			reposFlag = true
		case a == landWatchFlag:
			if i+1 >= len(args) {
				fmt.Fprintln(d.stderr, "rgit land: --watch needs an id")
				fmt.Fprintln(d.stderr, landUsageLine)
				return exitcode.InvalidUsage
			}
			i++
			watchID = args[i]
		case a == "--parent":
			if i+1 >= len(args) {
				fmt.Fprintln(d.stderr, "rgit land: --parent needs an id")
				fmt.Fprintln(d.stderr, landUsageLine)
				return exitcode.InvalidUsage
			}
			i++
			watchParent = args[i]
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(d.stderr, "rgit land: unknown argument: %s\n", a)
			fmt.Fprintln(d.stderr, landUsageLine)
			return exitcode.InvalidUsage
		default:
			repos = append(repos, a)
		}
	}
	if watchID != "" {
		return runLandWatch(watchID, watchParent, d)
	}
	if reposFlag && len(repos) == 0 {
		fmt.Fprintln(d.stderr, "rgit land: --repos needs a directory")
		fmt.Fprintln(d.stderr, landUsageLine)
		return exitcode.InvalidUsage
	}
	if len(repos) == 0 {
		if dir != "" {
			repos = []string{dir}
		} else {
			repos = []string{"."}
		}
	} else if dir != "" {
		for i, r := range repos {
			if !filepath.IsAbs(r) {
				repos[i] = filepath.Join(dir, r)
			}
		}
	}
	asJSON := jsonFlag(ctx)
	return landRepos(repos, wait, asJSON, d)
}

func landRepos(repos []string, wait, asJSON bool, d landDeps) exitcode.Code {
	ordered := orderRepos(repos)
	var results []landResult
	red := false
	for _, r := range ordered {
		res := landOne(r, d)
		results = append(results, res)
		if res.Kind == landRed {
			red = true
		}
	}

	var watches []landWatchJob
	for i, res := range results {
		if res.Kind != landPR || res.PRState == "merged" || res.URL == "" {
			continue
		}
		job := landWatchJob{
			ID:      watchIDFor(res, d),
			Parent:  d.parent,
			Repo:    res.Repo,
			Slug:    res.Slug,
			PR:      res.PRNumber,
			URL:     res.URL,
			Default: res.Default,
		}
		if wait {
			fin := watchUntilDone(job, d)
			results[i].Watch = &fin
			results[i].Lines = append([]string{}, finalWaitLine(res, fin))
		} else {
			watches = append(watches, job)
		}
	}

	if asJSON {
		enc := json.NewEncoder(d.stdout)
		_ = enc.Encode(landJSON{Repos: jsonResults(results, watches, wait)})
	} else {
		for _, res := range results {
			for _, line := range res.Lines {
				fmt.Fprintln(d.stdout, line)
			}
		}
	}

	if !wait {
		for _, job := range watches {
			if err := spawnWatcher(job, d); err != nil {
				fmt.Fprintf(d.stderr, "rgit land: watch %s: %v\n", job.ID, err)
				continue
			}
			fmt.Fprintf(d.stdout, "watching: %s\n", job.ID)
		}
	}

	if red {
		return 1
	}
	return exitcode.Success
}

type landKind int

const (
	landSkipped landKind = iota
	landPushed
	landPR
	landRed
)

type landResult struct {
	Repo     string
	Kind     landKind
	Lines    []string
	URL      string
	PRState  string
	PRNumber int
	Slug     string
	Default  string
	Watch    *landWatchResult
}

type landWatchJob struct {
	ID      string `json:"id"`
	Parent  string `json:"parent"`
	Repo    string `json:"repo"`
	Slug    string `json:"slug"`
	PR      int    `json:"pr"`
	URL     string `json:"url"`
	Default string `json:"default"`
}

type landWatchResult struct {
	ID         string `json:"id"`
	Parent     string `json:"parent"`
	Repo       string `json:"repo"`
	PR         int    `json:"pr"`
	URL        string `json:"url"`
	State      string `json:"state"`
	CI         string `json:"ci"`
	FinishedAt string `json:"finishedAt"`
}

type landJSON struct {
	Repos []landJSONRepo `json:"repos"`
}

type landJSONRepo struct {
	Path     string           `json:"path"`
	Status   string           `json:"status"`
	URL      string           `json:"url,omitempty"`
	State    string           `json:"state,omitempty"`
	Watching string           `json:"watching,omitempty"`
	Watch    *landWatchResult `json:"watch,omitempty"`
}

func jsonResults(results []landResult, watches []landWatchJob, wait bool) []landJSONRepo {
	watchByRepo := map[string]string{}
	for _, w := range watches {
		watchByRepo[w.Repo] = w.ID
	}
	out := make([]landJSONRepo, 0, len(results))
	for _, res := range results {
		jr := landJSONRepo{Path: res.Repo, URL: res.URL, State: res.PRState, Watch: res.Watch}
		switch res.Kind {
		case landPushed:
			jr.Status = "pushed"
		case landSkipped:
			jr.Status = "skipped"
		case landRed:
			jr.Status = "red"
		case landPR:
			jr.Status = "pr"
		}
		if !wait {
			jr.Watching = watchByRepo[res.Repo]
		}
		out = append(out, jr)
	}
	return out
}

func orderRepos(repos []string) []string {
	var ordered, umbrellas []string
	for _, r := range repos {
		if _, err := os.Stat(filepath.Join(r, ".gitmodules")); err == nil {
			umbrellas = append(umbrellas, r)
		} else {
			ordered = append(ordered, r)
		}
	}
	return append(ordered, umbrellas...)
}

func landOne(r string, d landDeps) landResult {
	repo, err := d.git.run(r, "rev-parse", "--show-toplevel")
	if err != nil {
		return landResult{Repo: r, Kind: landSkipped, Lines: []string{r + ": skipped (not a git repo)"}}
	}
	repo = strings.TrimSpace(repo)
	res := landResult{Repo: repo}

	def := defaultBranch(repo, d)
	res.Default = def
	if dirtyTracked(repo, d) {
		res.Kind = landSkipped
		res.Lines = []string{repo + ": skipped (dirty tracked files)"}
		return res
	}
	cur := strings.TrimSpace(mustGit(repo, d, "rev-parse", "--abbrev-ref", "HEAD"))
	if cur != def {
		res.Kind = landSkipped
		res.Lines = []string{fmt.Sprintf("%s: skipped (on %s, not %s)", repo, cur, def)}
		return res
	}
	_, _ = d.git.run(repo, "fetch", "-q")
	aheadOut, err := d.git.run(repo, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		res.Kind = landSkipped
		res.Lines = []string{repo + ": skipped (no upstream)"}
		return res
	}
	ahead, _ := strconv.Atoi(strings.TrimSpace(aheadOut))
	if ahead == 0 {
		res.Kind = landSkipped
		res.Lines = []string{repo + ": skipped (not ahead)"}
		return res
	}
	behindOut, _ := d.git.run(repo, "rev-list", "--count", "HEAD..@{u}")
	behind, _ := strconv.Atoi(strings.TrimSpace(behindOut))
	if behind > 0 {
		if _, err := d.git.run(repo, "pull", "-q", "--rebase"); err != nil {
			_, _ = d.git.run(repo, "rebase", "--abort")
			res.Kind = landSkipped
			res.Lines = []string{fmt.Sprintf("%s: skipped (behind %d, rebase not clean)", repo, behind)}
			return res
		}
	}
	aheadOut, _ = d.git.run(repo, "rev-list", "--count", "@{u}..HEAD")
	ahead, _ = strconv.Atoi(strings.TrimSpace(aheadOut))
	if ahead == 0 {
		res.Kind = landSkipped
		res.Lines = []string{summarySkippedAlreadyUpstream(repo)}
		return res
	}

	origin := strings.TrimSpace(mustGit(repo, d, "config", "--get", "remote.origin.url"))
	slug := slugFromOrigin(origin)
	res.Slug = slug
	var rules []string
	if slug != "" {
		rules, _ = d.gh.branchRuleTypes(slug, def)
	}
	needPR, mergeQueue := rulesNeedPR(rules)
	if !needPR {
		out, err := pushRetry(repo, "", d)
		if err == nil {
			res.Kind = landPushed
			res.Lines = []string{summaryPushed(repo)}
			touchFleet(repo, 1, d)
			return res
		}
		if !pushRejectedProtected(out) {
			res.Kind = landRed
			res.Lines = []string{summaryRed(repo, out)}
			return res
		}
	}

	br := "land/" + d.clock.Now().UTC().Format("20060102-150405")
	subj := strings.TrimSpace(mustGit(repo, d, "log", "--format=- %s", "@{u}..HEAD"))
	if _, err := d.git.run(repo, "branch", br, "HEAD"); err != nil {
		res.Kind = landRed
		res.Lines = []string{summaryRed(repo, err.Error())}
		return res
	}
	pushOut, err := d.git.run(repo, "push", "-u", "origin", br)
	if err != nil {
		res.Kind = landRed
		res.Lines = []string{summaryRed(repo, pushOut)}
		return res
	}
	url, err := d.gh.createPR(repo, br, subj)
	if err != nil {
		res.Kind = landRed
		res.Lines = []string{summaryRed(repo, err.Error())}
		return res
	}
	res.URL = url
	strategy := ""
	if !mergeQueue {
		strategy = mergeStrategy(slug, d)
	}
	// Plain merge first: with no required checks GitHub treats the PR as
	// mergeable now, so --auto only warns and queues nothing.
	_ = d.gh.mergePR(repo, br, false, "")
	if mergeQueue {
		_ = d.gh.mergePR(repo, br, true, "")
	} else {
		_ = d.gh.mergePR(repo, br, true, strategy)
	}
	num, _ := d.gh.prNumber(repo, br)
	res.PRNumber = num
	queued := ""
	if owner, name, ok := splitSlug(slug); ok && num > 0 {
		info, err := d.gh.pullRequest(owner, name, num)
		if err == nil {
			queued = judgePRState(info)
			if res.URL == "" {
				res.URL = info.URL
			}
		}
	}
	res.PRState = queued
	if queued == "" {
		res.Lines = append(res.Lines, repo+": note: PR open but neither queued nor set to auto-merge")
	}
	_, _ = d.git.run(repo, "reset", "-q", "--keep", "origin/"+def)
	res.Kind = landPR
	res.Lines = append(res.Lines, summaryPR(repo, res.URL, queued))
	touchFleet(repo, 0, d)
	return res
}

func mustGit(repo string, d landDeps, args ...string) string {
	out, _ := d.git.run(repo, args...)
	return out
}

func defaultBranch(repo string, d landDeps) string {
	ref, err := d.git.run(repo, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	if err == nil {
		ref = strings.TrimSpace(ref)
		return strings.TrimPrefix(ref, "refs/remotes/origin/")
	}
	if _, err := d.git.run(repo, "show-ref", "--verify", "--quiet", "refs/heads/main"); err == nil {
		return "main"
	}
	return "master"
}

func dirtyTracked(repo string, d landDeps) bool {
	out, _ := d.git.run(repo, "status", "--porcelain=v1", "-uno")
	return strings.TrimSpace(out) != ""
}

func pushRetry(repo, to string, d landDeps) (string, error) {
	var out string
	var err error
	for range 4 {
		args := []string{"push"}
		if to != "" {
			args = append(args, "origin", "HEAD:"+to)
		}
		out, err = d.git.run(repo, args...)
		if err == nil {
			return out, nil
		}
		if !pushRetryable(out) {
			return out, err
		}
		pullArgs := []string{"pull", "-q", "--rebase"}
		if to != "" {
			pullArgs = append(pullArgs, "origin", to)
		}
		if _, pullErr := d.git.run(repo, pullArgs...); pullErr != nil {
			_, _ = d.git.run(repo, "rebase", "--abort")
			return out, err
		}
	}
	return out, err
}

func pushRetryable(out string) bool {
	return strings.Contains(out, "fetch first") ||
		strings.Contains(out, "non-fast-forward") ||
		strings.Contains(out, "cannot lock ref") ||
		strings.Contains(out, "stale info")
}

func pushRejectedProtected(out string) bool {
	return strings.Contains(out, "GH013") || strings.Contains(out, "protected branch")
}

func mergeStrategy(slug string, d landDeps) string {
	if ok, err := d.gh.squashMergeAllowed(slug); err == nil && ok {
		return "--squash"
	}
	if ok, err := d.gh.mergeCommitAllowed(slug); err == nil && ok {
		return "--merge"
	}
	return "--rebase"
}

func slugFromOrigin(origin string) string {
	slug := strings.TrimSuffix(strings.TrimSpace(origin), ".git")
	switch {
	case strings.Contains(slug, "@") && strings.Contains(slug, ":"):
		// git@host:owner/repo
		if i := strings.Index(slug, ":"); i >= 0 && !strings.Contains(slug[i+1:], "://") {
			at := strings.LastIndex(slug[:i], "@")
			if at >= 0 {
				return slug[i+1:]
			}
		}
		return ""
	case strings.HasPrefix(slug, "http://") || strings.HasPrefix(slug, "https://") || strings.HasPrefix(slug, "ssh://"):
		rest := slug
		if i := strings.Index(rest, "://"); i >= 0 {
			rest = rest[i+3:]
		}
		if i := strings.Index(rest, "/"); i >= 0 {
			return rest[i+1:]
		}
		return ""
	default:
		return ""
	}
}

func splitSlug(slug string) (owner, name string, ok bool) {
	i := strings.IndexByte(slug, '/')
	if i <= 0 || i == len(slug)-1 {
		return "", "", false
	}
	return slug[:i], slug[i+1:], true
}

func parseRuleTypes(out string) []string {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	if strings.HasPrefix(out, "[") {
		var raw []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(out), &raw) == nil {
			var types []string
			for _, r := range raw {
				if r.Type != "" {
					types = append(types, r.Type)
				}
			}
			return types
		}
		var lines []string
		_ = json.Unmarshal([]byte(out), &lines)
		return lines
	}
	var types []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			types = append(types, line)
		}
	}
	return types
}

func rulesNeedPR(types []string) (needPR, mergeQueue bool) {
	for _, t := range types {
		switch t {
		case "pull_request":
			needPR = true
		case "merge_queue":
			needPR = true
			mergeQueue = true
		}
	}
	return needPR, mergeQueue
}

func judgePRState(info prInfo) string {
	if info.State == "MERGED" || info.Merged {
		return "merged"
	}
	if info.HasQueue {
		return "queued"
	}
	if info.HasAutoMerge {
		return "auto-merge"
	}
	return ""
}

func parsePRQuery(out string) prInfo {
	out = strings.TrimSpace(out)
	var raw struct {
		State string `json:"state"`
		URL   string `json:"url"`
		Auto  bool   `json:"auto"`
		Queue bool   `json:"queue"`
		SHA   string `json:"sha"`
	}
	if json.Unmarshal([]byte(out), &raw) == nil && raw.State != "" {
		return prInfo{
			State:        raw.State,
			Merged:       raw.State == "MERGED",
			Closed:       raw.State == "CLOSED",
			HasAutoMerge: raw.Auto,
			HasQueue:     raw.Queue,
			MergeSHA:     raw.SHA,
			URL:          raw.URL,
		}
	}
	// gh -q string form used by the script
	switch strings.TrimSpace(out) {
	case "merged", "queued", "auto-merge":
		return prInfo{State: strings.ToUpper(out)}
	}
	return prInfo{}
}

func parseWorkflowQuery(out string) workflowRun {
	out = strings.TrimSpace(out)
	var raw struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	if json.Unmarshal([]byte(out), &raw) == nil {
		return workflowRun{Status: raw.Status, Conclusion: raw.Conclusion}
	}
	return workflowRun{}
}

func summaryPushed(repo string) string { return repo + ": pushed" }

func summarySkippedAlreadyUpstream(repo string) string {
	return repo + ": skipped (already upstream)"
}

func summaryPR(repo, url, state string) string {
	if state == "" {
		return repo + ": PR " + url
	}
	return repo + ": PR " + url + " (" + state + ")"
}

func summaryRed(repo, out string) string {
	return repo + ": red (" + last5Piped(out) + ")"
}

func last5Piped(out string) string {
	lines := nonEmptyLines(out)
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, "|")
}

func lastNonEmptyLine(s string) string {
	lines := nonEmptyLines(s)
	if len(lines) == 0 {
		return strings.TrimSpace(s)
	}
	return lines[len(lines)-1]
}

func nonEmptyLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func touchFleet(repo string, pushed int, d landDeps) {
	dir := filepath.Join(d.cacheHome(), "nomad-hud", "fleet-touch")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	base := fmt.Sprintf("land-%d-%d", d.pid, d.randN())
	tmp := filepath.Join(dir, base+".tmp")
	final := filepath.Join(dir, base+".tsv")
	body := fmt.Sprintf("%s\t%d\n", repo, pushed)
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, final)
}

func watchIDFor(res landResult, d landDeps) string {
	owner, name, ok := splitSlug(res.Slug)
	if !ok {
		name = filepath.Base(res.Repo)
		owner = "local"
	}
	return fmt.Sprintf("%s-%s-%d-%s", owner, name, res.PRNumber, d.clock.Now().UTC().Format("20060102T150405Z"))
}

func spawnWatcher(job landWatchJob, d landDeps) error {
	exe, err := d.executable()
	if err != nil {
		return err
	}
	dir := filepath.Join(d.cacheHome(), "rgit", "land", job.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	state, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), state, 0o600); err != nil {
		return err
	}
	logPath := filepath.Join(dir, "watch.log")
	argv := []string{exe, "land", landWatchFlag, job.ID, "--parent", job.Parent}
	return d.startWatch(argv, logPath)
}

func runLandWatch(id, parent string, d landDeps) exitcode.Code {
	job, err := loadWatchJob(id, parent, d)
	if err != nil {
		fmt.Fprintf(d.stderr, "rgit land: watch %s: %v\n", id, err)
		return 1
	}
	res := watchUntilDone(job, d)
	writeWatchResult(res, d)
	notifyWatch(res, d)
	return exitcode.Success
}

func loadWatchJob(id, parent string, d landDeps) (landWatchJob, error) {
	path := filepath.Join(d.cacheHome(), "rgit", "land", id, "state.json")
	b, err := os.ReadFile(path) //nolint:gosec // path is the watch state file under the cache dir rgit created
	if err != nil {
		return landWatchJob{}, err
	}
	var job landWatchJob
	if err := json.Unmarshal(b, &job); err != nil {
		return landWatchJob{}, err
	}
	job.ID = id
	job.Parent = parent
	return job, nil
}

func watchUntilDone(job landWatchJob, d landDeps) landWatchResult {
	repo := job.Slug
	if repo == "" {
		repo = filepath.Base(job.Repo)
	}
	res := landWatchResult{
		ID:     job.ID,
		Parent: job.Parent,
		Repo:   repo,
		PR:     job.PR,
		URL:    job.URL,
		CI:     "none",
	}
	owner, name, ok := splitSlug(job.Slug)
	info, state := pollPR(owner, name, ok, job, d)
	if info.URL != "" {
		res.URL = info.URL
	}
	res.State = state
	if state != "merged" {
		res.FinishedAt = d.clock.Now().UTC().Format(time.RFC3339)
		return res
	}
	return waitForCI(res, owner, name, ok, job.Default, info.MergeSHA, d)
}

func pollPR(owner, name string, ok bool, job landWatchJob, d landDeps) (prInfo, string) {
	deadline := d.clock.Now().Add(landPollCap)
	var info prInfo
	for {
		if ok {
			got, err := d.gh.pullRequest(owner, name, job.PR)
			if err == nil {
				info = got
			}
		}
		switch {
		case info.State == "MERGED" || info.Merged:
			return info, "merged"
		case info.State == "CLOSED" || info.Closed:
			return info, "closed"
		case !d.clock.Now().Before(deadline):
			return info, "timeout"
		}
		d.clock.Sleep(landPollInterval)
	}
}

func waitForCI(res landWatchResult, owner, name string, ok bool, branch, sha string, d landDeps) landWatchResult {
	deadline := d.clock.Now().Add(landPollCap)
	for {
		if ok && sha != "" {
			run, err := d.gh.latestWorkflowRun(owner, name, branch, sha)
			if err == nil && (run.Status != "" || run.Conclusion != "") {
				res.CI = ciFromRun(run)
				if run.Status == "completed" {
					res.FinishedAt = d.clock.Now().UTC().Format(time.RFC3339)
					return res
				}
			}
		}
		if !d.clock.Now().Before(deadline) {
			if res.CI != "none" {
				res.State = "timeout"
				res.CI = "running"
			}
			res.FinishedAt = d.clock.Now().UTC().Format(time.RFC3339)
			return res
		}
		d.clock.Sleep(landPollInterval)
	}
}

func ciFromRun(run workflowRun) string {
	switch {
	case run.Status == "completed" && run.Conclusion == "success":
		return "success"
	case run.Status == "completed":
		return "failure"
	default:
		return "running"
	}
}

func writeWatchResult(res landWatchResult, d landDeps) {
	dir := filepath.Join(d.cacheHome(), "nomad-hud", "land")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	tmp := filepath.Join(dir, res.ID+".tmp")
	final := filepath.Join(dir, res.ID+".json")
	b, err := json.Marshal(res)
	if err != nil {
		return
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, final)
}

func notifyWatch(res landWatchResult, d landDeps) {
	bin, err := d.lookPath("nomad-hud-notify")
	if err != nil {
		return
	}
	level := "info"
	if res.State == "closed" || res.State == "timeout" || res.CI == "failure" {
		level = "error"
	}
	owner, name, ok := splitSlug(res.Repo)
	if !ok {
		name = res.Repo
		owner = "local"
	}
	key := "land-" + owner + "-" + name + "-" + strconv.Itoa(res.PR)
	msg := fmt.Sprintf("%s #%d: merged, CI %s", name, res.PR, res.CI)
	if res.State != "merged" {
		msg = fmt.Sprintf("%s #%d: %s, CI %s", name, res.PR, res.State, res.CI)
	}
	argv := []string{bin, "--source", "rgit-land", "--key", key, "--level", level, "--url", res.URL, msg}
	_ = d.notify(argv)
}

func finalWaitLine(res landResult, fin landWatchResult) string {
	if res.Kind == landPR {
		return fmt.Sprintf("%s: PR %s (%s); CI %s", res.Repo, res.URL, fin.State, fin.CI)
	}
	if len(res.Lines) > 0 {
		return res.Lines[len(res.Lines)-1]
	}
	return res.Repo
}
