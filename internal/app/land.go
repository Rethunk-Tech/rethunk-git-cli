package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gitx"
)

const landUsageLine = "usage: rgit land [--repos DIR...] [--sha SHA] [--no-gate] [--dry-run] [--wait] [--json] [--install] | rgit land --branch BR [--repos DIR] [--push] [--wait] [--json] [--install]"

const landHelp = landUsageLine + `

Gate, then push each repo's ahead commits the way its GitHub branch rules require: a
direct push when the default branch is open, or a timestamped land/ branch,
PR, and merge (or merge-queue) request when it is not. One summary line per
repo; exit 1 if any repo is red, 4 if a PR is neither queued nor set to auto-merge,
5 if an install or catch-up install failed, 14 if a repo was skipped because it
needs action (dirty tracked files, wrong branch, behind origin, no upstream, not
a repo; see docs/CODES.md). Each repo's full gate (gate in the repo
directory) runs on HEAD first; a red gate pushes nothing for that repo, and
its line names the failing gates and the log path. If HEAD or the tree
changes during the gate, nothing is pushed.

--dry-run prints, in run order, each repo's route (push, PR or queue), gate status
("would run", "would reuse pass" or skipped) and ahead commits, then exits 0
without catch-up, gating or pushing. It takes only --repos, --no-gate and --json.
A repo that pins another run repo's unpushed commit by git+ssh SHA (package.json,
go.mod) runs after it, and is skipped if that repo went red.

--sha SHA lands that ahead commit (an ancestor of HEAD) instead of HEAD:
the same direct, PR or queue route, no rebase, dirty tree allowed, local
default branch left alone. Only HEAD on a clean tree can be gated from the
checkout, so any other --sha is refused unless --no-gate is given.

--branch BR lands local branch BR onto the default branch in one repo
(--repos DIR, or the current directory). By default it rebases onto the
local default branch, runs the gate, and fast-forwards it even from a
checkout whose uncommitted changes touch none of BR's files, then stops:
no push, land/ branch, PR, or watch. Last stdout line: <repo> <branch>
<sha> <gate log> local. --push pushes through the repo's branch rules
instead; its last line ends <direct|PR|queue>. --push without --branch
is a usage error. Exit 1 on a red push or a moved origin, 2 on rebase
conflict, 3 on a red gate, 4 if a merge-queue PR does not enqueue, 5 if
--install fails, 13 if the checkout's dirty files overlap BR's, 14 if the
branch's worktree is dirty or the gate edited tracked files.

Before anything else, a clean default branch that is behind its remote and not
ahead is fast-forwarded and installed: "DIR: caught up to SHA", with
" (merged PR #N)" when a new commit subject carries #N.

--install runs make install from each directly pushed, clean HEAD after
every repo in the run has been pushed. A merged or queued PR is not
installed.

A push rejected because origin moved is never rebased and retried: land
exits 1 with "origin moved; nothing pushed, re-run" on stderr ("origin moved
during the gate" with --branch), so only verified commits are pushed.

Control returns after the push or the merge/queue request. PRs not yet
merged are watched by bosun (nomad-hud records the PR line) unless --wait
is set, in which case land polls inline and prints the final line per repo.

Flags:
  --repos DIR...  repositories to land (default: the current directory)
  --sha SHA       land this ahead commit instead of HEAD (not with --branch)
  --no-gate       skip the gate; for callers that already gated the commit
  --dry-run       print each repo's order, route, gate status and ahead commits; change nothing
  --branch BR     land local branch BR onto the local default branch (no push)
  --push          with --branch: also push through the repo's branch rules
  --wait          poll each open PR until it merges or the cap is hit
  --json          one JSON object on stdout
  --install       after a direct push, make install from that HEAD
  -h, --help      show this help and exit

Full reference: docs/USAGE.md
`

const (
	landPollInterval = 60 * time.Second
	landPollCap      = 45 * time.Minute
)

type landClock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

// realClock's Sleep returns early once ctx is cancelled.
type realClock struct {
	ctx context.Context //nolint:containedctx // per-run dependency bound to the run's signal context
}

func (realClock) Now() time.Time { return time.Now().UTC() }
func (c realClock) Sleep(d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-c.ctx.Done():
	}
}

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

// execGit runs git under ctx; a nil ctx never cancels.
type execGit struct {
	ctx  context.Context //nolint:containedctx // per-run dependency bound to the run's signal context
	step *string
}

func (g execGit) run(dir string, args ...string) (string, error) {
	setLandStep(g.step, "git")
	cmd := exec.CommandContext(orBackground(g.ctx), "git", append(slices.Clone(gitx.NoFsmonitor), args...)...) //nolint:gosec // git argv is constructed by rgit, never a shell string
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func setLandStep(step *string, name string) {
	if step != nil {
		*step = name
	}
}

func orBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func hasPrePushHook(ctx context.Context, dir string) bool {
	out, err := exec.CommandContext(ctx, "git", append(slices.Clone(gitx.NoFsmonitor), "-C", dir, "rev-parse", "--path-format=absolute", "--git-path", "hooks/pre-push")...).Output() //nolint:gosec // fixed argv
	if err != nil {
		return false
	}
	_, err = os.Stat(strings.TrimSpace(string(out)))
	return err == nil
}

func runGateCmd(ctx context.Context, dir string, pushFollows bool) (string, error) {
	// --fuzz: a local gate skips the fuzz gate, and a pre-push hook runs it,
	// so land gates with it for the hook's pass to match.
	args := []string{"-C", dir, "--fuzz"}
	// A pre-push hook that runs gate turns --isolate on by itself, and gate
	// reuses only a pass that ran the same way, so gate here as the hook
	// will and the push does not run everything a second time. Without a push
	// no hook runs, so there is nothing to match.
	if pushFollows && hasPrePushHook(ctx, dir) {
		args = append(args, "--isolate")
	}
	cmd := exec.CommandContext(ctx, "gate", args...) //nolint:gosec // gate argv is constructed by rgit, never a shell string
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// gatePassRecorded reads `gate --json record`: a fresh, complete, exit-0 pass of head on a clean tree.
func gatePassRecorded(ctx context.Context, dir, head string) bool {
	cmd := exec.CommandContext(ctx, "gate", "-C", dir, "--json", "record") //nolint:gosec // gate argv is constructed by rgit, never a shell string
	out, _ := cmd.Output()
	var rec struct {
		Head    string `json:"head"`
		Dirty   bool   `json:"dirty"`
		Partial bool   `json:"partial"`
		Exit    int    `json:"exit"`
		Fresh   bool   `json:"fresh"`
		Current struct {
			Head  string `json:"head"`
			Dirty bool   `json:"dirty"`
		} `json:"current"`
	}
	if json.NewDecoder(bytes.NewReader(out)).Decode(&rec) != nil {
		return false
	}
	head = strings.TrimSpace(head)
	return rec.Fresh && rec.Exit == 0 && !rec.Partial && !rec.Dirty && !rec.Current.Dirty && rec.Head == head && rec.Current.Head == head
}

// failedGate is one red gate from gate's result record.
type failedGate struct {
	Name string
	Log  string
}

// gateFailuresRecorded reads the per-gate verdicts in `gate --json record`, so a red gate is named from
// gate's data rather than its display text. Allowed failures and passes are left out.
func gateFailuresRecorded(ctx context.Context, dir string) []failedGate {
	cmd := exec.CommandContext(ctx, "gate", "-C", dir, "--json", "record") //nolint:gosec // gate argv is constructed by rgit, never a shell string
	out, _ := cmd.Output()
	var rec struct {
		Gates []struct {
			Name    string `json:"name"`
			Display string `json:"display"`
			Status  string `json:"status"`
			Log     string `json:"log"`
			Allowed bool   `json:"allowed"`
		} `json:"gates"`
	}
	if json.NewDecoder(bytes.NewReader(out)).Decode(&rec) != nil {
		return nil
	}
	var failed []failedGate
	for _, g := range rec.Gates {
		if g.Status == "ok" || g.Status == "skipped" || g.Allowed {
			continue
		}
		name := g.Name
		if name == "" {
			name = g.Display
		}
		failed = append(failed, failedGate{Name: name, Log: g.Log})
	}
	return failed
}

type ghCLI struct {
	runFn func(dir string, args ...string) (string, error)
}

func newGHCLI(ctx context.Context, step *string) *ghCLI {
	return &ghCLI{runFn: func(dir string, args ...string) (string, error) {
		setLandStep(step, "gh")
		return runGH(ctx, dir, args...)
	}}
}

func runGH(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...) //nolint:gosec // gh argv is constructed by rgit, never a shell string
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
	out, err := g.runFn(dir, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(out), err)
	}
	return nil
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
	// ctx is cancelled by SIGINT/SIGTERM; the poll loops stop on it. Nil never cancels.
	ctx context.Context //nolint:containedctx // per-run dependency bag, bound to the run's signal context
	// step is the named land step running when a signal arrives (gate, git, gh, wait).
	step  *string
	git   gitRunner
	gh    gitHub
	clock landClock
	gate  func(dir string, pushFollows bool) (string, error)
	// passed reports whether gate's result record shows a clean pass of exactly head in dir.
	passed func(dir, head string) bool
	// failedGates lists the gates gate's result record in dir marks failed and not excused; nil when there is no record.
	failedGates func(dir string) []failedGate
	cacheHome   func() string
	pid         int
	sha         string
	noGate      bool
	asJSON      bool
	// fetched is set when catchUp has just fetched origin for the repo landOne is about to read.
	fetched bool
	stdout  io.Writer
	stderr  io.Writer
	// branchJSON receives --branch's result line when --json is set.
	branchJSON *landBranchJSON
}

