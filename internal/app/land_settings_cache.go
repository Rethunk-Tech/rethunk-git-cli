package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// settingsTTL is how long a GitHub answer that rarely changes (branch rules, allowed merge methods) is reused.
const settingsTTL = 24 * time.Hour

// githubSettingsPath is where land keeps those answers; empty when there is no user cache dir, which turns caching off.
func githubSettingsPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "rgit", "github-settings.json")
}

// settingsHintMarkers are refusal texts that say a repo's branch rules or merge methods changed.
var settingsHintMarkers = []string{
	"gh006", "gh013", "repository rule violations", "protected branch", "merge method",
	"squash merges are not allowed", "merge commits are not allowed",
}

func settingsHint(out string) bool {
	lower := strings.ToLower(out)
	for _, m := range settingsHintMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// dropSettingsOnHint forgets slug's cached settings when a refused push, PR or merge says they changed.
func dropSettingsOnHint(gh gitHub, slug, out string) {
	c, ok := gh.(*ghSettingsCache)
	if !ok || slug == "" || !settingsHint(out) {
		return
	}
	c.forget(slug)
}

// ghSettingsCache is a gitHub decorator that keeps branch rules and merge-method answers on disk for
// settingsTTL. Only successful answers are stored. An empty path turns the cache off.
type ghSettingsCache struct {
	inner gitHub
	path  string
	now   func() time.Time
}

func newGHSettingsCache(inner gitHub, path string) *ghSettingsCache {
	return &ghSettingsCache{inner: inner, path: path, now: time.Now}
}

type ghSettingsFile struct {
	Repos map[string]*ghRepoSettings `json:"repos"`
}

type ghRepoSettings struct {
	Rules       map[string]ghRulesEntry `json:"rules,omitempty"`
	SquashMerge *ghFlagEntry            `json:"squashMerge,omitempty"`
	MergeCommit *ghFlagEntry            `json:"mergeCommit,omitempty"`
}

type ghRulesEntry struct {
	Read  time.Time `json:"read"`
	Types []string  `json:"types"`
}

type ghFlagEntry struct {
	Read    time.Time `json:"read"`
	Allowed bool      `json:"allowed"`
}

// load reads the cache file; a missing or unreadable file is empty.
func (c *ghSettingsCache) load() ghSettingsFile {
	var f ghSettingsFile
	if c.path == "" {
		return f
	}
	b, err := os.ReadFile(c.path)
	if err != nil || json.Unmarshal(b, &f) != nil {
		return ghSettingsFile{}
	}
	return f
}

// save writes f through a temp file and a rename, so a concurrent land never reads a partial file.
// A failed write only costs a later re-read.
func (c *ghSettingsCache) save(f ghSettingsFile) {
	if c.path == "" {
		return
	}
	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".github-settings-*.json")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), c.path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

func (c *ghSettingsCache) fresh(read time.Time) bool {
	age := c.now().Sub(read)
	return age >= 0 && age < settingsTTL
}

// update applies mutate to slug's settings, reading the file fresh so other keys survive.
func (c *ghSettingsCache) update(slug string, mutate func(*ghRepoSettings)) {
	f := c.load()
	if f.Repos == nil {
		f.Repos = map[string]*ghRepoSettings{}
	}
	rs := f.Repos[slug]
	if rs == nil {
		rs = &ghRepoSettings{}
		f.Repos[slug] = rs
	}
	mutate(rs)
	c.save(f)
}

// forget drops slug's cached answers so the next land reads them again.
func (c *ghSettingsCache) forget(slug string) {
	f := c.load()
	if _, ok := f.Repos[slug]; !ok {
		return
	}
	delete(f.Repos, slug)
	c.save(f)
}

func (c *ghSettingsCache) branchRuleTypes(slug, branch string) ([]string, error) {
	if rs := c.load().Repos[slug]; rs != nil {
		if e, ok := rs.Rules[branch]; ok && c.fresh(e.Read) {
			return e.Types, nil
		}
	}
	types, err := c.inner.branchRuleTypes(slug, branch)
	if err != nil {
		return nil, err
	}
	c.update(slug, func(rs *ghRepoSettings) {
		if rs.Rules == nil {
			rs.Rules = map[string]ghRulesEntry{}
		}
		rs.Rules[branch] = ghRulesEntry{Read: c.now(), Types: types}
	})
	return types, nil
}

func (c *ghSettingsCache) squashMergeAllowed(slug string) (bool, error) {
	if rs := c.load().Repos[slug]; rs != nil && rs.SquashMerge != nil && c.fresh(rs.SquashMerge.Read) {
		return rs.SquashMerge.Allowed, nil
	}
	ok, err := c.inner.squashMergeAllowed(slug)
	if err != nil {
		return false, err
	}
	c.update(slug, func(rs *ghRepoSettings) {
		rs.SquashMerge = &ghFlagEntry{Read: c.now(), Allowed: ok}
	})
	return ok, nil
}

func (c *ghSettingsCache) mergeCommitAllowed(slug string) (bool, error) {
	if rs := c.load().Repos[slug]; rs != nil && rs.MergeCommit != nil && c.fresh(rs.MergeCommit.Read) {
		return rs.MergeCommit.Allowed, nil
	}
	ok, err := c.inner.mergeCommitAllowed(slug)
	if err != nil {
		return false, err
	}
	c.update(slug, func(rs *ghRepoSettings) {
		rs.MergeCommit = &ghFlagEntry{Read: c.now(), Allowed: ok}
	})
	return ok, nil
}

// The rest is live state and passes straight through.

func (c *ghSettingsCache) createPR(dir, head, body string) (string, error) {
	return c.inner.createPR(dir, head, body)
}

func (c *ghSettingsCache) mergePR(dir, head string, auto bool, strategy string) error {
	return c.inner.mergePR(dir, head, auto, strategy)
}

func (c *ghSettingsCache) prNumber(dir, head string) (int, error) {
	return c.inner.prNumber(dir, head)
}

func (c *ghSettingsCache) pullRequest(owner, repo string, number int) (prInfo, error) {
	return c.inner.pullRequest(owner, repo, number)
}

func (c *ghSettingsCache) latestWorkflowRun(owner, repo, branch, sha string) (workflowRun, error) {
	return c.inner.latestWorkflowRun(owner, repo, branch, sha)
}
