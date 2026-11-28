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
	_ = os.Unsetenv("CLAUDE_CODE_SESSION_ID")
	if dir := os.Getenv("RGIT_TEST_LAND_SIGINT_DIR"); dir != "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := Run(ctx, "test", []string{"-C", dir, "land"}, os.Stdout, os.Stderr)
		stop()
		os.Exit(int(code))
	}
	os.Exit(lsptest.RunWithoutManagedDaemon(m))
}
