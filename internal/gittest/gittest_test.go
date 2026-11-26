package gittest

import (
	"os"
	"path/filepath"
	"testing"
)

// readCount returns how many times the shim recorded an invocation: the
// shim appends one byte per call, so the file's size is the count.
func readCount(t *testing.T, countFile string) int64 {
	t.Helper()
	info, err := os.Stat(countFile)
	if err != nil {
		t.Fatalf("stat git invocation count: %v", err)
	}
	return info.Size()
}

// TestNewCostsOneGitProcess pins the fixture's own cost: a repository built
// by New must come from the template in a single `git init`, not from four
// `git config` invocations afterwards. Those four would be the largest
// single block of subprocesses in the unit lane, so a regression here is a
// slow suite, not a failing one.
func TestNewCostsOneGitProcess(t *testing.T) {
	bin, countFile := CountingGit(t, "")
	t.Setenv("PATH", bin)

	New(t.Context(), t)

	if got := readCount(t, countFile); got != 1 {
		t.Errorf("New ran %d git processes; want 1 (git init against the fixture template)", got)
	}
}

// TestNewCarriesTemplateSettings is the other half: one process is only
// correct if it still leaves the repository with an identity to commit under
// and auto-maintenance off, which is what the four config calls were for.
func TestNewCarriesTemplateSettings(t *testing.T) {
	t.Parallel()
	dir, _ := New(t.Context(), t)

	for _, c := range []struct{ key, want string }{
		{"user.email", "rgit-test@example.com"},
		{"user.name", "rgit Test"},
		{"gc.auto", "0"},
		{"maintenance.auto", "false"},
	} {
		if got := Git(t.Context(), t, dir, "config", c.key); got != c.want+"\n" {
			t.Errorf("%s = %q; want %q", c.key, got, c.want)
		}
	}

	// A template replaces the hooks git would otherwise copy in, so the
	// directory InstallHook writes real hooks into has to exist.
	InstallHook(t.Context(), t, dir, "pre-commit", "#!/bin/sh\nexit 0\n")
}

// TestTemplateLeavesOneFixedEntry pins the leak fix: however many times a
// test binary builds the template, TMPDIR holds the one per-user directory.
func TestTemplateLeavesOneFixedEntry(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for range 3 {
		if _, err := templateIn(); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("TMPDIR holds %d entries after template builds; want 1 fixed one", len(entries))
	}
	left, err := os.ReadDir(filepath.Join(tmp, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 { // config, hooks
		t.Errorf("template dir holds %d entries; want config and hooks only", len(left))
	}
}