// info is where progress lines go: stdout, except under --json where stdout holds one object.
func (d landDeps) info() io.Writer {
	if d.asJSON {
		return d.stderr
	}
	return d.stdout
}

type landBranchJSON struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
	Log    string `json:"log"`
	Route  string `json:"route"`
	// Code is the exit code, so a failed land still leaves one object on stdout.
	Code int `json:"code"`
}

func defaultLandDeps(ctx context.Context, stdout, stderr io.Writer) landDeps {
	step := new(string)
	return landDeps{
		ctx:   ctx,
		step:  step,
		git:   execGit{ctx: ctx, step: step},
		gh:    newGHSettingsCache(newGHCLI(ctx, step), githubSettingsPath()),
		clock: realClock{ctx},
		gate: func(dir string, pushFollows bool) (string, error) {
			setLandStep(step, "gate")
			return runGateCmd(ctx, dir, pushFollows)
		},
		passed: func(dir, head string) bool {
			setLandStep(step, "gate")
			return gatePassRecorded(ctx, dir, head)
		},
		failedGates: func(dir string) []failedGate { return gateFailuresRecorded(ctx, dir) },
		cacheHome:   cacheHome,
		pid:         os.Getpid(),
		stdout:      stdout,
		stderr:      stderr,
	}
}

func (d landDeps) cancelled() bool { return d.ctx != nil && d.ctx.Err() != nil }

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

// landValueFlags maps each flag that takes a value to what its missing-value error asks for.
var landValueFlags = map[string]string{
	"--branch": "a name",
	"--sha":    "a commit",
}

func runLand(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	return runLandWith(ctx, dir, args, defaultLandDeps(ctx, stdout, stderr))
}

func runLandWith(ctx context.Context, dir string, args []string, d landDeps) exitcode.Code {
	wait := false
	install := false
	push := false
	dryRun := false
	argJSON := false
	branch := ""
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
		case a == "--install":
			install = true
		case a == "--push":
			push = true
		case a == "--dry-run":
			dryRun = true
		case a == "--no-gate":
			d.noGate = true
		case a == "--json":
			argJSON = true
		case a == "--repos":
			reposFlag = true
		case landValueFlags[a] != "":
			val, next, ok := landFlagValue(args, i)
			if !ok {
				fmt.Fprintf(d.stderr, "rgit land: %s needs %s\n", a, landValueFlags[a])
				fmt.Fprintln(d.stderr, landUsageLine)
				return exitcode.InvalidUsage
			}
			i = next
			switch a {
			case "--branch":
				branch = val
			case "--sha":
				d.sha = val
			}
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(d.stderr, "rgit land: unknown argument: %s\n", a)
			fmt.Fprintln(d.stderr, landUsageLine)
			return exitcode.InvalidUsage
		default:
			repos = append(repos, a)
		}
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
	d.asJSON = jsonFlag(ctx) || argJSON
	if dryRun {
		if branch != "" || d.sha != "" || push || wait || install {
			fmt.Fprintln(d.stderr, "rgit land: --dry-run combines only with --repos, --no-gate and --json")
			fmt.Fprintln(d.stderr, landUsageLine)
			return exitcode.InvalidUsage
		}
		return landDryRun(repos, d.asJSON, d)
	}
	return landDispatch(ctx, repos, branch, push, wait, install, d.asJSON, d)
}

func landDispatch(ctx context.Context, repos []string, branch string, push, wait, install, asJSON bool, d landDeps) exitcode.Code {
	if d.sha != "" && branch != "" {
		fmt.Fprintln(d.stderr, "rgit land: --sha does not combine with --branch")
		fmt.Fprintln(d.stderr, landUsageLine)
		return exitcode.InvalidUsage
	}
	if branch != "" {
		if d.noGate {
			fmt.Fprintln(d.stderr, "rgit land: --no-gate does not combine with --branch")
			fmt.Fprintln(d.stderr, landUsageLine)
			return exitcode.InvalidUsage
		}
		if len(repos) > 1 {
			fmt.Fprintln(d.stderr, "rgit land: --branch lands one repo")
			fmt.Fprintln(d.stderr, landUsageLine)
			return exitcode.InvalidUsage
		}
		if !asJSON {
			return landSignalExit(landBranch(ctx, repos[0], branch, wait, install, !push, d), d)
		}
		out := landBranchJSON{Repo: repos[0], Branch: branch}
		d.branchJSON = &out
		code := landSignalExit(landBranch(ctx, repos[0], branch, wait, install, !push, d), d)
		if code != exitcode.Interrupted {
			out.Code = int(code)
			_ = json.NewEncoder(d.stdout).Encode(out)
		}
		return code
	}
	if push {
		fmt.Fprintln(d.stderr, "rgit land: --push needs --branch")
		fmt.Fprintln(d.stderr, landUsageLine)
		return exitcode.InvalidUsage
	}
	return landSignalExit(landRepos(ctx, repos, wait, asJSON, install, d), d)
}

func landSignalExit(code exitcode.Code, d landDeps) exitcode.Code {
	if !d.cancelled() {
		return code
	}
	step := "land"
	if d.step != nil && *d.step != "" {
		step = *d.step
	}
	fmt.Fprintf(d.stderr, "rgit land: interrupted (%s)\n", step)
	return exitcode.Interrupted
}

func landFlagValue(args []string, i int) (value string, next int, ok bool) {
	tail := args[i:]
	if len(tail) < 2 {
		return "", i, false
	}
	return tail[1], i + 1, true
}

func landRepos(ctx context.Context, repos []string, wait, asJSON, install bool, d landDeps) exitcode.Code {
	ordered := orderRepos(repos)
	deps := map[string][]string{}
	if d.sha == "" {
		deps = pinDeps(ordered, d)
		ordered = orderByPins(ordered, deps)
	}
	redRepos := map[string]bool{}
	var results []landResult
	red, subRed, caughtUpFail, blocked, notEnqueued := false, false, false, false, false
	for _, r := range ordered {
		od := d
		if d.sha == "" {
			failed, fetched := catchUp(ctx, r, d)
			caughtUpFail = caughtUpFail || failed
			od.fetched = fetched
		}
		if subRed && isUmbrella(r) {
			// An umbrella pushed past a red submodule can point at a commit that never reached its remote.
			results = append(results, landResult{Repo: r, Kind: landSkipped, Lines: []string{r + ": skipped (a submodule repo is red)"}})
			continue
		}
		if pinned := redDep(deps[r], redRepos); pinned != "" {
			// The consumer's pin names a commit of a repo that was not pushed, so the remote could never resolve it.
			results = append(results, landResult{Repo: r, Kind: landSkipped, Lines: []string{r + ": skipped (pinned repo " + pinned + " is red)"}})
			continue
		}
		res := landOne(r, od)
		results = append(results, res)
		blocked = blocked || skipNeedsAction(res)
		notEnqueued = notEnqueued || res.NotEnqueued
		if res.Kind == landRed {
			red = true
			redRepos[r] = true
			subRed = subRed || !isUmbrella(r)
		}
	}

	return finishLandRepos(ctx, results, wait, asJSON, install, red, caughtUpFail, blocked, notEnqueued, d)
}

