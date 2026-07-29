// Detection and capability-reporting coverage. detectServers and
// formatServerStatus take fakes; taploCapability instead gets a real fake
// binary (a tiny shell script) on disk and is run for real, since it is
// taplo's own exit code that matters -- a Go-level double for "did the
// subprocess exit 0" would just restate the function under test.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	qt "github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
)

func fakeLookPath(found map[string]string) func(string) (string, error) {
	return func(bin string) (string, error) {
		if p, ok := found[bin]; ok {
			return p, nil
		}
		return "", fmt.Errorf("%s: not found", bin)
	}
}

func TestDetectServers(t *testing.T) {
	t.Parallel()

	catalog := []serverEntry{
		{bin: "gopls", manager: managerGo},
		{bin: "does-not-exist", manager: managerNPM},
		{
			bin: "taplo", manager: managerCargo,
			capability: func(bin string) (bool, string) {
				qt.Assert(t, qt.Equals(bin, "/usr/bin/taplo"))
				return false, "no lsp subcommand"
			},
		},
		{
			bin: "marksman", manager: managerNone,
			unmanagedHint: "download a release binary",
		},
	}
	lookPath := fakeLookPath(map[string]string{
		"gopls": "/usr/bin/gopls",
		"taplo": "/usr/bin/taplo",
	})

	got := detectServers(catalog, lookPath)
	qt.Assert(t, qt.HasLen(got, 4))

	qt.Assert(t, qt.IsTrue(got[0].pathFound))
	qt.Assert(t, qt.Equals(got[0].foundAt, "/usr/bin/gopls"))
	qt.Assert(t, qt.IsTrue(got[0].capable)) // no capability func -- presence is enough

	qt.Assert(t, qt.IsFalse(got[1].pathFound))

	// The presence-is-not-capability case: found on PATH, but its own
	// capability check says it is unusable.
	qt.Assert(t, qt.IsTrue(got[2].pathFound))
	qt.Assert(t, qt.IsFalse(got[2].capable))
	qt.Assert(t, qt.Equals(got[2].capDetail, "no lsp subcommand"))

	qt.Assert(t, qt.IsFalse(got[3].pathFound))
}

func TestFormatServerStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		st        serverStatus
		wantParts []string
	}{
		{
			name:      "not found, managed",
			st:        serverStatus{entry: serverEntry{bin: "gopls", manager: managerGo}},
			wantParts: []string{"gopls", "not found"},
		},
		{
			name: "not found, unmanaged shows the hand-run hint",
			st: serverStatus{entry: serverEntry{
				bin: "marksman", manager: managerNone,
				unmanagedHint: "download a release binary from GitHub",
			}},
			wantParts: []string{"marksman", "not found -- download a release binary from GitHub"},
		},
		{
			name: "found and capable",
			st: serverStatus{
				entry: serverEntry{bin: "gopls", manager: managerGo}, pathFound: true,
				foundAt: "/usr/bin/gopls", capable: true,
			},
			wantParts: []string{"gopls", "found at /usr/bin/gopls"},
		},
		{
			// A naive presence check would report this line identically to
			// the capable case above -- exactly the gap that lets a
			// featureless install (e.g. npm's taplo) go unnoticed.
			name: "found but not capable explains why",
			st: serverStatus{
				entry:     serverEntry{bin: "taplo", manager: managerCargo},
				pathFound: true, foundAt: "/home/x/.cargo/bin/taplo",
				capable: false, capDetail: `no "lsp" subcommand`,
			},
			wantParts: []string{"taplo", `found at /home/x/.cargo/bin/taplo but unusable: no "lsp" subcommand`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Substrings, not the whole padded line: the column width is a
			// layout choice, and pinning it byte-for-byte fails this test
			// for a change altering nothing a reader depends on. What must
			// hold is that the binary is named and its status reads
			// correctly -- including the "unusable" case a naive presence
			// check would render identically to the capable one.
			got := formatServerStatus(tt.st)
			for _, want := range tt.wantParts {
				qt.Assert(t, qt.StringContains(got, want))
			}
		})
	}
}

// serverCatalog itself is data, not logic -- but a duplicate manager/pkg/bin
// mismatch here would silently break buildInstallJobs' grouping or
// detectServers' PATH lookups, so pin the invariants that matter.
func TestServerCatalog(t *testing.T) {
	t.Parallel()

	seenBin := map[string]bool{}
	for _, e := range serverCatalog {
		qt.Assert(t, qt.Not(qt.Equals(e.bin, "")))
		qt.Assert(t, qt.IsFalse(seenBin[e.bin])) // each binary listed once
		seenBin[e.bin] = true

		if e.manager == managerNone {
			qt.Assert(t, qt.Not(qt.Equals(e.unmanagedHint, "")))
		} else {
			qt.Assert(t, qt.Not(qt.Equals(e.pkg, "")))
		}
	}

	// taplo is the one entry that needs non-default install options to be
	// useful at all (see servers_install.go's installCommand doc comment).
	var taplo *serverEntry
	for i := range serverCatalog {
		if serverCatalog[i].bin == "taplo" {
			taplo = &serverCatalog[i]
		}
	}
	qt.Assert(t, qt.IsNotNil(taplo))
	qt.Assert(t, qt.DeepEquals(taplo.extraArgs, []string{"--locked", "--features", "lsp"}))
	qt.Assert(t, qt.IsNotNil(taplo.capability))
}

