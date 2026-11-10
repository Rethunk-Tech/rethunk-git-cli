package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// countingGit puts a shim named git on PATH that appends one byte to a count
// file per invocation and then execs the real git. PATH is replaced rather
// than prepended so nothing else on it can satisfy a lookup, and the shim
// carries git's absolute path because it no longer resolves through PATH.
func countingGit(t *testing.T) (bin, countFile string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh not on PATH: %v", err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	bin = t.TempDir()
	countFile = filepath.Join(bin, "count")
	script := "#!/bin/sh\nprintf x >> '" + countFile + "'\nexec '" + gitPath + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil { //nolint:gosec // executable git shim is the behavior under test
		t.Fatal(err)
	}
	return bin, countFile
}

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
// by New must come from the template in a single `git init`, not from the
// four `git config` invocations it used to take to write the same settings
// afterwards. Those four were the largest single block of subprocesses in
// the unit lane, so a regression here is a slow suite, not a failing one.
func TestNewCostsOneGitProcess(t *testing.T) {
	bin, countFile := countingGit(t)
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