func finishLandRepos(ctx context.Context, results []landResult, wait, asJSON, install, red, caughtUpFail, blocked, notEnqueued bool, d landDeps) exitcode.Code {
	if d.cancelled() {
		return exitcode.Interrupted
	}

	for i, res := range results {
		// An unqueued PR never merges by itself, so polling it only burns the cap.
		if res.Kind != landPR || res.PRState == "merged" || res.PRState == "" || res.URL == "" {
			continue
		}
		job := landWatchJob{
			Repo:    res.Repo,
			Slug:    res.Slug,
			PR:      res.PRNumber,
			URL:     res.URL,
			Default: res.Default,
		}
		if wait {
			fin := watchUntilDone(job, d)
			results[i].Watch = &fin
			results[i].Lines = append(append([]string{}, res.Lines...), finalWaitLine(res, fin))
		}
	}

	if d.cancelled() {
		return exitcode.Interrupted
	}

	if asJSON {
		enc := json.NewEncoder(d.stdout)
		_ = enc.Encode(landJSON{Repos: jsonResults(results)})
		// Stdout holds the one object; the result lines go where --branch's do, for nomad-hud's watch.
		for _, res := range results {
			for _, line := range res.Lines {
				fmt.Fprintln(d.stderr, line)
			}
		}
	} else {
		for _, res := range results {
			out := d.stdout
			if res.Moved {
				out = d.stderr
			}
			for _, line := range res.Lines {
				fmt.Fprintln(out, line)
			}
		}
	}

	instFail := false
	if install {
		instFail = installPushedRepos(ctx, results, d)
	}

	return landExit(red, instFail || caughtUpFail, blocked, notEnqueued)
}

func landExit(red, installFailed, blocked, notEnqueued bool) exitcode.Code {
	switch {
	case red:
		return exitcode.LandRed
	case notEnqueued:
		return exitcode.LandNotEnqueued
	case installFailed:
		return exitcode.LandInstallFailed
	case blocked:
		return exitcode.LandSkipped
	}
	return exitcode.Success
}

// skipNeedsAction is true for a skip the user must fix (dirty tree, wrong branch, behind origin,
// no upstream, not a repo); "not ahead" and "already upstream" mean there was nothing to land.
func skipNeedsAction(res landResult) bool {
	if res.Kind != landSkipped {
		return false
	}
	reason := skipReason(res.Lines)
	return reason != "not ahead" && reason != "already upstream"
}

var prNumberInSubject = regexp.MustCompile(`#(\d+)`)

// catchUp fast-forwards a clean default branch to a remote that gained commits since the last land (a rebase-merged PR has no #N),
// then installs, so a PR that an earlier land opened and bosun saw merge is picked up before new work.
// It reports whether the install failed and whether it fetched origin.
func catchUp(ctx context.Context, r string, d landDeps) (installFailed, fetched bool) {
	repo, err := toplevel(r, d)
	if err != nil {
		return false, false
	}
	def, ok := defaultBranchChecked(repo, d)
	if cur, err := currentBranch(repo, d); !ok || err != nil || cur != def || dirtyTracked(repo, d) {
		return false, false
	}
	remote := upstreamRemote(repo, def, d)
	if _, err := d.git.run(repo, "fetch", "-q", remote); err != nil {
		return false, false
	}
	if n, err := d.git.run(repo, "rev-list", "--count", remote+"/"+def+"..HEAD"); err != nil || strings.TrimSpace(n) != "0" {
		return false, true
	}
	subjects, err := d.git.run(repo, "log", "--format=%s", "HEAD.."+remote+"/"+def)
	if err != nil {
		return false, true
	}
	if strings.TrimSpace(subjects) == "" {
		return false, true
	}
	pr := ""
	for line := range strings.SplitSeq(subjects, "\n") {
		if m := prNumberInSubject.FindStringSubmatch(line); m != nil {
			pr = m[1]
			break
		}
	}
	before := strings.TrimSpace(mustGit(repo, d, "rev-parse", "HEAD"))
	fastForwardDefaultAfterMerge(landWatchJob{Repo: repo, Default: def}, d)
	after := strings.TrimSpace(mustGit(repo, d, "rev-parse", "HEAD"))
	if after == before {
		return false, true
	}
	short := strings.TrimSpace(mustGit(repo, d, "rev-parse", "--short", "HEAD"))
	merged := ""
	if pr != "" {
		merged = " (merged PR #" + pr + ")"
	}
	fmt.Fprintf(d.info(), "%s: caught up to %s%s\n", repo, short, merged)
	return installOne(ctx, repo, d), true
}

func installPushedRepos(ctx context.Context, results []landResult, d landDeps) bool {
	failed := false
	for _, res := range results {
		if res.Kind != landPushed {
			continue
		}
		if head := strings.TrimSpace(mustGit(res.Repo, d, "rev-parse", "HEAD")); d.sha == "" && head != res.SHA {
			fmt.Fprintf(d.info(), "%s: skipped install (HEAD %s is not the pushed %s)\n", res.Repo, head, res.SHA)
			failed = true
			continue
		}
		if installOne(ctx, res.Repo, d) {
			failed = true
		}
	}
	return failed
}

func makefileName(dir string) string {
	for _, name := range []string{"GNUmakefile", "Makefile", "makefile"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return name
		}
	}
	return ""
}

func makeHasInstall(ctx context.Context, dir string) bool {
	cmd := exec.CommandContext(ctx, "make", "-q", "install")
	cmd.Dir = dir
	cmd.Stdin = nil
	if err := cmd.Run(); err == nil {
		return true
	}
	if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 1 {
		return true
	}
	return false
}

func runMakeInstall(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "make", "install")
	cmd.Dir = dir
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func installOne(ctx context.Context, repo string, d landDeps) bool {
	if dirtyTracked(repo, d) {
		fmt.Fprintln(d.info(), dirtySkipLine(repo, d))
		return false
	}
	if makefileName(repo) == "" {
		fmt.Fprintln(d.info(), repo+": skipped (no Makefile)")
		return false
	}
	if !makeHasInstall(ctx, repo) {
		fmt.Fprintln(d.info(), repo+": skipped (no install target)")
		return false
	}
	out, err := runMakeInstall(ctx, repo)
	if err != nil {
		lines := tail(out, 40)
		if len(lines) == 0 {
			fmt.Fprintln(d.info(), repo+": "+err.Error())
		} else {
			for _, line := range lines {
				fmt.Fprintln(d.info(), line)
			}
		}
		return true
	}
	describe := lastNonEmptyLine(out)
	if describe == "" {
		describe = "make install"
	}
	fmt.Fprintln(d.info(), repo+": installed "+describe)
	return false
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
	Route    string
	Queue    bool
	Log      string // gate log of a red gate
	Moved    bool   // push rejected because origin moved (no-rebase route)
	// NotEnqueued is a PR that is neither queued nor set to auto-merge, or whose state is unknown.
	NotEnqueued bool
	SHA         string // pushed commit
	Watch       *landWatchResult
}

type landWatchJob struct {
	Repo    string `json:"repo"`
	Slug    string `json:"slug"`
	PR      int    `json:"pr"`
	URL     string `json:"url"`
	Default string `json:"default"`
}

type landWatchResult struct {
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
	Path   string           `json:"path"`
	Status string           `json:"status"`
	URL    string           `json:"url,omitempty"`
	State  string           `json:"state,omitempty"`
	Detail string           `json:"detail,omitempty"`
	SHA    string           `json:"sha,omitempty"`
	Log    string           `json:"log,omitempty"`
	Watch  *landWatchResult `json:"watch,omitempty"`
}

func jsonResults(results []landResult) []landJSONRepo {
	out := make([]landJSONRepo, 0, len(results))
	for _, res := range results {
		jr := landJSONRepo{Path: res.Repo, URL: res.URL, State: res.PRState, Watch: res.Watch}
		switch res.Kind {
		case landPushed:
			jr.Status = "pushed"
			jr.SHA = res.SHA
		case landSkipped:
			jr.Status = "skipped"
			jr.Detail = skipReason(res.Lines)
		case landRed:
			jr.Status = "red"
			jr.Detail = strings.Join(res.Lines, "\n")
			jr.Log = res.Log
		case landPR:
			jr.Status = "pr"
		}
		out = append(out, jr)
	}
	return out
}

// skipReason is the parenthesised reason of a `DIR: skipped (reason)` line.
func skipReason(lines []string) string {
	for _, l := range lines {
		if _, rest, ok := strings.Cut(l, ": skipped ("); ok {
			return strings.TrimSuffix(rest, ")")
		}
	}
	return ""
}

func isUmbrella(r string) bool {
	_, err := os.Stat(filepath.Join(r, ".gitmodules")) //nolint:gosec,nolintlint // r is a land repo path; gosec's G703 taint pass flags this only on some runs
	return err == nil
}

func orderRepos(repos []string) []string {
	var ordered, umbrellas []string
	for _, r := range repos {
		if isUmbrella(r) {
			umbrellas = append(umbrellas, r)
		} else {
			ordered = append(ordered, r)
		}
	}
	return append(ordered, umbrellas...)
}

