// Command rgit is git add <pathspec> && git commit at symbol granularity.
// See AGENTS.md for the governing invariant and docs/USAGE.md for the full
// command reference.
//
// Everything beyond process wiring lives in internal/app so it can be tested
// without building a binary.
package main

import (
	"os"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/app"
)

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=vX.Y.Z"
var version = "dev"

func main() {
	os.Exit(int(app.Run(version, os.Args[1:], os.Stdout, os.Stderr)))
}
