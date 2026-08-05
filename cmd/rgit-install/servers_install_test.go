// Install/update coverage: the manager-selection table, command
// construction per ecosystem, PATH-reachability detection, and warning
// formatting -- CONTRIBUTING.md's "test the pure logic" boundary.
// manageServers itself takes an injected lookPath (servers_install.go), so
// its wiring and skip branches are tested here too with dryRun:true, which
// never runs a real command. runInstallJob's own failure-detection contract
// (FAILED printed and ok=false on a nonzero exit) is pinned with real,
// deterministic exec.Command targets ("true", a binary name
// guaranteed absent from PATH) -- only a real go/npm/cargo/bun install
// succeeding or failing for real stays untested, exercised by hand instead.
package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	qt "github.com/go-quicktest/qt"
)

func TestBuildInstallJobs(t *testing.T) {
	t.Parallel()

	catalog := []serverEntry{
		{bin: "gopls", manager: managerGo, pkg: "golang.org/x/tools/gopls@latest"},
		// Two entries sharing a package must collapse into one job.
		{bin: "vscode-json-language-server", manager: managerNPM, pkg: "vscode-langservers-extracted"},
		{bin: "vscode-css-language-server", manager: managerNPM, pkg: "vscode-langservers-extracted"},
		{bin: "taplo", manager: managerCargo, pkg: "taplo-cli", extraArgs: []string{"--locked", "--features", "lsp"}},
		// managerNone must never produce a job.
		{bin: "marksman", manager: managerNone, unmanagedHint: "download a release binary"},
	}

	jobs := buildInstallJobs(catalog)
	qt.Assert(t, qt.HasLen(jobs, 3))

	qt.Assert(t, qt.Equals(jobs[0].manager, managerGo))
	qt.Assert(t, qt.DeepEquals(jobs[0].provides, []string{"gopls"}))

	qt.Assert(t, qt.Equals(jobs[1].manager, managerNPM))
	qt.Assert(t, qt.DeepEquals(jobs[1].provides, []string{"vscode-json-language-server", "vscode-css-language-server"}))

	qt.Assert(t, qt.Equals(jobs[2].manager, managerCargo))
	qt.Assert(t, qt.DeepEquals(jobs[2].extraArgs, []string{"--locked", "--features", "lsp"}))
}

func TestSelectNPMManager(t *testing.T) {
	t.Parallel()

	t.Run("prefers bun when both are on PATH", func(t *testing.T) {
		t.Parallel()
		cmd, ok := selectNPMManager(fakeLookPath(map[string]string{
			"bun": "/usr/bin/bun", "npm": "/usr/bin/npm",
		}))
		qt.Assert(t, qt.IsTrue(ok))
		qt.Assert(t, qt.Equals(cmd, "bun"))
	})

	t.Run("falls back to npm without bun", func(t *testing.T) {
		t.Parallel()
		cmd, ok := selectNPMManager(fakeLookPath(map[string]string{"npm": "/usr/bin/npm"}))
		qt.Assert(t, qt.IsTrue(ok))
		qt.Assert(t, qt.Equals(cmd, "npm"))
	})

	t.Run("neither on PATH", func(t *testing.T) {
		t.Parallel()
		_, ok := selectNPMManager(fakeLookPath(nil))
		qt.Assert(t, qt.IsFalse(ok))
	})
}

func TestInstallCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		job      installJob
		useBun   bool
		wantName string
		wantArgs []string
	}{
		{
			name:     "go install carries the @latest already in pkg",
			job:      installJob{manager: managerGo, pkg: "golang.org/x/tools/gopls@latest"},
			wantName: "go",
			wantArgs: []string{"install", "golang.org/x/tools/gopls@latest"},
		},
		{
			name:     "npm without bun",
			job:      installJob{manager: managerNPM, pkg: "pyright"},
			useBun:   false,
			wantName: "npm",
			wantArgs: []string{"install", "-g", "pyright"},
		},
		{
			name:     "npm prefers bun add -g when bun is selected",
			job:      installJob{manager: managerNPM, pkg: "pyright"},
			useBun:   true,
			wantName: "bun",
			wantArgs: []string{"add", "-g", "pyright"},
		},
		{
			// taplo built via `cargo install taplo-cli` alone has no LSP
			// support at all -- extraArgs must reach the invocation.
			name:     "cargo carries extraArgs",
			job:      installJob{manager: managerCargo, pkg: "taplo-cli", extraArgs: []string{"--locked", "--features", "lsp"}},
			wantName: "cargo",
			wantArgs: []string{"install", "taplo-cli", "--locked", "--features", "lsp"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			name, args := installCommand(tt.job, tt.useBun)
			qt.Assert(t, qt.Equals(name, tt.wantName))
			qt.Assert(t, qt.DeepEquals(args, tt.wantArgs))
		})
	}
}