func landOne(r string, d landDeps) landResult {
	repo, err := toplevel(r, d)
	if err != nil {
		return landResult{Repo: r, Kind: landSkipped, Lines: []string{r + ": skipped (not a git repo)"}}
	}
	// Another land of this repo would gate and push the same commits; wait for it, then see what it left.
	unlock, err := gitx.New(repo).LockLand(d.ctx)
	if err != nil {
		return landResult{Repo: repo, Kind: landSkipped, Lines: []string{repo + ": skipped (cannot take the land lock: " + err.Error() + ")"}}
	}
	defer unlock()
	res := landResult{Repo: repo}

	def, ok := defaultBranchChecked(repo, d)
	if !ok {
		res.Kind = landSkipped
		res.Lines = []string{repo + ": skipped (cannot determine default branch)"}
		return res
	}
	res.Default = def
	if d.sha != "" {
		return landSha(repo, def, d)
	}
	if dirtyTracked(repo, d) {
		res.Kind = landSkipped
		res.Lines = []string{dirtySkipLine(repo, d)}
		return res
	}
	cur, err := currentBranch(repo, d)
	if err != nil {
		res.Kind = landSkipped
		res.Lines = []string{repo + ": skipped (cannot determine current branch)"}
		return res
	}
	if cur != def {
		res.Kind = landSkipped
		res.Lines = []string{fmt.Sprintf("%s: skipped (on %s, not %s)", repo, cur, def)}
		return res
	}
	if !d.fetched || upstreamRemote(repo, def, d) != "origin" {
		_, _ = d.git.run(repo, "fetch", "-q")
	}
	ahead, behind, err := aheadBehind(repo, d)
	if err != nil {
		res.Kind = landSkipped
		res.Lines = []string{repo + ": skipped (no upstream)"}
		return res
	}
	if ahead == 0 {
		res.Kind = landSkipped
		res.Lines = []string{repo + ": skipped (not ahead)"}
		return res
	}
	if behind > 0 {
		if _, err := d.git.run(repo, "pull", "-q", "--rebase"); err != nil {
			_, _ = d.git.run(repo, "rebase", "--abort")
			res.Kind = landSkipped
			res.Lines = []string{fmt.Sprintf("%s: skipped (behind %d, rebase not clean)", repo, behind)}
			return res
		}
		if ahead, _, err = aheadBehind(repo, d); err != nil {
			res.Kind = landRed
			res.Lines = []string{summaryRed(repo, err.Error())}
			return res
		}
		if ahead == 0 {
			res.Kind = landSkipped
			res.Lines = []string{summarySkippedAlreadyUpstream(repo)}
			return res
		}
	}

	gated := strings.TrimSpace(mustGit(repo, d, "rev-parse", "HEAD"))
	if line, log, ok := gateHead(repo, d); !ok {
		res.Kind = landRed
		res.Lines = []string{line}
		res.Log = log
		return res
	}
	// A sibling's edit between the gate's own check and the push would ship HEAD over a tree that was never gated.
	if !d.noGate {
		if msg := gateMoved(repo, gated, "", d); msg != "" {
			res.Kind = landRed
			res.Lines = []string{msg}
			return res
		}
	}
	// Push and branch the gated commit, not whatever HEAD became after the gate.
	res = landByRules(repo, def, d, landPushOpts{remote: upstreamRemote(repo, def, d), pushRef: gated, pushTo: def, prRef: gated, logRange: "@{u}.." + gated})
	if res.Kind == landPR && !flagNotEnqueued(&res) {
		resetToRemote(repo, upstreamRemote(repo, def, d)+"/"+def, gated, d, &res)
	}
	return res
}

// resetToRemote moves the checkout off commits that now live in a PR, only while HEAD is still the
// commit that was branched: a commit made meanwhile is left on the branch with a warning.
func resetToRemote(repo, target, branched string, d landDeps, res *landResult) {
	if head := strings.TrimSpace(mustGit(repo, d, "rev-parse", "HEAD")); head != branched {
		res.Lines = append(res.Lines, repo+": warning: HEAD moved since the PR was opened; local branch not reset")
		return
	}
	if out, err := d.git.run(repo, "reset", "-q", "--keep", target); err != nil {
		res.Lines = append(res.Lines, fmt.Sprintf("%s: warning: local %s not reset (%s); its commits are in the PR, run git reset --keep %s once the tree is clean",
			repo, branchOrHEAD(repo, d), strings.TrimSpace(out), target))
	}
}

// landSha lands one already-committed ahead commit by the repo's branch rules. The working tree and
// the local default branch stay as they are, so a dirty checkout or newer unpushed commits do not block it.
func landSha(repo, def string, d landDeps) landResult {
	res := landResult{Repo: repo, Default: def, Kind: landSkipped}
	sha, err := d.git.run(repo, "rev-parse", "--verify", "-q", d.sha+"^{commit}")
	sha = strings.TrimSpace(sha)
	if err != nil || sha == "" {
		res.Lines = []string{fmt.Sprintf("%s: skipped (no commit %s)", repo, d.sha)}
		return res
	}
	cur, err := currentBranch(repo, d)
	if err != nil {
		res.Lines = []string{repo + ": skipped (cannot determine current branch)"}
		return res
	}
	if cur != def {
		res.Lines = []string{fmt.Sprintf("%s: skipped (on %s, not %s)", repo, cur, def)}
		return res
	}
	_, _ = d.git.run(repo, "fetch", "-q")
	if _, err := d.git.run(repo, "merge-base", "--is-ancestor", sha, "HEAD"); err != nil {
		res.Lines = []string{fmt.Sprintf("%s: skipped (%s is not on %s)", repo, d.sha, def)}
		return res
	}
	if _, err := d.git.run(repo, "rev-parse", "--verify", "-q", "@{u}"); err != nil {
		res.Lines = []string{repo + ": skipped (no upstream)"}
		return res
	}
	if _, err := d.git.run(repo, "merge-base", "--is-ancestor", sha, "@{u}"); err == nil {
		res.Lines = []string{summarySkippedAlreadyUpstream(repo)}
		return res
	}
	if !d.noGate {
		if dirtyTracked(repo, d) {
			res.Lines = []string{dirtySkipLine(repo, d)}
			return res
		}
		if head := strings.TrimSpace(mustGit(repo, d, "rev-parse", "HEAD")); head != sha {
			res.Kind = landRed
			res.Lines = []string{summaryRed(repo, "--sha can be gated only as HEAD; nothing pushed (--no-gate if already gated)")}
			return res
		}
		if line, log, ok := gateHead(repo, d); !ok {
			res.Kind = landRed
			res.Lines = []string{line}
			res.Log = log
			return res
		}
	}
	res = landByRules(repo, def, d, landPushOpts{remote: upstreamRemote(repo, def, d), pushRef: sha, pushTo: def, prRef: sha, logRange: "@{u}.." + sha})
	if res.Kind == landPR {
		flagNotEnqueued(&res)
	}
	return res
}

// flagNotEnqueued marks a PR that is neither queued nor set to auto-merge, or whose state is
// unknown, and puts the note first in its lines.
func flagNotEnqueued(res *landResult) bool {
	var note string
	switch res.PRState {
	case prStateUnknown:
		note = res.Repo + ": note: PR state unknown (lookup failed)"
	case "":
		note = res.Repo + ": note: PR open but neither queued nor set to auto-merge"
	default:
		return false
	}
	res.NotEnqueued = true
	res.Lines = append([]string{note}, res.Lines...)
	return true
}

