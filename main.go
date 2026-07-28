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
	"syscall"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/app"
)

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=vX.Y.Z"
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := app.Run(ctx, version, os.Args[1:], os.Stdout, os.Stderr)
	stop() // not deferred: os.Exit would skip it
	os.Exit(int(code))
}
