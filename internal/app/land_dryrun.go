package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/exitcode"
)

type landDryRunRepo struct {
	Path   string   `json:"path"`
	Order  int      `json:"order"`
	Status string   `json:"status"`
	Detail string   `json:"detail,omitempty"`
	Route  string   `json:"route,omitempty"`
	Gate   string   `json:"gate,omitempty"`
	Ahead  []string `json:"ahead,omitempty"`
}

// landDryRun prints, in run order, what a land would do to each repo, and changes nothing: no catch-up, gate or push.
func landDryRun(repos []string, asJSON bool, d landDeps) exitcode.Code {
	ordered := orderRepos(repos)
	if d.sha == "" {
		ordered = orderByPins(ordered, pinDeps(ordered, d))
	}
	plan := make([]landDryRunRepo, 0, len(ordered))
	for i, r := range ordered {
		p := dryRunRepo(r, d)
		p.Order = i + 1
		plan = append(plan, p)
	}
	if asJSON {
		_ = json.NewEncoder(d.stdout).Encode(struct {
			Repos []landDryRunRepo `json:"repos"`
		}{plan})
		return exitcode.Success
	}
	for _, p := range plan {
		if p.Status != "would-land" {
			fmt.Fprintf(d.stdout, "%d. %s: skipped (%s)\n", p.Order, p.Path, p.Detail)
			continue
		}
		fmt.Fprintf(d.stdout, "%d. %s: would %s (gate: %s; %d ahead)\n", p.Order, p.Path, p.Route, p.Gate, len(p.Ahead))
		for _, c := range p.Ahead {
			fmt.Fprintf(d.stdout, "   %s\n", c)
		}
	}
	return exitcode.Success
}

func dryRunRepo(r string, d landDeps) landDryRunRepo {
	p := landDryRunRepo{Path: r}
	skip := func(why string) landDryRunRepo {
		p.Status, p.Detail = "skipped", why
		return p
	}
	top, err := d.git.run(r, "rev-parse", "--show-toplevel")
	if err != nil {
		return skip("not a git repo")
	}
	repo := strings.TrimSpace(top)
	p.Path = repo
	def, ok := defaultBranchChecked(repo, d)
	if !ok {
		return skip("cannot determine default branch")
	}
	if dirtyTracked(repo, d) {
		return skip("dirty tracked files")
	}
	if cur := strings.TrimSpace(mustGit(repo, d, "rev-parse", "--abbrev-ref", "HEAD")); cur != def {
		return skip(fmt.Sprintf("on %s, not %s", cur, def))
	}
	log, err := d.git.run(repo, "log", "--format=%h %s", "@{u}..HEAD")
	if err != nil {
		return skip("no upstream")
	}
	p.Ahead = strings.Split(strings.TrimSpace(log), "\n")
	if p.Ahead[0] == "" {
		return skip("not ahead")
	}
	remote := upstreamRemote(repo, def, d)
	var rules []string
	if slug := slugFromOrigin(strings.TrimSpace(mustGit(repo, d, "config", "--get", "remote."+remote+".url"))); slug != "" {
		rules = lookupRules(slug, def, d)
	}
	needPR, mergeQueue := rulesNeedPR(rules)
	switch {
	case mergeQueue:
		p.Route = "queue"
	case needPR:
		p.Route = "PR"
	default:
		p.Route = "push"
	}
	p.Gate = "would run"
	switch {
	case d.noGate:
		p.Gate = "skipped (--no-gate)"
	case d.passed != nil && d.passed(repo, mustGit(repo, d, "rev-parse", "HEAD")):
		p.Gate = "would reuse pass"
	}
	p.Status = "would-land"
	return p
}