// gateHead runs the repo's full gate on its HEAD before anything is pushed. It returns the
// summary line when the gate is red or HEAD or the tree moved during it.
func gateHead(repo string, d landDeps) (line, logPath string, ok bool) {
	if d.noGate {
		return "", "", true
	}
	// The gate runs in this checkout, so an untracked file could make it pass for a commit that lacks it.
	if out, _ := d.git.run(repo, "ls-files", "-z", "--others", "--exclude-standard"); strings.TrimSpace(out) != "" {
		return repo + ": error: untracked files (" + strings.Join(gitx.SplitNUL([]byte(out)), ", ") + "); add or ignore them first, nothing pushed", "", false
	}
	headBefore, statusBefore := gateState(repo, d)
	// An edit that landed after the dirty check would be gated with the tree and then ride along unpushed.
	if strings.TrimSpace(statusBefore) != "" {
		return repo + ": error: tracked files changed before the gate (" + dirtyNames(statusBefore) + "); nothing pushed", "", false
	}
	if d.passed != nil && d.passed(repo, headBefore) {
		fmt.Fprintf(d.info(), "%s: gate: reused the recorded pass of %s\n", repo, strings.TrimSpace(headBefore)[:min(7, len(strings.TrimSpace(headBefore)))])
		return "", "", true
	}
	out, gateErr := d.gate(repo, true)
	logdir := filepath.Join(d.cacheHome(), "land")
	sum := sha256.Sum256([]byte(repo))
	logPath = filepath.Join(logdir, filepath.Base(repo)+"-"+hex.EncodeToString(sum[:4])+"-"+strconv.Itoa(d.pid)+".log")
	logBody := out
	if gateErr != nil && out == "" {
		logBody = gateErr.Error() + "\n"
	}
	if err := os.MkdirAll(logdir, 0o750); err == nil {
		_ = os.WriteFile(logPath, []byte(logBody), 0o600)
	}
	if gateErr != nil {
		var names string
		if d.failedGates != nil {
			if fg := d.failedGates(repo); len(fg) > 0 {
				var ns []string
				for _, g := range fg {
					ns = append(ns, g.Name)
				}
				names = strings.Join(ns, ",")
				if fg[0].Log != "" {
					logPath = fg[0].Log
				}
			}
		}
		if names == "" {
			names = failedGateNames(out)
		}
		if names == "?" {
			if names = redDetail(out); names == "" {
				names = gateErr.Error()
			}
		}
		return repo + ": red (gate failed: " + names + "; log " + logPath + ")", logPath, false
	}
	if msg := gateMoved(repo, headBefore, statusBefore, d); msg != "" {
		return msg, "", false
	}
	return "", "", true
}

func gateState(repo string, d landDeps) (head, status string) {
	st, _ := d.git.run(repo, "status", "--porcelain=v1", "-z", "-uno")
	return mustGit(repo, d, "rev-parse", "HEAD"), st
}

// gateMoved is the refusal line when HEAD or the tracked tree differs from what was gated.
func gateMoved(repo, headBefore, statusBefore string, d landDeps) string {
	head, status := gateState(repo, d)
	if strings.TrimSpace(head) != strings.TrimSpace(headBefore) {
		return repo + ": error: HEAD moved during the gate; nothing pushed, re-run"
	}
	if status != statusBefore {
		return repo + ": error: gate modified tracked files (" + dirtyNames(status) + "); nothing pushed, commit or revert them"
	}
	return ""
}

type landPushOpts struct {
	// remote is where the branch rules are read and the push goes; origin when empty.
	remote   string
	pushDir  string
	pushRef  string
	pushTo   string
	prRef    string
	logRange string
	// afterGate words the origin-moved error for a push that follows a gate.
	afterGate bool
	// slug is the GitHub slug of the push's remote, so a refusal can drop its cached settings.
	slug string
}

func landByRules(repo, def string, d landDeps, opts landPushOpts) landResult {
	res := landResult{Repo: repo, Default: def}
	pushDir := opts.pushDir
	if pushDir == "" {
		pushDir = repo
	}
	prRef := opts.prRef
	if prRef == "" {
		prRef = "HEAD"
	}
	logRange := opts.logRange
	if logRange == "" {
		logRange = "@{u}..HEAD"
	}

	remote := opts.remote
	if remote == "" {
		remote = "origin"
	}
	origin := strings.TrimSpace(mustGit(repo, d, "config", "--get", "remote."+remote+".url"))
	slug := slugFromOrigin(origin)
	res.Slug = slug
	opts.slug = slug
	var rules []string
	if slug != "" {
		rules = lookupRules(slug, def, d)
	}
	needPR, mergeQueue := rulesNeedPR(rules)
	res.Queue = mergeQueue
	if !needPR {
		if direct, done := pushDirect(repo, pushDir, remote, d, opts, res); done {
			return direct
		}
	}

	br := "land/" + d.clock.Now().UTC().Format("20060102-150405")
	subj := strings.TrimSpace(mustGit(repo, d, "log", "--format=- %s", logRange))
	if _, err := d.git.run(repo, "branch", br, prRef); err != nil {
		res.Kind = landRed
		res.Lines = []string{summaryRed(repo, err.Error())}
		return res
	}
	pushOut, err := d.git.run(repo, "push", "--recurse-submodules=check", "-u", remote, br)
	if err != nil {
		dropSettingsOnHint(d.gh, slug, pushOut)
		res.Kind = landRed
		res.Lines = []string{summaryRed(repo, pushOut)}
		return res
	}
	// The remote copy carries the PR; the local ref only fed the push.
	defer func() { _, _ = d.git.run(repo, "branch", "-D", br) }()
	url, err := d.gh.createPR(repo, br, subj)
	if err != nil {
		dropSettingsOnHint(d.gh, slug, err.Error())
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
	if err := d.gh.mergePR(repo, br, false, ""); err != nil {
		dropSettingsOnHint(d.gh, slug, err.Error())
	}
	// strategy is empty for a merge queue, so the queued request names no merge method.
	if err := d.gh.mergePR(repo, br, true, strategy); err != nil {
		dropSettingsOnHint(d.gh, slug, err.Error())
	}
	num, _ := d.gh.prNumber(repo, br)
	res.PRNumber = num
	queued := ""
	if owner, name, ok := splitSlug(slug); ok && num > 0 {
		info, err := d.gh.pullRequest(owner, name, num)
		if err != nil {
			queued = prStateUnknown
		} else {
			queued = judgePRState(info)
			if res.URL == "" {
				res.URL = info.URL
			}
		}
	}
	res.PRState = queued
	if mergeQueue {
		res.Route = "queue"
	} else {
		res.Route = "PR"
	}
	res.Kind = landPR
	res.Lines = append(res.Lines, summaryPR(repo, res.URL, queued))
	return res
}

type landBranchPrep struct {
	repo, branch, def, wt string
	dirty, noPush         bool
	// wtCreated is true when land made wt itself; a worktree that was already there is removed only while it holds no ignored files.
	wtCreated bool
	// gated is the worktree HEAD the gate ran on.
	gated string
	opts  landPushOpts
}

func landBranch(ctx context.Context, repoArg, branch string, wait, install, noPush bool, d landDeps) exitcode.Code {
	unlock, err := gitx.New(repoArg).LockLand(ctx)
	if err != nil {
		fmt.Fprintf(d.stderr, "error: cannot take the land lock for %s: %v\n", repoArg, err)
		return exitcode.GitFailure
	}
	defer unlock()
	prep, code := prepareLandBranch(repoArg, branch, noPush, d)
	if code != exitcode.Success {
		return code
	}
	prep.gated = strings.TrimSpace(mustGit(prep.wt, d, "rev-parse", "HEAD"))
	logPath, code := gateLandBranch(prep, d)
	if code != exitcode.Success {
		if d.branchJSON != nil {
			d.branchJSON.Log = logPath
		}
		return code
	}
	return finishLandBranch(ctx, prep, logPath, wait, install, noPush, d)
}

func prepareLandBranch(repoArg, branch string, noPush bool, d landDeps) (landBranchPrep, exitcode.Code) {
	repo, err := toplevel(repoArg, d)
	if err != nil {
		fmt.Fprintf(d.stderr, "error: %s is not a git repo\n", repoArg)
		return landBranchPrep{}, exitcode.GitFailure
	}
	if isLinkedWorktree(repo, d) {
		fmt.Fprintf(d.stderr, "error: %s is not the main checkout\n", repo)
		return landBranchPrep{}, exitcode.InvalidUsage
	}
	def, ok := defaultBranchChecked(repo, d)
	if !ok {
		fmt.Fprintf(d.stderr, "error: cannot determine default branch for %s\n", repo)
		return landBranchPrep{}, exitcode.GitFailure
	}
	dirty := dirtyTracked(repo, d)
	cur, err := currentBranch(repo, d)
	if err != nil {
		fmt.Fprintf(d.stderr, "error: cannot determine current branch for %s\n", repo)
		return landBranchPrep{}, exitcode.GitFailure
	}
	if !dirty || noPush {
		if cur != def {
			fmt.Fprintf(d.stderr, "error: %s is on %s, not %s\n", repo, cur, def)
			return landBranchPrep{}, exitcode.InvalidUsage
		}
	}
	if _, err := d.git.run(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		fmt.Fprintf(d.stderr, "error: branch %s does not exist\n", branch)
		return landBranchPrep{}, exitcode.InvalidUsage
	}
	wt, found := findBranchWorktree(repo, branch, d)
	if !found {
		tmp, err := os.MkdirTemp("", "land-branch.")
		if err != nil {
			fmt.Fprintf(d.stderr, "error: %v\n", err)
			return landBranchPrep{}, exitcode.GitFailure
		}
		_ = os.Remove(tmp)
		if out, err := d.git.run(repo, "worktree", "add", tmp, branch); err != nil {
			fmt.Fprintf(d.stderr, "error: %s\n", strings.TrimSpace(out))
			return landBranchPrep{}, exitcode.GitFailure
		}
		wt = tmp
	}
	prep := landBranchPrep{repo: repo, branch: branch, def: def, wt: wt, dirty: dirty, noPush: noPush, wtCreated: !found}
	base := def
	if !noPush {
		_, _ = d.git.run(repo, "fetch", "-q", upstreamRemote(repo, def, d))
	}
	if !noPush && !dirty {
		// A clean main that fell behind its remote would make the push look like a remote move forever.
		_, _ = d.git.run(repo, "merge", "--ff-only", upstreamRemote(repo, def, d)+"/"+def)
	}
	if dirty && !noPush {
		base = upstreamRemote(repo, def, d) + "/" + def
		prep.opts = landPushOpts{
			pushDir:  wt,
			pushTo:   def,
			prRef:    branch,
			logRange: upstreamRemote(repo, def, d) + "/" + def + ".." + branch,
		}
	}
	if !dirty && !noPush {
		// Gate and push from the one worktree so a pre-push hook's gate sees the root already gated.
		prep.opts = landPushOpts{pushDir: wt, pushTo: def}
	}
	prep.opts.afterGate = true
	if dirty := dirtyTracked(wt, d); dirty {
		st, _ := d.git.run(wt, "status", "--porcelain=v1", "-z", "-uno")
		fmt.Fprintf(d.stderr, "error: worktree %s has uncommitted changes (%s); %s not rebased\n", wt, dirtyNames(st), branch)
		return landBranchPrep{}, exitcode.LandSkipped
	}
	if _, err := d.git.run(wt, "rebase", base); err != nil {
		files, _ := d.git.run(wt, "diff", "--name-only", "-z", "--diff-filter=U")
		_, _ = d.git.run(wt, "rebase", "--abort")
		fmt.Fprintf(d.stderr, "conflict: hand to solver (worktree %s, files %s)\n", wt, strings.Join(gitx.SplitNUL([]byte(files)), ", "))
		return landBranchPrep{}, exitcode.LandConflict
	}
	return prep, exitcode.Success
}

func gateLandBranch(prep landBranchPrep, d landDeps) (string, exitcode.Code) {
	logdir := filepath.Join(d.cacheHome(), "land-branch")
	if err := os.MkdirAll(logdir, 0o750); err != nil {
		fmt.Fprintf(d.stderr, "error: %v\n", err)
		return "", exitcode.GitFailure
	}
	safe := strings.ReplaceAll(prep.branch, "/", "_")
	logPath := filepath.Join(logdir, filepath.Base(prep.repo)+"-"+safe+"-"+strconv.Itoa(d.pid)+".log")
	// Linked worktrees omit gitignored env files the main checkout uses for the build.
	links := linkIgnoredEnvFiles(prep.repo, prep.wt, d)
	defer unlinkLandEnvLinks(links)
	pushFollows := !prep.noPush
	headBefore, statusBefore := gateState(prep.wt, d)
	moved := func() exitcode.Code {
		if msg := gateMoved(prep.wt, headBefore, statusBefore, d); msg != "" {
			fmt.Fprintln(d.stderr, msg)
			return exitcode.LandSkipped
		}
		return exitcode.Success
	}
	out, gateErr := d.gate(prep.wt, pushFollows)
	if werr := os.WriteFile(logPath, []byte(out), 0o600); werr != nil {
		fmt.Fprintf(d.stderr, "error: %v\n", werr)
		return "", exitcode.GitFailure
	}
	if d.cancelled() {
		return logPath, exitcode.Interrupted
	}
	if gateErr == nil {
		return logPath, moved()
	}
	fmt.Fprintln(d.stderr, logPath)
	for _, line := range tail(out, 40) {
		fmt.Fprintln(d.stderr, line)
	}
	return logPath, exitcode.LandGateRed
}

func isIgnoredEnvName(base string) bool {
	if base == ".env" || base == ".env.local" {
		return true
	}
	return strings.HasPrefix(base, ".env.") && strings.HasSuffix(base, ".local") &&
		len(base) > len(".env.")+len(".local")
}

func linkIgnoredEnvFiles(repo, wt string, d landDeps) []string {
	if repo == "" || wt == "" || repo == wt {
		return nil
	}
	out, err := d.git.run(repo, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--",
		".env", ".env.local", ".env.*.local",
		":(glob)**/.env", ":(glob)**/.env.local", ":(glob)**/.env.*.local")
	if err != nil {
		return nil
	}
	var made []string
	for rel := range strings.SplitSeq(out, "\x00") {
		if rel == "" || !isIgnoredEnvName(filepath.Base(rel)) {
			continue
		}
		dst := filepath.Join(wt, filepath.FromSlash(rel))
		if _, err := os.Lstat(dst); err == nil || !os.IsNotExist(err) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			continue
		}
		if err := os.Symlink(filepath.Join(repo, filepath.FromSlash(rel)), dst); err != nil {
			continue
		}
		made = append(made, dst)
	}
	return made
}