func TestDirOnPATH(t *testing.T) {
	t.Parallel()

	path := "/usr/local/bin:/home/x/.local/bin:/usr/bin"
	qt.Assert(t, qt.IsTrue(dirOnPATH("/home/x/.local/bin", path)))
	qt.Assert(t, qt.IsTrue(dirOnPATH("/home/x/.local/bin/", path))) // trailing slash still matches, cleaned

	// cargo can write taplo to ~/.cargo/bin while that directory is absent
	// from PATH; dirOnPATH is what catches it.
	qt.Assert(t, qt.IsFalse(dirOnPATH("/home/x/.cargo/bin", path)))
}

// TestBinDirWarning covers the three-way decision manageServers' loop makes
// after a job's install succeeds -- including the err != nil case (m23 in
// the 2026-07-29 audit) that previously had no signal at all, silently
// skipping the PATH check on a managerBinDir failure.
func TestBinDirWarning(t *testing.T) {
	t.Parallel()

	t.Run("managerBinDir failed: warn PATH could not be verified", func(t *testing.T) {
		t.Parallel()
		got := binDirWarning("taplo", "", fmt.Errorf("boom"), "/usr/bin")
		qt.Assert(t, qt.StringContains(got, "taplo"))
		qt.Assert(t, qt.StringContains(got, "could not verify"))
		qt.Assert(t, qt.StringContains(got, "boom"))
	})

	t.Run("dir known and on PATH: nothing to print", func(t *testing.T) {
		t.Parallel()
		got := binDirWarning("gopls", "/usr/local/bin", nil, "/usr/local/bin:/usr/bin")
		qt.Assert(t, qt.Equals(got, ""))
	})

	t.Run("dir known and off PATH: formatPathWarning's own message", func(t *testing.T) {
		t.Parallel()
		got := binDirWarning("taplo", "/home/x/.cargo/bin", nil, "/usr/bin")
		qt.Assert(t, qt.Equals(got, formatPathWarning("taplo", "/home/x/.cargo/bin")))
	})
}

func TestFormatPathWarning(t *testing.T) {
	t.Parallel()
	got := formatPathWarning("taplo", "/home/x/.cargo/bin")
	qt.Assert(t, qt.Equals(got,
		"WARNING: taplo installed to /home/x/.cargo/bin, which is not on PATH -- "+
			"the binary exists but nothing that shells out (rgit included) can reach it. "+
			"Add /home/x/.cargo/bin to PATH."))
}

// Not t.Parallel(): t.Setenv forbids it, and forbids it for the whole
// ancestor chain too (CONTRIBUTING.md § Tests already draws this line
// around internal/app's directory-changing cases).
func TestManagerBinDirCargo(t *testing.T) {
	t.Run("CARGO_HOME set", func(t *testing.T) {
		t.Setenv("CARGO_HOME", "/opt/cargo")
		got, err := managerBinDir(managerCargo, false)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(got, "/opt/cargo/bin"))
	})

	t.Run("CARGO_HOME unset falls back to ~/.cargo/bin", func(t *testing.T) {
		t.Setenv("CARGO_HOME", "")
		got, err := managerBinDir(managerCargo, false)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Satisfies(got, func(s string) bool { return strings.HasSuffix(s, "/.cargo/bin") }))
	})
}

