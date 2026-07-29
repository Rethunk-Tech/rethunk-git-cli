// -with-servers (wired in servers_install.go) extends the installer to the
// language servers rgit's LSP cross-check dials (docs/INSTALL.md §
// Language servers), not just rgit itself. This file holds the catalog and
// its detection/capability logic; servers_install.go holds the opt-in
// install/update behavior that consumes it.
package main

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// manager identifies which package manager installs/updates a server.
// managerNone means no package manager exists for it at all -- marksman
// ships GitHub release binaries only, out of scope per the operator's own
// decision not to teach this installer HTTP fetching for one server.
type manager int

const (
	managerNone manager = iota
	managerGo
	managerNPM
	managerCargo
)

// serverEntry is one server rgit's LSP cross-check can dial, plus how
// -with-servers installs or updates it.
//
// This catalog is hardcoded rather than driven from internal/lsp/servers.go
// at runtime: every field there (serverSpec, the servers map itself) is
// unexported, so nothing in that package is importable from outside it, and
// it is under active edit as new grammars land (verified live against its
// current contents on 2026-07-28: only go/typescript/tsx/python/shell are
// wired so far). Keeping this list in sync with internal/lsp/servers.go and
// docs/INSTALL.md's Language servers table is a manual coupling this
// comment exists to flag, not to hide -- there is no exported surface to
// drive it from instead. The five entries below beyond those currently
// wired (yaml, json, css, taplo, marksman) match docs/INSTALL.md's and the
// operator's own reference list for servers the newer tree-sitter grammars
// (css, json, toml -- see this repo's go.mod) will need cross-checked next.
type serverEntry struct {
	// name mirrors internal/lsp/servers.go's own "name" field where a
	// counterpart already exists there.
	name string
	// bin is the binary rgit looks up on PATH -- and what this installer
	// checks and installs.
	bin     string
	manager manager
	// pkg is the manager-specific package/module identifier passed to the
	// install command. Empty when manager is managerNone. For managerGo
	// this already includes the "@latest" version query go install needs.
	pkg string
	// extraArgs are flags beyond the bare package name. taplo needs
	// exactly this: `cargo install taplo-cli` alone builds without LSP
	// support (npm's own @taplo/cli 0.9.0 has none at all, which is the
	// bug this whole feature exists to catch and fix at the source).
	extraArgs []string
	// unmanagedHint is what to print instead of installing when manager is
	// managerNone -- the command a user would run by hand.
	unmanagedHint string
	// capability, if set, checks the binary is not merely present but
	// actually usable. Presence proved nothing for taplo on 2026-07-28: an
	// npm-installed binary answered fine on PATH while speaking no LSP.
	capability func(bin string) (ok bool, detail string)
}

var serverCatalog = []serverEntry{
	{
		name:    "gopls",
		bin:     "gopls",
		manager: managerGo,
		pkg:     "golang.org/x/tools/gopls@latest",
	},
	{
		name:    "vtsls",
		bin:     "vtsls",
		manager: managerNPM,
		pkg:     "@vtsls/language-server",
	},
	{
		name:    "pyright",
		bin:     "pyright-langserver",
		manager: managerNPM,
		pkg:     "pyright",
	},
	{
		name:    "bash-language-server",
		bin:     "bash-language-server",
		manager: managerNPM,
		pkg:     "bash-language-server",
	},
	{
		name:    "yaml-language-server",
		bin:     "yaml-language-server",
		manager: managerNPM,
		pkg:     "yaml-language-server",
	},
	{
		// Same npm package as vscode-css-language-server below --
		// buildInstallJobs (servers_install.go) groups them into one
		// install, not two.
		name:    "vscode-json-language-server",
		bin:     "vscode-json-language-server",
		manager: managerNPM,
		pkg:     "vscode-langservers-extracted",
	},
	{
		name:    "vscode-css-language-server",
		bin:     "vscode-css-language-server",
		manager: managerNPM,
		pkg:     "vscode-langservers-extracted",
	},
	{
		name:          "marksman",
		bin:           "marksman",
		manager:       managerNone,
		unmanagedHint: "no package manager publishes it -- download a release binary from https://github.com/artempyanykh/marksman/releases and put it on PATH",
	},
	{
		name:       "taplo",
		bin:        "taplo",
		manager:    managerCargo,
		pkg:        "taplo-cli",
		extraArgs:  []string{"--locked", "--features", "lsp"},
		capability: taploCapability,
	},
}

// taploCapability runs `taplo lsp --help` and treats a clean exit as proof
// the binary understands the "lsp" subcommand at all. npm's @taplo/cli
// 0.9.0 has no such subcommand and exits nonzero for it; a cargo build with
// --features lsp prints its own help and exits 0 -- verified directly
// against both on 2026-07-28, which is the exact presence-vs-capability gap
// this function closes.
func taploCapability(bin string) (ok bool, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, bin, "lsp", "--help").Run(); err != nil {
		return false, `found on PATH but has no "lsp" subcommand -- likely built from npm's @taplo/cli, which ships no LSP support; run with -with-servers to reinstall via cargo`
	}
	return true, ""
}

// serverStatus is one catalog entry's detection result.
type serverStatus struct {
	entry     serverEntry
	pathFound bool
	foundAt   string
	capable   bool
	capDetail string
}

// detectServers checks every catalog entry against lookPath, running each
// entry's own capability check (if any) once a binary is found. lookPath is
// injected so this stays a pure function of its inputs -- production passes
// exec.LookPath, tests pass a fake.
func detectServers(catalog []serverEntry, lookPath func(string) (string, error)) []serverStatus {
	statuses := make([]serverStatus, 0, len(catalog))
	for _, e := range catalog {
		st := serverStatus{entry: e, capable: true}
		if path, err := lookPath(e.bin); err == nil {
			st.pathFound = true
			st.foundAt = path
			if e.capability != nil {
				st.capable, st.capDetail = e.capability(path)
			}
		}
		statuses = append(statuses, st)
	}
	return statuses
}

// formatServerStatus renders one detection result as a single report line.
func formatServerStatus(s serverStatus) string {
	const width = 28
	label := fmt.Sprintf("%-*s", width, s.entry.bin)
	switch {
	case !s.pathFound && s.entry.manager == managerNone:
		return fmt.Sprintf("%s not found -- %s", label, s.entry.unmanagedHint)
	case !s.pathFound:
		return fmt.Sprintf("%s not found", label)
	case !s.capable:
		return fmt.Sprintf("%s found at %s but unusable: %s", label, s.foundAt, s.capDetail)
	default:
		return fmt.Sprintf("%s found at %s", label, s.foundAt)
	}
}