func unlinkLandEnvLinks(links []string) {
	for _, p := range links {
		_ = os.Remove(p)
	}
}

func finishLandBranch(ctx context.Context, prep landBranchPrep, logPath string, wait, install, noPush bool, d landDeps) exitcode.Code {
	// A pushed branch fast-forwards local main only after the push, so a red push leaves main re-runnable.
	ff := func() exitcode.Code {
		if prep.dirty && !noPush {
			return exitcode.Success
		}
		if out, err := d.git.run(prep.repo, "merge", "--ff-only", prep.branch); err != nil {
			if overlap := mergeOverlapFiles(prep.repo, prep.branch, d); noPush && prep.dirty && len(overlap) > 0 {
				fmt.Fprintf(d.stderr, "error: %s has uncommitted changes to %s; %s not landed\n",
					prep.repo, strings.Join(overlap, ", "), prep.branch)
				return exitcode.DirtyOverlap
			}
			fmt.Fprintf(d.stderr, "error: %s\n", strings.TrimSpace(out))
			return exitcode.GitFailure
		}
		head := strings.TrimSpace(mustGit(prep.repo, d, "rev-parse", "HEAD"))
		tip := strings.TrimSpace(mustGit(prep.repo, d, "rev-parse", prep.branch))
		if head != tip {
			fmt.Fprintf(d.stderr, "error: HEAD %s != %s %s\n", head, prep.branch, tip)
			return exitcode.GitFailure
		}
		return exitcode.Success
	}
	if noPush {
		if code := ff(); code != exitcode.Success {
			return code
		}
	}
	sha := strings.TrimSpace(mustGit(prep.repo, d, "rev-parse", prep.branch))
	if noPush {
		// Merged into the local default branch, so the branch and its worktree are done either way.
		cleanupLandBranch(prep, &landResult{Kind: landPushed, Route: "direct"}, true, d)
		printLandBranchLine(prep, sha, logPath, "local", d)
		if install && installOne(ctx, prep.repo, d) {
			return exitcode.LandInstallFailed
		}
		return exitcode.Success
	}
	if prep.gated != "" {
		prep.opts.pushRef = prep.gated
		prep.opts.prRef = prep.gated
	}
	res := landByRules(prep.repo, prep.def, d, prep.opts)
	if res.Kind == landRed {
		out := d.info()
		if res.Moved {
			out = d.stderr
		}
		for _, line := range res.Lines {
			fmt.Fprintln(out, line)
		}
		return exitcode.LandRed
	}
	if !noPush {
		if code := ff(); code != exitcode.Success {
			return code
		}
	}
	if res.Route == "direct" && res.SHA != "" {
		sha = res.SHA
	}
	if res.Kind == landPR && flagNotEnqueued(&res) {
		fmt.Fprintln(d.stderr, res.Lines[0])
		return exitcode.LandNotEnqueued
	}
	cleanupLandBranch(prep, &res, false, d)
	printLandBranchResult(prep, res, sha, logPath, wait, d)
	if install && res.Kind == landPushed {
		if installOne(ctx, prep.repo, d) {
			return exitcode.LandInstallFailed
		}
	}
	return exitcode.Success
}

