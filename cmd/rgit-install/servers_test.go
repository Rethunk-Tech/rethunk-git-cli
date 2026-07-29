// Detection and capability-reporting coverage. taploCapability itself shells
// out to a real binary and is exercised by hand, the same boundary
// main_test.go draws around resolvePrefix's GOBIN/GOPATH fallback -- these
// tests inject fakes instead.
package main

import (
	"fmt"
	"testing"

	qt "github.com/go-quicktest/qt"
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
		{name: "gopls", bin: "gopls", manager: managerGo},
		{name: "missing", bin: "does-not-exist", manager: managerNPM},
		{
			name: "taplo", bin: "taplo", manager: managerCargo,
			capability: func(bin string) (bool, string) {
				qt.Assert(t, qt.Equals(bin, "/usr/bin/taplo"))
				return false, "no lsp subcommand"
			},
		},
		{
			name: "marksman", bin: "marksman", manager: managerNone,
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
		name string
		st   serverStatus
		want string
	}{
		{
			name: "not found, managed",
			st:   serverStatus{entry: serverEntry{bin: "gopls", manager: managerGo}},
			want: "gopls                        not found",
		},
		{
			name: "not found, unmanaged shows the hand-run hint",
			st: serverStatus{entry: serverEntry{
				bin: "marksman", manager: managerNone,
				unmanagedHint: "download a release binary from GitHub",
			}},
			want: "marksman                     not found -- download a release binary from GitHub",
		},
		{
			name: "found and capable",
			st: serverStatus{
				entry: serverEntry{bin: "gopls", manager: managerGo}, pathFound: true,
				foundAt: "/usr/bin/gopls", capable: true,
			},
			want: "gopls                        found at /usr/bin/gopls",
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
			want: `taplo                        found at /home/x/.cargo/bin/taplo but unusable: no "lsp" subcommand`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			qt.Assert(t, qt.Equals(formatServerStatus(tt.st), tt.want))
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
