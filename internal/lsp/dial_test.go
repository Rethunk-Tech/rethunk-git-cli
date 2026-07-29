package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// noopDaemonArgs is a serverSpec.daemonArgs for a test spec whose "daemon"
// never actually needs to listen on sockPath -- trySpawnDaemon never waits
// on what it spawns, so a process that starts and exits immediately
// exercises the same code path a real long-lived gopls would.
func noopDaemonArgs(string) []string { return nil }

// TestTrySpawnDaemon_BinaryNotOnPATH covers the "load-bearing" no-op this
// function is specs/design.md's rule for: a language server that is not
// installed must never even attempt to create a spawn lock, since nothing
// will ever clear one for a binary that can never be spawned.
func TestTrySpawnDaemon_BinaryNotOnPATH(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	spec := serverSpec{name: "test", bin: "rgit-lsp-test-binary-does-not-exist", daemonArgs: noopDaemonArgs}

	trySpawnDaemon(spec, sockPath)

	if _, err := os.Stat(sockPath + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock file = %v; want no lock created when the binary is absent", err)
	}
}

// TestTrySpawnDaemon_FreshLockIsLeftAlone covers the "another invocation is
// already spawning" branch: a lock younger than staleLockAge must survive
// untouched, or a burst of concurrent rgit invocations would each clear and
// recreate it instead of the one spawn the lock is meant to serialize.
func TestTrySpawnDaemon_FreshLockIsLeftAlone(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	lockPath := sockPath + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// A real binary must be reachable for LookPath to get past its own
	// early return -- otherwise this would pass for the wrong reason.
	spec := serverSpec{name: "test", bin: "true", daemonArgs: noopDaemonArgs}
	trySpawnDaemon(spec, sockPath)

	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file removed; want a fresh lock left in place: %v", err)
	}
}

// TestTrySpawnDaemon_StaleLockIsCleared covers the recovery path: a lock
// left behind by a process that died before its own deferred cleanup ran
// must not pin every later invocation to [ts-only] forever.
func TestTrySpawnDaemon_StaleLockIsCleared(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	lockPath := sockPath + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(lockPath, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	spec := serverSpec{name: "test", bin: "true", daemonArgs: noopDaemonArgs}
	trySpawnDaemon(spec, sockPath)

	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lock file = %v; want the stale lock removed", err)
	}
}

// TestTrySpawnDaemon_SuccessfulSpawnCleansUpItsOwnLock covers the ordinary
// path all the way through: lock acquired, process started and detached,
// lock cleaned up -- so a later invocation is never left believing a spawn
// is still in progress when it already finished.
func TestTrySpawnDaemon_SuccessfulSpawnCleansUpItsOwnLock(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	spec := serverSpec{name: "test", bin: "true", daemonArgs: noopDaemonArgs}

	trySpawnDaemon(spec, sockPath)

	if _, err := os.Stat(sockPath + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock file = %v; want removed once the spawn completed", err)
	}
}

// TestTrySpawnDaemon_StartFailureIsToleratedSilently covers cmd.Start()
// itself failing after the lock is already held: an executable-bit file
// with no valid binary format (no shebang, not an ELF) is found by
// exec.LookPath -- it is on PATH and marked executable -- but the kernel
// refuses to exec it, exactly the shape of failure a corrupt or
// half-installed language server binary would produce. trySpawnDaemon must
// not propagate this; a caller-visible failure here would surface as
// rgit's own error for a background optimization it never asked about.
func TestTrySpawnDaemon_StartFailureIsToleratedSilently(t *testing.T) {
	binDir := t.TempDir()
	fakeBin := filepath.Join(binDir, "not-a-real-executable")
	if err := os.WriteFile(fakeBin, []byte("this is not an executable\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	spec := serverSpec{name: "test", bin: "not-a-real-executable", daemonArgs: noopDaemonArgs}

	trySpawnDaemon(spec, sockPath)

	// The lock is still cleaned up by the same deferred cleanup regardless
	// of whether the spawn it guarded actually succeeded.
	if _, err := os.Stat(sockPath + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock file = %v; want removed even though Start() failed", err)
	}
}

// TestDialStdio_CloseTearsDownConnectionThenProcess pins the teardown
// order: closing a stdio client must close the connection -- letting
// jsonrpc2's own read goroutine and the pipe's EOF-then-close sequence
// shut down cleanly -- before killing the subprocess, not kill the
// process out from under a connection that was never closed at all. A
// query issued after Close is the observable proof the connection came
// down and not just the process; a kill-only closeFn would leave both the
// jsonrpc2.Conn and the pipes open forever.
//
// Runs against a real, installed stdio server (CONTRIBUTING.md's "prefer
// the real dependency over a double" rule -- a fake transport would only
// prove this package calls its own mock correctly) and is exactly the
// concurrency go test -race exists for: jsonrpc2's own read goroutine is
// still live when Close begins tearing the connection down.
func TestDialStdio_CloseTearsDownConnectionThenProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("live language-server dial skipped under -short")
	}
	const bin = "bash-language-server"
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("%s not on PATH", bin)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.sh")
	src := []byte("foo() {\n  echo hi\n}\n")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, degraded := Dial(ctx, "shell", dir)
	if degraded {
		t.Fatal("Dial degraded with bash-language-server on PATH")
	}
	if _, err := client.DocumentSymbols(ctx, path, src); err != nil {
		t.Fatalf("DocumentSymbols before Close: %v", err)
	}

	// Close's own return is not asserted nil: killAndReap always sends
	// SIGKILL as insurance even after a clean conn.Close-triggered exit,
	// so cmd.Wait() legitimately reports "signal: killed" -- unchanged
	// from this package's prior behaviour and not what this test is
	// pinning. What matters is that the connection came down.
	_ = client.Close()

	if _, err := client.DocumentSymbols(ctx, path, src); err == nil {
		t.Error("DocumentSymbols after Close = nil error; want one -- the connection should already be closed")
	}
}