// cleanupLandBranch writes resetToRemote's warnings into res, which printLandBranchResult prints.
func cleanupLandBranch(prep landBranchPrep, res *landResult, local bool, d landDeps) {
	remote := upstreamRemote(prep.repo, prep.def, d)
	if prep.dirty && !local {
		_, _ = d.git.run(prep.repo, "fetch", "-q", remote)
	} else if !prep.dirty && res.Route != "direct" {
		resetToRemote(prep.repo, remote+"/"+prep.def, prep.gated, d, res)
	}
	if res.Kind == landPR && res.PRState != "merged" {
		return
	}
	if !prep.wtCreated && hasIgnoredFiles(prep.wt, d) {
		fmt.Fprintf(d.stderr, "worktree %s has ignored files; keeping it\n", prep.wt)
		return
	}
	if _, err := d.git.run(prep.repo, "worktree", "remove", prep.wt); err != nil {
		fmt.Fprintf(d.stderr, "worktree remove refused; keeping %s\n", prep.wt)
		return
	}
	fmt.Fprintf(d.info(), "removed worktree %s\n", prep.wt)
	del := []string{"branch", "-d", prep.branch}
	if prep.dirty && !local {
		if !cherryUpstream(prep.repo, prep.def, prep.branch, d) {
			return
		}
		del[1] = "-D"
	}
	if _, err := d.git.run(prep.repo, del...); err != nil {
		return
	}
	fmt.Fprintf(d.info(), "deleted branch %s\n", prep.branch)
}

// hasIgnoredFiles reports ignored files in dir, which `git worktree remove` would delete as if clean.
func hasIgnoredFiles(dir string, d landDeps) bool {
	out, _ := d.git.run(dir, "ls-files", "--others", "--ignored", "--exclude-standard")
	return strings.TrimSpace(out) != ""
}

func printLandBranchResult(prep landBranchPrep, res landResult, sha, logPath string, wait bool, d landDeps) {
	for _, line := range res.Lines {
		fmt.Fprintln(d.info(), line)
	}
	if res.Kind == landPR && res.PRState != "merged" && res.URL != "" {
		job := landWatchJob{
			Repo:    res.Repo,
			Slug:    res.Slug,
			PR:      res.PRNumber,
			URL:     res.URL,
			Default: res.Default,
		}
		if wait {
			fmt.Fprintln(d.info(), finalWaitLine(res, watchUntilDone(job, d)))
		}
	}
	printLandBranchLine(prep, sha, logPath, res.Route, d)
}

func printLandBranchLine(prep landBranchPrep, sha, logPath, route string, d landDeps) {
	if d.branchJSON != nil {
		*d.branchJSON = landBranchJSON{Repo: prep.repo, Branch: prep.branch, SHA: sha, Log: logPath, Route: route}
		return
	}
	fmt.Fprintf(d.stdout, "%s %s %s %s %s\n", prep.repo, prep.branch, sha, logPath, route)
}

// isLinkedWorktree is true when repo's git dir differs from the common one,
// which only a linked worktree's does.
func isLinkedWorktree(repo string, d landDeps) bool {
	out, err := d.git.run(repo, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return false
	}
	dirs := strings.Split(strings.TrimSpace(out), "\n")
	return len(dirs) == 2 && filepath.Clean(dirs[0]) != filepath.Clean(dirs[1])
}

func defaultBranchChecked(repo string, d landDeps) (string, bool) {
	ref, err := d.git.run(repo, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	if err == nil {
		ref = strings.TrimSpace(ref)
		if ref != "" {
			return strings.TrimPrefix(ref, "refs/remotes/origin/"), true
		}
	}
	if _, err := d.git.run(repo, "show-ref", "--verify", "--quiet", "refs/heads/main"); err == nil {
		return "main", true
	}
	if _, err := d.git.run(repo, "show-ref", "--verify", "--quiet", "refs/heads/master"); err == nil {
		return "master", true
	}
	return "", false
}

func findBranchWorktree(repo, branch string, d landDeps) (string, bool) {
	out, err := d.git.run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false
	}
	want := "refs/heads/" + branch
	wt := ""
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			wt = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			if strings.TrimPrefix(line, "branch ") == want {
				return wt, true
			}
		}
	}
	return "", false
}

func mergeOverlapFiles(repo, branch string, d landDeps) []string {
	out, err := d.git.run(repo, "diff", "--name-only", "-z", "HEAD", branch)
	if err != nil {
		return nil
	}
	changed := map[string]bool{}
	for _, p := range gitx.SplitNUL([]byte(out)) {
		changed[p] = true
	}
	status, err := d.git.run(repo, "status", "--porcelain=v1", "-z", "-uno")
	if err != nil {
		return nil
	}
	var hit []string
	for _, path := range gitx.StatusPaths(status) {
		if changed[path] {
			hit = append(hit, path)
		}
	}
	return hit
}

func cherryUpstream(repo, def, branch string, d landDeps) bool {
	out, err := d.git.run(repo, "cherry", upstreamRemote(repo, def, d)+"/"+def, branch)
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "+") {
			return false
		}
	}
	return true
}

func failedGateNames(log string) string {
	var names []string
	for _, m := range gateFailedRE.FindAllStringSubmatch(log, -1) {
		names = append(names, strings.TrimSpace(m[1]))
	}
	if len(names) == 0 {
		return "?"
	}
	return strings.Join(names, ",")
}

