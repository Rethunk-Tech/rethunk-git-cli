// Install/update coverage: the manager-selection table, command
// construction per ecosystem, PATH-reachability detection, and warning
// formatting -- CONTRIBUTING.md's "test the pure logic" boundary. Nothing
// here shells out to a real package manager; manageServers itself (which
// does) is exercised by hand with -dry-run instead.
package main

import (
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
			// The regression this guards: taplo built via `cargo install
			// taplo-cli` alone has no LSP support at all -- extraArgs must
			// reach the invocation.
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

	// The regression this guards: cargo wrote taplo to ~/.cargo/bin on
	// 2026-07-28 while that directory was absent from PATH, and nothing
	// caught it.
	qt.Assert(t, qt.IsFalse(dirOnPATH("/home/x/.cargo/bin", path)))
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
