package lsp

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsptest"
)

// shortTempDir returns a fresh, short-named temp directory, cleaned up when
// t completes. Unlike t.TempDir(), its name does not embed the calling
// test's own name -- needed wherever a path built from it is bound as a
// unix socket, which is capped at ~108 bytes (sun_path) on Linux and can
// overflow once a long test name and privateSocketDir's own "rgit-<uid>"
// subdirectory are both appended to it. It roots at /tmp where one exists
// rather than $TMPDIR: macOS runners and scratch harnesses set a TMPDIR long
// enough to overflow sun_path on its own.
func shortTempDir(t *testing.T) string {
	t.Helper()
	base := ""
	if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "rgit-lsp-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// noopDaemonArgs is a serverSpec.daemonArgs for a test spec whose "daemon"
// never actually needs to listen on sockPath -- trySpawnDaemon never waits
// on what it spawns, so a process that starts and exits immediately
// exercises the same code path a real long-lived gopls would.
func noopDaemonArgs(string) []string { return nil }

// writeStaleLock creates sockPath's lock file already backdated past
// staleLockAge -- the shape trySpawnDaemon's stale-lock recovery path
// exists for: a lock left behind by a process that died before its own
// deferred cleanup ran.
func writeStaleLock(t *testing.T, sockPath string) {
	t.Helper()
	lockPath := sockPath + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(lockPath, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
}

// TestTrySpawnDaemon_BinaryNotOnPATH covers the "load-bearing" no-op this
// function exists for: a language server that is not
// installed must never even attempt to create a spawn lock, since nothing
// will ever clear one for a binary that can never be spawned.
func TestTrySpawnDaemon_BinaryNotOnPATH(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	spec := serverSpec{name: "test", bin: "rgit-lsp-test-binary-does-not-exist", daemonArgs: noopDaemonArgs}

	trySpawnDaemon(t.Context(), spec, sockPath)

	if _, err := os.Stat(sockPath + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock file = %v; want no lock created when the binary is absent", err)
	}
}

// TestTrySpawnDaemon_FreshLockIsLeftAlone covers the "another invocation is
// already spawning" branch: a lock younger than staleLockAge must survive
// untouched, or a burst of concurrent rgit invocations would each clear and
// recreate it instead of the one spawn the lock is meant to serialize.
func TestTrySpawnDaemon_FreshLockIsLeftAlone(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	lockPath := sockPath + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// A real binary must be reachable for LookPath to get past its own
	// early return -- otherwise this would pass for the wrong reason.
	spec := serverSpec{name: "test", bin: "true", daemonArgs: noopDaemonArgs}
	trySpawnDaemon(t.Context(), spec, sockPath)

	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file removed; want a fresh lock left in place: %v", err)
	}
}

// TestTrySpawnDaemon_StaleLockIsCleared covers the recovery path: a lock
// left behind by a process that died before its own deferred cleanup ran
// must not pin every later invocation to [ts-only] forever.
func TestTrySpawnDaemon_StaleLockIsCleared(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	lockPath := sockPath + ".lock"
	writeStaleLock(t, sockPath)

	spec := serverSpec{name: "test", bin: "true", daemonArgs: noopDaemonArgs}
	trySpawnDaemon(t.Context(), spec, sockPath)

	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lock file = %v; want the stale lock removed", err)
	}
}

// TestTrySpawnDaemon_SymlinkLockIsNeverAged guards acquireSpawnLock's own
// Lstat: a lock path is a symlink, never a plain lock file, when another
// user in this same world-writable-by-default runtime directory has
// planted one -- privateSocketDir's own doc comment gives the parallel
// reasoning for the socket path itself. Following it (os.Stat) would read
// whatever the symlink points at instead of the lock rgit created, so a
// target backdated past staleLockAge would convince this invocation a
// live lock is abandoned and worth clearing, exactly the confusion the
// fix closes: a lock this function cannot vouch for as a regular file is
// left in place untouched, the same as any other stat failure.
func TestTrySpawnDaemon_SymlinkLockIsNeverAged(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	lockPath := sockPath + ".lock"

	target := filepath.Join(t.TempDir(), "old-target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(target, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, lockPath); err != nil {
		t.Fatal(err)
	}

	spec := serverSpec{name: "test", bin: "true", daemonArgs: noopDaemonArgs}
	trySpawnDaemon(t.Context(), spec, sockPath)

	if _, err := os.Lstat(lockPath); err != nil {
		t.Errorf("symlink lock removed (%v); want it left in place untouched", err)
	}
}