func tail(s string, n int) []string {
	lines := nonEmptyLines(s)
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

func toplevel(r string, d landDeps) (string, error) {
	out, err := d.git.run(r, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(out), err
}

func currentBranch(repo string, d landDeps) (string, error) {
	out, err := d.git.run(repo, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}

// branchOrHEAD names the branch in a warning line, where a failed lookup must not hide the warning.
func branchOrHEAD(repo string, d landDeps) string {
	if cur, err := currentBranch(repo, d); err == nil {
		return cur
	}
	return "HEAD"
}

func aheadBehind(repo string, d landDeps) (ahead, behind int, err error) {
	out, err := d.git.run(repo, "rev-list", "--left-right", "--count", "HEAD...@{u}")
	if err != nil {
		return 0, 0, err
	}
	return gitx.ParseAheadBehind(strings.TrimSpace(out))
}

func mustGit(repo string, d landDeps, args ...string) string {
	out, _ := d.git.run(repo, args...)
	return out
}

func dirtyNames(status string) string {
	return strings.Join(gitx.StatusPaths(status), ", ")
}

// dirtySkipLine is the skip line for a dirty tracked tree; the reason stays first so skipReason still reads it.
func dirtySkipLine(repo string, d landDeps) string {
	const maxNames = 8
	st, _ := d.git.run(repo, "status", "--porcelain=v1", "-z", "-uno")
	names := gitx.StatusPaths(st)
	if len(names) > maxNames {
		names = append(names[:maxNames], fmt.Sprintf("and %d more", len(names)-maxNames))
	}
	return repo + ": skipped (dirty tracked files: " + strings.Join(names, ", ") + ")"
}

func dirtyTracked(repo string, d landDeps) bool {
	out, _ := d.git.run(repo, "status", "--porcelain=v1", "-uno")
	return strings.TrimSpace(out) != ""
}

// pushDirect pushes exactly what was verified: a rejected push reports that
// origin moved rather than rebasing onto it and pushing an unverified commit.
// done is false only when a protected branch sends the caller to the PR route.
func pushDirect(repo, pushDir, remote string, d landDeps, opts landPushOpts, res landResult) (landResult, bool) {
	var out string
	var err error
	pushed := opts.pushRef
	if pushed != "" {
		out, err = d.git.run(pushDir, "push", "--recurse-submodules=check", remote, pushed+":refs/heads/"+opts.pushTo)
	} else {
		out, err = pushOnce(pushDir, remote, opts.pushTo, d)
		pushed = strings.TrimSpace(mustGit(pushDir, d, "rev-parse", "HEAD"))
	}
	if err != nil {
		dropSettingsOnHint(d.gh, opts.slug, out)
	}
	switch {
	case err == nil || remoteHolds(pushDir, d, remote, pushed, opts.pushTo):
		res.Kind = landPushed
		res.Route = "direct"
		res.SHA = pushed
		res.Lines = []string{summaryPushed(repo, pushed)}
	case pushRetryable(out):
		msg := repo + ": error: origin moved; nothing pushed, re-run"
		if opts.afterGate {
			msg = repo + ": error: origin moved during the gate; nothing pushed, re-run"
		}
		res.Kind = landRed
		res.Moved = true
		res.Lines = []string{msg}
	case !pushRejectedProtected(out):
		res.Kind = landRed
		res.Lines = []string{summaryRed(repo, out)}
	default:
		return res, false
	}
	return res, true
}

// remoteHolds reports whether origin's branch already equals the pushed
// commit: a push can fail updating only the local tracking ref (a concurrent
// fetch holding its lock) after the remote moved.
func remoteHolds(dir string, d landDeps, remote, pushed, to string) bool {
	if to == "" {
		return false
	}
	want, err := d.git.run(dir, "rev-parse", pushed+"^{commit}")
	if err != nil {
		return false
	}
	out, err := d.git.run(dir, "ls-remote", remote, "refs/heads/"+to)
	f := strings.Fields(out)
	return err == nil && len(f) > 0 && f[0] == strings.TrimSpace(want)
}

func pushOnce(repo, remote, to string, d landDeps) (string, error) {
	if to == "" {
		return d.git.run(repo, "push")
	}
	return d.git.run(repo, "push", "--recurse-submodules=check", remote, "HEAD:"+to)
}

// upstreamRemote is the remote the default branch tracks, so the rules, the push and
// the ls-remote check all talk to the one remote a bare `git push` would use.
func upstreamRemote(repo, def string, d landDeps) string {
	r := strings.TrimSpace(mustGit(repo, d, "config", "--get", "branch."+def+".remote"))
	if r == "" || r == "." {
		return "origin"
	}
	return r
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
	if _, rest, ok := strings.Cut(slug, "://"); ok {
		// host (with any userinfo and port) up to the first slash.
		_, path, ok := strings.Cut(rest, "/")
		if !ok {
			return ""
		}
		return path
	}
	// scp-like git@host:owner/repo
	if at := strings.Index(slug, "@"); at >= 0 {
		if colon := strings.Index(slug[at:], ":"); colon >= 0 {
			return slug[at+colon+1:]
		}
	}
	return ""
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

// lookupRules reads the default branch's rule types and remembers them per repo. A failed lookup
// (offline, unauthenticated) falls back to the remembered rules, so a PR-only repo is not pushed
// to directly; with none remembered it returns nil and says so.
func lookupRules(slug, def string, d landDeps) []string {
	file := filepath.Join(d.cacheHome(), "land-rules", strings.ReplaceAll(slug, "/", "_")+"-"+strings.ReplaceAll(def, "/", "_"))
	rules, err := d.gh.branchRuleTypes(slug, def)
	if err == nil {
		if os.MkdirAll(filepath.Dir(file), 0o750) == nil {
			_ = os.WriteFile(file, []byte(strings.Join(rules, "\n")+"\n"), 0o600)
		}
		return rules
	}
	body, rerr := os.ReadFile(file) //nolint:gosec // path built from the cache dir and the remote slug
	st, serr := os.Stat(file)
	if rerr != nil || serr != nil {
		fmt.Fprintf(d.stderr, "%s: warning: branch-rules lookup failed (%v) and none cached; pushing direct\n", slug, err)
		return nil
	}
	fmt.Fprintf(d.stderr, "%s: warning: branch-rules lookup failed (%v); using rules cached %s ago\n", slug, err, d.clock.Now().Sub(st.ModTime()).Round(time.Second))
	return strings.Fields(string(body))
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

// prStateUnknown is PRState when the PR lookup failed: neither queued nor
// unqueued is known, so it must not read as the latter.
const prStateUnknown = "unknown"

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

func summaryPushed(repo, sha string) string { return repo + ": pushed " + sha }

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
	return repo + ": red (" + redDetail(out) + ")"
}

const redDetailCap = 300

var (
	ansiCSI        = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	gateFailedRE   = regexp.MustCompile(`(?m)^gate: failed: (.+?) -> (\S+)\s*$`)
	gateFailRE     = regexp.MustCompile(`(?m)^gate: FAIL\b(.*)$`)
	lefthookFailRE = regexp.MustCompile(`^✗\s+(\S+)`)
)

func redDetail(out string) string {
	// Lefthook's closing box never names the failure; the ✗ row and gate verdict do.
	out = ansiCSI.ReplaceAllString(out, "")
	if cmds, logs := gateVerdicts(out); len(cmds) > 0 {
		parts := append([]string{}, cmds...)
		for _, p := range logs {
			parts = append(parts, lastLogLines(p, 3)...)
		}
		return capJoin(parts, redDetailCap)
	}
	lines := meaningfulLines(out)
	if names := lefthookFailed(lines); len(names) > 0 {
		parts := append([]string{}, names...)
		parts = append(parts, errorishLines(lines)...)
		return capJoin(parts, redDetailCap)
	}
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return capJoin(lines, redDetailCap)
}

func gateVerdicts(out string) (cmds, logs []string) {
	for _, m := range gateFailedRE.FindAllStringSubmatch(out, -1) {
		cmds = append(cmds, strings.TrimSpace(m[1]))
		logs = append(logs, m[2])
	}
	for _, m := range gateFailRE.FindAllStringSubmatch(out, -1) {
		rest := strings.TrimSpace(m[1])
		if rest == "" {
			continue
		}
		cmds = append(cmds, stripTrailingTiming(rest))
	}
	return cmds, logs
}

func lastLogLines(path string, n int) []string {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil
	}
	return tail(string(b), n)
}

func meaningfulLines(out string) []string {
	var lines []string
	for _, line := range nonEmptyLines(out) {
		line = strings.TrimSpace(line)
		if line == "" || isDecorationLine(line) {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func isDecorationLine(line string) bool {
	if strings.HasPrefix(strings.ToLower(line), "summary:") {
		return true
	}
	stripped := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || (r >= 0x2500 && r <= 0x257F) {
			return -1
		}
		return r
	}, line)
	return stripped == ""
}

func lefthookFailed(lines []string) []string {
	var names []string
	for _, line := range lines {
		m := lefthookFailRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		names = append(names, m[1])
	}
	return names
}

func errorishLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if lefthookFailRE.MatchString(line) {
			continue
		}
		if isErrorish(line) {
			out = append(out, line)
		}
	}
	return out
}

func isErrorish(line string) bool {
	if strings.Contains(line, "×") {
		return true
	}
	lower := strings.ToLower(line)
	return strings.Contains(lower, "error") || strings.Contains(lower, "fail")
}

func stripTrailingTiming(s string) string {
	before, after, ok := strings.CutLast(s, " (")
	if ok && strings.HasSuffix(after, ")") {
		return strings.TrimSpace(before)
	}
	return s
}

func capJoin(parts []string, limit int) string {
	s := strings.Join(parts, " | ")
	n := 0
	for i := range s {
		if n == limit {
			return s[:i]
		}
		n++
	}
	return s
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

func watchUntilDone(job landWatchJob, d landDeps) landWatchResult {
	repo := job.Slug
	if repo == "" {
		repo = filepath.Base(job.Repo)
	}
	res := landWatchResult{
		Repo: repo,
		PR:   job.PR,
		URL:  job.URL,
		CI:   "none",
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
	fastForwardDefaultAfterMerge(job, d)
	return waitForCI(res, owner, name, ok, job.Default, info.MergeSHA, d)
}

func fastForwardDefaultAfterMerge(job landWatchJob, d landDeps) {
	if job.Repo == "" || job.Default == "" {
		return
	}
	remote := upstreamRemote(job.Repo, job.Default, d)
	if out, err := d.git.run(job.Repo, "fetch", "-q", remote); err != nil {
		fmt.Fprintf(d.stderr, "rgit land: %s: left %s (%s)\n", job.Repo, job.Default, gitErrDetail(out, err))
		return
	}
	cur, err := currentBranch(job.Repo, d)
	if err != nil {
		fmt.Fprintf(d.stderr, "rgit land: %s: left %s (cannot determine current branch)\n", job.Repo, job.Default)
		return
	}
	if cur != job.Default {
		fmt.Fprintf(d.stderr, "rgit land: %s: left %s (on %s, not %s)\n", job.Repo, job.Default, cur, job.Default)
		return
	}
	if dirtyTracked(job.Repo, d) {
		fmt.Fprintf(d.stderr, "rgit land: %s: left %s (dirty tracked files)\n", job.Repo, job.Default)
		return
	}
	if out, err := d.git.run(job.Repo, "merge", "--ff-only", remote+"/"+job.Default); err != nil {
		fmt.Fprintf(d.stderr, "rgit land: %s: left %s (%s)\n", job.Repo, job.Default, gitErrDetail(out, err))
	}
}

func gitErrDetail(out string, err error) string {
	if s := strings.TrimSpace(out); s != "" {
		return lastNonEmptyLine(s)
	}
	if err != nil {
		return err.Error()
	}
	return "unknown error"
}

func pollPR(owner, name string, ok bool, job landWatchJob, d landDeps) (prInfo, string) {
	setLandStep(d.step, "wait")
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
		if d.cancelled() {
			return info, "cancelled"
		}
		d.clock.Sleep(landPollInterval)
	}
}

func waitForCI(res landWatchResult, owner, name string, ok bool, branch, sha string, d landDeps) landWatchResult {
	setLandStep(d.step, "wait")
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
		if d.cancelled() {
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

func finalWaitLine(res landResult, fin landWatchResult) string {
	if res.Kind == landPR {
		return fmt.Sprintf("%s: PR %s (%s); CI %s", res.Repo, res.URL, fin.State, fin.CI)
	}
	if len(res.Lines) > 0 {
		return res.Lines[len(res.Lines)-1]
	}
	return res.Repo
}