// TestServerCatalog_MatchesLSPServers is a drift test, not a shared
// catalog: internal/lsp's own serverSpec is unexported, and importing
// package lsp from this main package's own production code would pull in
// its full jsonrpc2/LSP client dependency graph purely for a data list
// (servers.go's own doc comment measures the cost). A _test.go import
// never ships in the built rgit-install binary, so comparing catalogs
// here costs nothing.
//
// taplo is the one documented exception: specs/design.md measured its
// ranges genuinely disagreeing with this resolver's own extents on a
// nested TOML table, so it stays permanently unwired in
// internal/lsp/servers.go despite being installed here for a user's own
// editor tooling.
func TestServerCatalog_MatchesLSPServers(t *testing.T) {
	t.Parallel()

	const taplo = "taplo"

	wired := map[string]bool{}
	for _, s := range lsp.Servers() {
		wired[s.Bin] = true
	}

	for _, e := range serverCatalog {
		if e.bin == taplo {
			continue
		}
		if !wired[e.bin] {
			t.Errorf("serverCatalog has %q with no counterpart in internal/lsp.Servers() -- either it was unwired from internal/lsp/servers.go (update this catalog and its comment) or this catalog is stale", e.bin)
		}
		delete(wired, e.bin)
	}
	for bin := range wired {
		t.Errorf("internal/lsp.Servers() wires %q but serverCatalog has no entry for it -- a user running -with-servers cannot install it", bin)
	}
}

// fakeTaplo writes a tiny script at a controlled path that behaves like
// `taplo lsp --help` would -- exiting 0 (a cargo build with --features lsp)
// or nonzero (npm's featureless @taplo/cli, which has no "lsp" subcommand
// at all). taploCapability is called with this path directly, so no PATH
// manipulation is needed.
func fakeTaplo(t *testing.T, exitCode int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "taplo")
	script := fmt.Sprintf("#!/bin/sh\nexit %d\n", exitCode)
	qt.Assert(t, qt.IsNil(os.WriteFile(path, []byte(script), 0o755)))
	return path
}

// mustTaploCapable retries taploCapability a bounded number of times when it
// unexpectedly reports incapable, to absorb a real, measured Linux race
// rather than mask a genuine regression.
//
// fork() duplicates a process's entire file descriptor table before
// execve() replaces it, so an unrelated goroutine's own fork (a parallel
// subtest doing this same write-then-exec pattern, another test in this
// package invoking a real binary, or -- the condition this actually
// surfaced under -- another `go build`/`go test` hammering this same
// checkout concurrently) can hold bin's freshly-written, already-closed
// file open just long enough that the kernel reports ETXTBSY ("text file
// busy") to this goroutine's own exec, even though bin's own os.WriteFile
// (fakeTaplo) already returned before this ever runs.
//
// Reproduced directly, not assumed: `go test -race -count=15` under heavy
// concurrent build/test load (three background `go build ./...`/`go test
// ./...` loops plus CPU load) failed intermittently, and a throwaway
// instrumented run -- taploCapability's own return shape does not expose
// the underlying error -- captured the raw cause as exactly
// `fork/exec .../taplo: text file busy`, never a data race `-race` itself
// flagged (this is a kernel-level exec race, not a Go memory race, and
// candidate explanations involving PATH or a shared temp dir were ruled
// out first: taploCapability takes bin as a full path and never consults
// PATH, and every subtest's fakeTaplo path comes from its own t.TempDir()).
//
// Only the "lsp subcommand present" subtest below needs this: the other
// two already expect ok=false, which is exactly what an ETXTBSY-induced
// failure also produces, so they cannot distinguish the race from their own
// expected outcome. taploCapability has no other source of nondeterminism
// against a script that deterministically exits 0 -- a real regression in
// it would fail every attempt, not just some.
//
// Production code is deliberately untouched: manageServers
// (servers_install.go) never runs a capability check against a binary this
// same process just wrote -- detectServers runs exactly once, before any
// install job -- so this is a test-harness-only hazard, not one
// taploCapability itself needs to defend against.
func mustTaploCapable(t *testing.T, bin string) (ok bool, detail string) {
	t.Helper()
	const attempts = 5
	for i := 0; i < attempts; i++ {
		ok, detail = taploCapability(bin)
		if ok {
			return ok, detail
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ok, detail
}

// TestTaploCapability is the presence-vs-capability regression this whole
// feature exists for: a taplo that answers on PATH is not necessarily one
// that speaks LSP, and a wrong answer here would silently report a server
// as usable when it is not.
func TestTaploCapability(t *testing.T) {
	t.Parallel()

	t.Run("lsp subcommand present -- a real cargo build", func(t *testing.T) {
		t.Parallel()
		ok, detail := mustTaploCapable(t, fakeTaplo(t, 0))
		qt.Assert(t, qt.IsTrue(ok))
		qt.Assert(t, qt.Equals(detail, ""))
	})

	t.Run("no lsp subcommand -- npm's @taplo/cli shape", func(t *testing.T) {
		t.Parallel()
		ok, detail := taploCapability(fakeTaplo(t, 1))
		qt.Assert(t, qt.IsFalse(ok))
		qt.Assert(t, qt.StringContains(detail, `no "lsp" subcommand`))
	})

	t.Run("binary does not exist at all", func(t *testing.T) {
		t.Parallel()
		ok, detail := taploCapability(filepath.Join(t.TempDir(), "does-not-exist"))
		qt.Assert(t, qt.IsFalse(ok))
		qt.Assert(t, qt.StringContains(detail, `no "lsp" subcommand`))
	})
}