// TestTrySpawnDaemon_SuccessfulSpawnCleansUpItsOwnLock covers the ordinary
// path all the way through: lock acquired, process started and detached,
// lock cleaned up -- so a later invocation is never left believing a spawn
// is still in progress when it already finished.
func TestTrySpawnDaemon_SuccessfulSpawnCleansUpItsOwnLock(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	spec := serverSpec{name: "test", bin: "true", daemonArgs: noopDaemonArgs}

	trySpawnDaemon(t.Context(), spec, sockPath)

	if _, err := os.Stat(sockPath + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock file = %v; want removed once the spawn completed", err)
	}
	if _, err := os.Stat(pidPath(sockPath)); err != nil {
		t.Errorf("pidfile = %v; want written for the spawned daemon", err)
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
	// cannot Parallel because t.Setenv("PATH", ...) below
	binDir := t.TempDir()
	fakeBin := filepath.Join(binDir, "not-a-real-executable")
	if err := os.WriteFile(fakeBin, []byte("this is not an executable\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	spec := serverSpec{name: "test", bin: "not-a-real-executable", daemonArgs: noopDaemonArgs}

	trySpawnDaemon(t.Context(), spec, sockPath)

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
	t.Parallel()
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
	if err := os.WriteFile(path, src, 0o600); err != nil {
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

// --- the managed socket directory must be trusted, not assumed ---

// TestPrivateSocketDir_CreatesPrivateDirectory covers the ordinary case: a
// fresh, writable base directory gets a 0700 UID-scoped subdirectory
// created under it, and a second call against the same base is idempotent
// (returns the same path, still trusted).
func TestPrivateSocketDir_CreatesPrivateDirectory(t *testing.T) {
	// cannot Parallel because t.Setenv("XDG_RUNTIME_DIR", ...) below
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	dir, ok := privateSocketDir()
	if !ok {
		t.Fatal("privateSocketDir() ok = false; want true for a fresh, owned base directory")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("privateSocketDir() did not create a directory")
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("privateSocketDir() mode = %o; want 0700", perm)
	}

	dir2, ok2 := privateSocketDir()
	if !ok2 || dir2 != dir {
		t.Errorf("privateSocketDir() second call = (%q, %v); want (%q, true)", dir2, ok2, dir)
	}
}

// TestRuntimeDir_EmptyXDGFallsBackToTempDir covers runtimeDir's os.TempDir()
// fallback branch: every other case in this file sets XDG_RUNTIME_DIR to its
// own temp dir, so that branch never actually runs otherwise. An empty
// (unset) XDG_RUNTIME_DIR must resolve the managed socket directory under
// the same place os.TempDir() reports, not silently build every socket path
// relative to "" (the process's own current directory).
//
// This exercises the real, shared os.TempDir() rather than an isolated
// t.TempDir() -- the one thing this branch is actually for -- so the
// directory it creates there is removed afterward rather than left behind.
func TestRuntimeDir_EmptyXDGFallsBackToTempDir(t *testing.T) {
	// cannot Parallel because t.Setenv("XDG_RUNTIME_DIR", ...) below
	t.Setenv("XDG_RUNTIME_DIR", "")

	dir, ok := privateSocketDir()
	if !ok {
		t.Fatal("privateSocketDir() ok = false; want true under the os.TempDir() fallback")
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	if got, want := filepath.Dir(dir), filepath.Clean(os.TempDir()); got != want {
		t.Errorf("privateSocketDir() parent = %q; want os.TempDir() %q", got, want)
	}
}

// TestPrivateSocketDir_RejectsLoosePermissions covers the case a predictable
// path in a shared directory exists for: someone (or something) already
// created the expected path with group/other permissions. rgit must not
// trust it merely because it is a directory it owns: permission bits alone
// are not enough on their own to rule out a planted path, but a directory
// this loose is rejected before ownership even needs checking.
func TestPrivateSocketDir_RejectsLoosePermissions(t *testing.T) {
	// cannot Parallel because t.Setenv("XDG_RUNTIME_DIR", ...) below
	base := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", base)
	dir := filepath.Join(base, fmt.Sprintf("rgit-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	if _, ok := privateSocketDir(); ok {
		t.Error("privateSocketDir() ok = true for a group/other-accessible directory; want false")
	}
}

// TestPrivateSocketDir_RejectsSymlink covers a symlink planted at the exact
// expected path: it must be rejected outright, never followed, regardless
// of what it points at or that path's own permissions.
func TestPrivateSocketDir_RejectsSymlink(t *testing.T) {
	// cannot Parallel because t.Setenv("XDG_RUNTIME_DIR", ...) below
	base := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", base)
	dir := filepath.Join(base, fmt.Sprintf("rgit-%d", os.Getuid()))
	realDir := filepath.Join(base, "elsewhere")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, dir); err != nil {
		t.Fatal(err)
	}

	if _, ok := privateSocketDir(); ok {
		t.Error("privateSocketDir() ok = true for a symlink at the expected path; want false")
	}
}

// TestPrivateSocketDir_RejectsNonDirectory covers a plain file occupying the
// expected path.
func TestPrivateSocketDir_RejectsNonDirectory(t *testing.T) {
	// cannot Parallel because t.Setenv("XDG_RUNTIME_DIR", ...) below
	base := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", base)
	dir := filepath.Join(base, fmt.Sprintf("rgit-%d", os.Getuid()))
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := privateSocketDir(); ok {
		t.Error("privateSocketDir() ok = true for a plain file at the expected path; want false")
	}
}

// TestPrivateSocketDir_BaseMissingFailsClosed covers the base directory
// itself being unusable (e.g. $XDG_RUNTIME_DIR pointing nowhere): this must
// degrade the caller to [ts-only] rather than panic or propagate an error
// of its own -- the same "fail closed" posture privateSocketDir uses
// throughout.
func TestPrivateSocketDir_BaseMissingFailsClosed(t *testing.T) {
	// cannot Parallel because t.Setenv("XDG_RUNTIME_DIR", ...) below
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(t.TempDir(), "does-not-exist"))

	if _, ok := privateSocketDir(); ok {
		t.Error("privateSocketDir() ok = true with a missing base directory; want false (fail closed)")
	}
}

// --- a stale/incompatible managed socket must not pin every future
// invocation to [ts-only] forever ---

// TestDialSocket_HandshakeFailureUnlinksManagedSocketAndDegrades covers the
// bug directly: a listener that accepts but never speaks the handshake (a
// stale or incompatible daemon's shape) must be unlinked once this
// invocation gives up on it, so the very next invocation can reclaim the
// path via spawn-on-demand instead of finding the same dead listener
// forever.
func TestDialSocket_HandshakeFailureUnlinksManagedSocketAndDegrades(t *testing.T) {
	// cannot Parallel because t.Setenv("XDG_RUNTIME_DIR", ...) below
	// A unix socket path is capped at ~108 bytes (sun_path) on Linux --
	// t.TempDir() embeds this test's own (long) name in the path, which
	// combined with privateSocketDir's own "rgit-<uid>" subdirectory
	// overflows that limit. A short, manually-cleaned temp dir keeps the
	// path realistic (this is exactly how short $XDG_RUNTIME_DIR normally
	// is, e.g. /run/user/1000).
	base := shortTempDir(t)
	t.Setenv("XDG_RUNTIME_DIR", base)

	spec := serverSpec{name: "test-handshake-fail", bin: "rgit-lsp-test-binary-does-not-exist", daemonArgs: noopDaemonArgs}

	sockPath, ok := defaultSocketPath(spec.name)
	if !ok {
		t.Fatal("defaultSocketPath() ok = false; want true for a fresh, owned temp dir")
	}

	lsptest.Listen(t, sockPath, lsptest.HangUp)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client, degraded := dialSocket(ctx, spec, t.TempDir())
	if client != nil {
		t.Error("dialSocket() client != nil; want nil after a handshake failure")
	}
	if !degraded {
		t.Error("dialSocket() degraded = false; want true after a handshake failure")
	}

	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("managed socket file = %v; want removed after its handshake failed", err)
	}
}

// TestDialSocket_HandshakeFailureLeavesUserSuppliedSocketAlone covers the
// other half of dialSocket's own unlink-on-handshake-failure fix:
// $RGIT_LSP_SOCKET is the caller's own path, not rgit's to manage, so a
// handshake failure against it must never unlink it -- only the managed
// default is rgit's to clean up.
func TestDialSocket_HandshakeFailureLeavesUserSuppliedSocketAlone(t *testing.T) {
	// cannot Parallel because t.Setenv("XDG_RUNTIME_DIR", ...) below
	t.Setenv("XDG_RUNTIME_DIR", shortTempDir(t))

	sockPath := filepath.Join(shortTempDir(t), "user-supplied.sock")
	lsptest.Listen(t, sockPath, lsptest.HangUp)
	t.Setenv("RGIT_LSP_SOCKET", sockPath)

	spec := serverSpec{name: "test-user-socket", bin: "rgit-lsp-test-binary-does-not-exist", daemonArgs: noopDaemonArgs}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, degraded := dialSocket(ctx, spec, t.TempDir()); !degraded {
		t.Error("dialSocket() degraded = false; want true")
	}

	if _, err := os.Stat(sockPath); err != nil {
		t.Errorf("user-supplied socket file = %v; want left in place, not rgit's to remove", err)
	}
}

// --- daemon recovery gaps ---

// TestUnlinkDeadSocket_RemovesDeadSocketFile covers the recovery case
// directly: a unix socket special file left behind by a killed daemon
// (SetUnlinkOnClose(false) simulates exactly that -- an ordinary Close
// would already unlink it, masking the case this function exists for)
// must be removed once nothing answers a connection attempt against it,
// or a freshly spawned daemon's own net.Listen on the same path fails
// EADDRINUSE and spawn-on-demand never recovers.
func TestUnlinkDeadSocket_RemovesDeadSocketFile(t *testing.T) {
	// cannot Parallel: asserts a real connect() against a just-closed unix
	// listener fails fast enough to fall inside unlinkDeadSocket's
	// dialBudget window (150ms). Verified flaky under -count=1 alongside
	// this package's other now-parallel cases (~1 in 15 runs): heavier
	// concurrent scheduling load widens the gap between ln.Close() and the
	// dial attempt enough for a stray connect to land inside it. Running
	// serially keeps that window narrow and the assertion deterministic.
	sockPath := filepath.Join(shortTempDir(t), "rgit-test.sock")
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	if unixLn, ok := ln.(*net.UnixListener); ok {
		unixLn.SetUnlinkOnClose(false)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	unlinkDeadSocket(t.Context(), sockPath)

	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("socket file = %v; want removed once nothing answers it", err)
	}
}

// TestUnlinkDeadSocket_LeavesLiveSocketAlone is the other side: a socket a
// live daemon is actually listening on must never be unlinked out from
// under it.
func TestUnlinkDeadSocket_LeavesLiveSocketAlone(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(shortTempDir(t), "rgit-test.sock")
	lsptest.Listen(t, sockPath, lsptest.HangUp)

	unlinkDeadSocket(t.Context(), sockPath)

	if _, err := os.Stat(sockPath); err != nil {
		t.Errorf("socket file = %v; want left in place -- something is listening", err)
	}
}

// TestTrySpawnDaemon_StaleLockRetriesAndSpawns covers the retry-and-spawn
// path directly: clearing a stale lock must retry the O_EXCL claim once in
// the same invocation and actually spawn, rather than leaving the spawn to
// whatever invocation happens to run next. The fake "daemon" is a real,
// installed shell script so cmd.Start truly execs and runs it -- proven by
// the marker file it touches -- rather than merely asserting the lock
// file's own end-state, which looks identical whether or not a spawn
// actually happened.
func TestTrySpawnDaemon_StaleLockRetriesAndSpawns(t *testing.T) {
	// cannot Parallel because t.Setenv("PATH", ...) below
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "spawned")
	fakeBin := filepath.Join(binDir, "rgit-test-marker-bin")
	script := "#!/bin/sh\ntouch \"" + marker + "\"\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	writeStaleLock(t, sockPath)

	spec := serverSpec{name: "test", bin: "rgit-test-marker-bin", daemonArgs: noopDaemonArgs}
	trySpawnDaemon(t.Context(), spec, sockPath)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("marker file never appeared; want the stale-lock retry to have spawned the daemon")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
