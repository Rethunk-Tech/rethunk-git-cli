package app

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// settingsProbeGH counts the calls that reach the inner gitHub; only the cached answers are implemented.
type settingsProbeGH struct {
	gitHub
	rules, squash, merge int
	err                  error
}

func (p *settingsProbeGH) branchRuleTypes(slug, branch string) ([]string, error) {
	p.rules++
	if p.err != nil {
		return nil, p.err
	}
	return []string{"pull_request"}, nil
}

func (p *settingsProbeGH) squashMergeAllowed(slug string) (bool, error) {
	p.squash++
	return p.err == nil, p.err
}

func (p *settingsProbeGH) mergeCommitAllowed(slug string) (bool, error) {
	p.merge++
	return p.err == nil, p.err
}

// newSettingsTestCache keeps the cache file under the test's temp dir and returns a clock to advance.
func newSettingsTestCache(t *testing.T, inner gitHub) (*ghSettingsCache, func(time.Duration)) {
	t.Helper()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	c := newGHSettingsCache(inner, filepath.Join(t.TempDir(), "rgit", "github-settings.json"))
	c.now = func() time.Time { return now }
	return c, func(d time.Duration) { now = now.Add(d) }
}

func TestGHSettingsCacheSecondCallWithinTTLSkipsInner(t *testing.T) {
	inner := &settingsProbeGH{}
	c, advance := newSettingsTestCache(t, inner)
	for range 2 {
		if _, err := c.branchRuleTypes("o/r", "main"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.squashMergeAllowed("o/r"); err != nil {
			t.Fatal(err)
		}
	}
	advance(settingsTTL - time.Minute)
	if _, err := c.branchRuleTypes("o/r", "main"); err != nil {
		t.Fatal(err)
	}
	if inner.rules != 1 || inner.squash != 1 {
		t.Fatalf("inner reached rules=%d squash=%d within TTL, want 1 and 1", inner.rules, inner.squash)
	}
}

func TestGHSettingsCacheExpiredEntryRereads(t *testing.T) {
	inner := &settingsProbeGH{}
	c, advance := newSettingsTestCache(t, inner)
	if _, err := c.mergeCommitAllowed("o/r"); err != nil {
		t.Fatal(err)
	}
	advance(settingsTTL + time.Second)
	if _, err := c.mergeCommitAllowed("o/r"); err != nil {
		t.Fatal(err)
	}
	if inner.merge != 2 {
		t.Fatalf("inner reached %d times after TTL, want 2", inner.merge)
	}
}

func TestGHSettingsCacheSettingsHintDropsSlug(t *testing.T) {
	inner := &settingsProbeGH{}
	c, _ := newSettingsTestCache(t, inner)
	if _, err := c.branchRuleTypes("o/r", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.squashMergeAllowed("o/r"); err != nil {
		t.Fatal(err)
	}
	dropSettingsOnHint(c, "o/r", "remote: error: GH013: Repository rule violations found")
	if _, err := c.branchRuleTypes("o/r", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.squashMergeAllowed("o/r"); err != nil {
		t.Fatal(err)
	}
	if inner.rules != 2 || inner.squash != 2 {
		t.Fatalf("after settings hint inner reached rules=%d squash=%d, want 2 and 2", inner.rules, inner.squash)
	}
}

func TestGHSettingsCacheNonHintFailureKeepsEntry(t *testing.T) {
	inner := &settingsProbeGH{}
	c, _ := newSettingsTestCache(t, inner)
	if _, err := c.branchRuleTypes("o/r", "main"); err != nil {
		t.Fatal(err)
	}
	dropSettingsOnHint(c, "o/r", "Pull request is not mergeable: the head branch is behind")
	if _, err := c.branchRuleTypes("o/r", "main"); err != nil {
		t.Fatal(err)
	}
	if inner.rules != 1 {
		t.Fatalf("non-hint failure re-read rules %d times, want 1", inner.rules)
	}
}

func TestGHSettingsCacheDoesNotCacheErrors(t *testing.T) {
	inner := &settingsProbeGH{err: errors.New("gh: offline")}
	c, _ := newSettingsTestCache(t, inner)
	if _, err := c.branchRuleTypes("o/r", "main"); err == nil {
		t.Fatal("want the inner error")
	}
	inner.err = nil
	types, err := c.branchRuleTypes("o/r", "main")
	if err != nil || len(types) != 1 {
		t.Fatalf("second call = %v, %v; want the inner answer", types, err)
	}
	if inner.rules != 2 {
		t.Fatalf("inner reached %d times, want 2 (error must not be cached)", inner.rules)
	}
}
