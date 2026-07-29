// Command rgit is git add <pathspec> && git commit at symbol granularity.
// See AGENTS.md for the governing invariant and docs/USAGE.md for the full
// command reference.
//
// Everything beyond process wiring lives in internal/app so it can be tested
// without building a binary.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/app"
)

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=vX.Y.Z"
//
// Plain `go build`/`go install ./cmd/rgit` (docs/INSTALL.md's second and
// third documented paths) pass no such ldflags, so this stays "dev" until
// resolveVersion falls back to the toolchain's own embedded VCS metadata.
var version = "dev"

func main() {
	info, ok := debug.ReadBuildInfo()
	resolved := resolveVersion(version, info, ok)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := app.Run(ctx, resolved, os.Args[1:], os.Stdout, os.Stderr)
	stop() // not deferred: os.Exit would skip it
	os.Exit(int(code))
}

// resolveVersion returns what rgit --version should report. The ldflags
// value wins whenever `make build`/`make install`/cmd/rgit-install set one
// (Makefile:12-14, cmd/rgit-install/main.go's ldflags helper) -- this never
// overrides it, so those two paths report exactly what they already report.
//
// Absent that, `go build`/`go install ./cmd/rgit` still get something truthful
// instead of the bare "dev" default: runtime/debug.ReadBuildInfo exposes the
// same VCS data the go tool embeds automatically from within a git checkout
// (`go version -m` on a plain build shows vcs.revision, vcs.time, and
// vcs.modified populated with no ldflags at all). Only vcs.revision and
// vcs.modified are used here -- vcs.time adds nothing git describe doesn't
// already convey via the revision itself.
//
// The dirty-suffix spelling ("-dirty") and the 7-character abbreviation
// match `git describe --tags --always --dirty`, which is what
// cmd/rgit-install's gitVersion (and Makefile's VERSION) already produce --
// deliberately the same shape either install path reports, not a second
// format.
func resolveVersion(ldflags string, info *debug.BuildInfo, ok bool) string {
	if ldflags != "" && ldflags != "dev" {
		return ldflags
	}
	if !ok || info == nil {
		return ldflags
	}

	const shortHashLen = 7
	var revision string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		// No VCS metadata (e.g. built outside a git checkout, or with
		// -buildvcs=false) -- nothing truthful to report beyond the ldflags
		// default.
		return ldflags
	}
	if len(revision) > shortHashLen {
		revision = revision[:shortHashLen]
	}
	if modified {
		revision += "-dirty"
	}
	return revision
}