func TestManageServersDryRun(t *testing.T) {
	t.Parallel()

	t.Run("nothing on PATH", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		ok := manageServers(true, &buf, fakeLookPath(nil))
		out := buf.String()

		qt.Assert(t, qt.StringContains(out, "Language servers:"))
		qt.Assert(t, qt.StringContains(out, "not found"))
		qt.Assert(t, qt.StringContains(out, "skip"))
		qt.Assert(t, qt.StringContains(out, "neither bun nor npm on PATH"))
		qt.Assert(t, qt.StringContains(out, "cargo not on PATH"))
		// dryRun:true never reaches cmd.Run() for any job -- a "FAILED"
		// line here would mean the dry-run guard stopped guarding.
		qt.Assert(t, qt.Not(qt.StringContains(out, "FAILED")))
		// A skip is not a failure -- there was nothing rgit could have run.
		qt.Assert(t, qt.IsTrue(ok))
	})

	t.Run("every manager on PATH", func(t *testing.T) {
		t.Parallel()
		lookPath := fakeLookPath(map[string]string{
			"gopls": "/x/gopls", "vtsls": "/x/vtsls", "pyright-langserver": "/x/pyright-langserver",
			"bash-language-server": "/x/bls", "yaml-language-server": "/x/yls",
			"vscode-json-language-server": "/x/json", "vscode-css-language-server": "/x/css",
			"marksman": "/x/marksman", "taplo": "/x/taplo",
			"bun": "/x/bun", "npm": "/x/npm", "cargo": "/x/cargo",
		})
		var buf bytes.Buffer
		ok := manageServers(true, &buf, lookPath)
		out := buf.String()

		qt.Assert(t, qt.StringContains(out, "found at /x/gopls"))
		qt.Assert(t, qt.StringContains(out, "go install golang.org/x/tools/gopls@v0.23.0"))
		// bun is preferred over npm when both are on PATH (selectNPMManager).
		qt.Assert(t, qt.StringContains(out, "bun add -g"))
		qt.Assert(t, qt.Not(qt.StringContains(out, "npm install")))
		qt.Assert(t, qt.StringContains(out, "cargo install taplo-cli --locked --features lsp"))
		// Nothing is skipped once every manager answers.
		qt.Assert(t, qt.Not(qt.StringContains(out, "skip")))
		qt.Assert(t, qt.Not(qt.StringContains(out, "FAILED")))
		qt.Assert(t, qt.IsTrue(ok))
	})

	// Two consecutive dry-runs must report identically -- a dry-run never
	// executes a job (the dryRun guard in manageServers returns before
	// runInstallJob), so there is no state a first run could leave behind
	// for a second to see differently. This is the idempotency question
	// dry-run can actually answer without a real package manager; whether a
	// second *real* run is idempotent is installCommand's own documented
	// contract (every manager's install verb re-resolves and updates in
	// place, cargo included) and is exercised by hand, not CI, the same
	// boundary this file's package doc comment already draws.
	t.Run("two consecutive dry-runs report identically", func(t *testing.T) {
		t.Parallel()
		lookPath := fakeLookPath(map[string]string{
			"gopls": "/x/gopls", "bun": "/x/bun", "cargo": "/x/cargo",
		})

		var first, second bytes.Buffer
		ok1 := manageServers(true, &first, lookPath)
		ok2 := manageServers(true, &second, lookPath)

		qt.Assert(t, qt.Equals(ok1, ok2))
		qt.Assert(t, qt.Equals(first.String(), second.String()))
	})
}

// TestRunInstallJob covers runInstallJob's own failure-detection contract
// directly: a nonzero exit must print "FAILED" and report false, a clean
// exit must report true and print nothing extra. A command name guaranteed
// absent from PATH is a real, deterministic failure -- exec.Command's own "file
// not found" -- without ever invoking a real package manager; "true" is
// likewise a real, deterministic success available on every POSIX test
// runner. A real go/npm/cargo/bun install succeeding or failing for real
// stays exercised by hand, the same boundary this file's own package doc
// comment already draws around manageServers.
func TestRunInstallJob(t *testing.T) {
	t.Parallel()

	t.Run("nonzero exit reports false and prints FAILED", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		ok := runInstallJob("rgit-install-test-binary-does-not-exist", nil, &buf)
		qt.Assert(t, qt.IsFalse(ok))
		qt.Assert(t, qt.StringContains(buf.String(), "FAILED"))
	})

	t.Run("clean exit reports true", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		ok := runInstallJob("true", nil, &buf)
		qt.Assert(t, qt.IsTrue(ok))
		qt.Assert(t, qt.Not(qt.StringContains(buf.String(), "FAILED")))
	})
}
