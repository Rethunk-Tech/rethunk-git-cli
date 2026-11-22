package app

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/lsptest"
)

func TestMain(m *testing.M) {
	if dir := os.Getenv("RGIT_TEST_LAND_SIGINT_DIR"); dir != "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		os.Exit(int(Run(ctx, "test", []string{"-C", dir, "land"}, os.Stdout, os.Stderr)))
	}
	os.Exit(lsptest.RunWithoutManagedDaemon(m))
}
