package lsptest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// RunWithoutManagedDaemon runs m with $XDG_RUNTIME_DIR pointed at a fresh
// directory whose rgit-<uid> subdirectory is group/world-readable, which
// rgit refuses to trust. Every dial then fails closed to [ts-only] instead
// of reaching the machine's shared gopls daemon or spawning one that
// outlives the run. A test that wants a daemon sets its own
// XDG_RUNTIME_DIR or RGIT_LSP_SOCKET with t.Setenv.
func RunWithoutManagedDaemon(m *testing.M) int {
	dir, err := os.MkdirTemp("", "rgit-test-runtime-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "lsptest:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	untrusted := filepath.Join(dir, fmt.Sprintf("rgit-%d", os.Getuid()))
	if err := os.Mkdir(untrusted, 0o755); err != nil { //nolint:gosec // fixture intentionally creates an untrusted world-readable runtime directory
		fmt.Fprintln(os.Stderr, "lsptest:", err)
		return 1
	}
	if err := os.Chmod(untrusted, 0o755); err != nil { //nolint:gosec // fixture must remain world-readable to exercise fail-closed trust checks
		fmt.Fprintln(os.Stderr, "lsptest:", err)
		return 1
	}
	if err := os.Setenv("XDG_RUNTIME_DIR", dir); err != nil {
		fmt.Fprintln(os.Stderr, "lsptest:", err)
		return 1
	}
	return m.Run()
}
