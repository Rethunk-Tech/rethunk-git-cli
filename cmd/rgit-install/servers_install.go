// -with-servers itself: opt-in install/update of serverCatalog's managed
// entries (servers.go), per-ecosystem, with a loud warning when a manager's
// bin dir is not on PATH. A plain rgit-install (no flag) never calls
// manageServers, so its existing behavior is unchanged.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// installJob is one install-or-update invocation, covering every catalog
// entry that shares its (manager, pkg, extraArgs) -- vscode-json-language-
// server and vscode-css-language-server both come from
// vscode-langservers-extracted and must install once, not twice.
type installJob struct {
	manager   manager
	pkg       string
	extraArgs []string
	provides  []string
}

// buildInstallJobs groups serverCatalog into deduplicated install jobs,
// skipping managerNone entries (nothing to run for them; formatServerStatus
// already surfaces marksman's unmanagedHint). Order follows first
// appearance in catalog, so output is deterministic.
func buildInstallJobs(catalog []serverEntry) []installJob {
	byKey := map[string]*installJob{}
	var order []string
	for _, e := range catalog {
		if e.manager == managerNone {
			continue
		}
		key := fmt.Sprintf("%d|%s|%s", e.manager, e.pkg, strings.Join(e.extraArgs, " "))
		job, ok := byKey[key]
		if !ok {
			job = &installJob{manager: e.manager, pkg: e.pkg, extraArgs: e.extraArgs}
			byKey[key] = job
			order = append(order, key)
		}
		job.provides = append(job.provides, e.bin)
	}
	jobs := make([]installJob, 0, len(order))
	for _, k := range order {
		jobs = append(jobs, *byKey[k])
	}
	return jobs
}

// selectNPMManager prefers bun over npm when both are on PATH, matching the
// operator's own "bun/npm" ordering; npm is the fallback for hosts without
// bun. lookPath is injected for the same reason detectServers takes one.
func selectNPMManager(lookPath func(string) (string, error)) (cmd string, ok bool) {
	if _, err := lookPath("bun"); err == nil {
		return "bun", true
	}
	if _, err := lookPath("npm"); err == nil {
		return "npm", true
	}
	return "", false
}

// installCommand builds the argv for one job's install-or-update
// invocation. All three forms are naturally idempotent with no extra flag:
// `go install pkg@latest`, `npm install -g pkg`, and `bun add -g pkg` all
// re-resolve to the latest published version and update in place if it
// changed (verified: `bun add -g --dry-run` against an already-installed
// package resolves and reports the current version, not an error). cargo's
// own docs state it reinstalls whenever the resolved version, binary set,
// or --features differ from what's already there -- which is exactly what
// fixes the taplo LSP-feature bug at the source rather than only detecting
// it: requesting --features lsp here makes a previously-featureless taplo
// install look stale to cargo and get rebuilt.
func installCommand(job installJob, useBun bool) (name string, args []string) {
	switch job.manager {
	case managerGo:
		return "go", append([]string{"install"}, job.pkg)
	case managerCargo:
		return "cargo", append([]string{"install", job.pkg}, job.extraArgs...)
	case managerNPM:
		if useBun {
			return "bun", []string{"add", "-g", job.pkg}
		}
		return "npm", []string{"install", "-g", job.pkg}
	default:
		return "", nil
	}
}

// dirOnPATH reports whether dir appears, after cleaning, as one of pathEnv's
// entries. This is the check behind the loud PATH warning: `cargo install`
// wrote a correctly-built taplo to ~/.cargo/bin on 2026-07-28 while that
// directory was not on PATH, and the install reported success anyway --
// nothing surfaced the gap until this existed.
func dirOnPATH(dir, pathEnv string) bool {
	dir = filepath.Clean(dir)
	for _, p := range filepath.SplitList(pathEnv) {
		if filepath.Clean(p) == dir {
			return true
		}
	}
	return false
}

// formatPathWarning is decision 4's loud warning: installed, but unreachable.
func formatPathWarning(servers, dir string) string {
	return fmt.Sprintf(
		"WARNING: %s installed to %s, which is not on PATH -- the binary exists but nothing that shells out (rgit included) can reach it. Add %s to PATH.",
		servers, dir, dir,
	)
}

// managerBinDir resolves where a manager places the binaries it installs, so
// the caller can check that directory against PATH. Only the go and cargo
// branches are pure enough to unit test without shelling out (go's through
// resolvePrefix, already exercised the same way; cargo's through a plain
// env var read). npm/bun's bin dir depends on `npm config get prefix` /
// `bun pm bin -g` and is exercised by hand instead -- the same boundary
// main_test.go already draws around resolvePrefix's own GOBIN/GOPATH
// fallback.
func managerBinDir(m manager, useBun bool) (string, error) {
	switch m {
	case managerGo:
		return resolvePrefix("")
	case managerCargo:
		if home := os.Getenv("CARGO_HOME"); home != "" {
			return filepath.Join(home, "bin"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".cargo", "bin"), nil
	case managerNPM:
		if useBun {
			out, err := exec.Command("bun", "pm", "bin", "-g").Output()
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(out)), nil
		}
		out, err := exec.Command("npm", "config", "get", "prefix").Output()
		if err != nil {
			return "", err
		}
		return filepath.Join(strings.TrimSpace(string(out)), "bin"), nil
	default:
		return "", fmt.Errorf("no bin dir for manager %d", m)
	}
}

// manageServers is -with-servers' entry point: report every catalog entry's
// detection/capability status, then install-or-update whatever has a
// manager, warning loudly per entry when the manager's bin dir is not on
// PATH. dryRun prints each command instead of running it -- generateSQLParser
// draws the same distinction for the SQL path.
func manageServers(dryRun bool, stdout io.Writer) {
	fmt.Fprintln(stdout, "Language servers:")
	for _, s := range detectServers(serverCatalog, exec.LookPath) {
		fmt.Fprintln(stdout, "  "+formatServerStatus(s))
	}

	jobs := buildInstallJobs(serverCatalog)
	if len(jobs) == 0 {
		return
	}

	npmCmd, npmOK := selectNPMManager(exec.LookPath)
	fmt.Fprintln(stdout, "Installing/updating:")
	for _, job := range jobs {
		label := strings.Join(job.provides, ", ")

		if job.manager == managerNPM && !npmOK {
			fmt.Fprintf(stdout, "  skip %s: neither bun nor npm on PATH\n", label)
			continue
		}
		if job.manager == managerCargo {
			if _, err := exec.LookPath("cargo"); err != nil {
				fmt.Fprintf(stdout, "  skip %s: cargo not on PATH\n", label)
				continue
			}
		}

		useBun := npmCmd == "bun"
		cmdName, args := installCommand(job, useBun)
		fmt.Fprintf(stdout, "  %s: %s %s\n", label, cmdName, strings.Join(args, " "))
		if dryRun {
			continue
		}

		cmd := exec.Command(cmdName, args...)
		cmd.Stdout = stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(stdout, "    FAILED: %v\n", err)
			continue
		}

		dir, err := managerBinDir(job.manager, useBun)
		if err == nil && !dirOnPATH(dir, os.Getenv("PATH")) {
			fmt.Fprintln(stdout, "    "+formatPathWarning(label, dir))
		}
	}
}
