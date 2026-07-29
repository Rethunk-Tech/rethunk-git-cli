package lsp

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// staleLockAge is how old a spawn lock must be before it is treated as
// abandoned. A daemon spawn completes in well under a second; anything
// this old belongs to a process that died before its deferred cleanup.
const staleLockAge = time.Minute

// Dial obtains a Client cross-checking source in lang (a resolve.Language's
// Name(): "go", "typescript", "tsx", or "python"), rooted at repoRoot.
//
// degraded=true (with client=nil) means no live, timely server was reached
// and the caller proceeds in [ts-only] mode. That is the normal case, not a
// failure: no daemon, a cold index, an unsupported language, or a stdio
// server still starting up within budget all degrade rather than abort.
func Dial(ctx context.Context, lang, repoRoot string) (client *Client, degraded bool) {
	spec, ok := servers[lang]
	if !ok {
		return nil, true
	}

	if spec.transport == transportSocket {
		return dialSocket(ctx, spec, repoRoot)
	}
	return dialStdio(ctx, spec, repoRoot)
}

// dialSocket implements the probe/spawn sequence from specs/design.md:
// try $RGIT_LSP_SOCKET, then the default socket path, each within
// DialBudget; if neither answers, spawn a daemon for a future invocation to
// find and degrade this one to [ts-only] rather than wait for it.
func dialSocket(ctx context.Context, spec serverSpec, repoRoot string) (*Client, bool) {
	sockPath, managedOK := defaultSocketPath(spec.name)

	for _, candidate := range socketCandidates(sockPath, managedOK) {
		managed := managedOK && candidate == sockPath

		dialer := net.Dialer{Timeout: DialBudget}
		conn, err := dialer.DialContext(ctx, "unix", candidate)
		if err != nil {
			continue
		}

		handshakeCtx, cancel := context.WithTimeout(ctx, DialBudget+QueryDeadline)
		client, err := NewClient(handshakeCtx, conn, repoRoot)
		cancel()
		if err != nil {
			// A socket answered but the handshake failed -- a stale or
			// incompatible daemon. Degrade rather than hard-fail: nothing
			// about this is the caller's problem to fix mid-commit.
			// NewClient owns close-on-handshake-failure (client.go: its
			// own Initialize/Initialized error paths already close the
			// jsonrpc2.Conn it wraps around conn, which in turn closes
			// conn itself) -- closing conn again here would double-close
			// the same net.Conn.
			if managed {
				// Only a path rgit itself manages is safe to unlink -- a
				// user-supplied $RGIT_LSP_SOCKET is the caller's own to
				// clean up, not ours. Removing it lets the next candidate
				// in this same loop, or trySpawnDaemon below if this was
				// the last one, reclaim the path instead of every future
				// invocation staying pinned to a dead listener forever
				// (finding 2).
				_ = os.Remove(candidate)
			}
			continue
		}
		return client, false
	}

	if managedOK {
		trySpawnDaemon(spec, sockPath)
	}
	return nil, true
}

// socketCandidates returns the probe order from specs/design.md:
// $RGIT_LSP_SOCKET first (an existing socket the caller points at
// explicitly), then the managed default path -- omitted entirely when
// hasDefault is false, meaning privateSocketDir could not vouch for a
// directory to hold it (finding 1).
func socketCandidates(defaultPath string, hasDefault bool) []string {
	candidates := make([]string, 0, 2)
	if v := os.Getenv("RGIT_LSP_SOCKET"); v != "" {
		candidates = append(candidates, v)
	}
	if hasDefault {
		candidates = append(candidates, defaultPath)
	}
	return candidates
}

// defaultSocketPath returns the managed socket path for serverName inside a
// directory this process can trust. ok=false means no such directory is
// available -- the caller must not dial or spawn into the managed default
// at all, only $RGIT_LSP_SOCKET if the caller supplied one.
func defaultSocketPath(serverName string) (path string, ok bool) {
	dir, ok := privateSocketDir()
	if !ok {
		return "", false
	}
	return filepath.Join(dir, "rgit-"+serverName+".sock"), true
}

// privateSocketDir returns a UID-scoped, 0700 subdirectory of runtimeDir()
// to hold the managed gopls socket and its spawn lock, creating it if
// absent. ok=false means the directory could not be trusted -- owned by
// someone else, not actually a directory, a symlink, or more permissive
// than 0700 -- and the caller must fail closed to [ts-only] rather than
// dial or spawn into a path another user on a multi-user host could have
// pre-created: runtimeDir() typically falls back to a world-writable
// os.TempDir(), and the socket name is otherwise predictable
// (rgit-<server>.sock), so without this check another user could plant
// their own listener there and read every file rgit sends it over
// textDocument/didOpen (finding 1).
func privateSocketDir() (dir string, ok bool) {
	dir = filepath.Join(runtimeDir(), fmt.Sprintf("rgit-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", false
	}

	// Lstat, not Stat: a symlink at this exact path -- planted by another
	// user pointing somewhere they control -- must be rejected outright,
	// never followed.
	info, err := os.Lstat(dir)
	if err != nil {
		return "", false
	}
	if !info.Mode().IsDir() || info.Mode().Perm()&0o077 != 0 || !sameOwner(info) {
		return "", false
	}
	return dir, true
}

func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return os.TempDir()
}

// trySpawnDaemon starts spec's daemon listening at sockPath in the
// background, guarded by an O_EXCL lock beside the socket so a burst of
// concurrent rgit invocations does not each launch a doomed duplicate
// (specs/design.md notes gopls itself survives the bind race unaided; the
// lock exists only to avoid the extra process spawns and their stderr
// noise, not for correctness). This invocation never waits on the daemon
// it just started — the load-bearing rule is that spawning must not block
// the current query.
func trySpawnDaemon(spec serverSpec, sockPath string) {
	if _, err := exec.LookPath(spec.bin); err != nil {
		return
	}

	lock, ok := acquireSpawnLock(sockPath)
	if !ok {
		return
	}
	defer func() {
		// Both are best-effort, in this order: close the descriptor, then
		// unlink. A lock that survives either failure is reclaimed by
		// acquireSpawnLock's own staleLockAge sweep, which is why the lock
		// can be an optimization rather than something correctness rests
		// on.
		_ = lock.Close()
		_ = os.Remove(sockPath + ".lock")
	}()

	// A dead daemon leaves the unix socket special file behind; a fresh
	// gopls's own net.Listen on the same path then fails EADDRINUSE, so
	// spawn-on-demand would otherwise silently never recover (finding
	// 10a). Only a socket nothing answers is removed -- a live daemon
	// actually listening there is left alone.
	unlinkDeadSocket(sockPath)

	cmd := exec.Command(spec.bin, spec.daemonArgs(sockPath)...)
	cmd.Stdin = nil
	// The daemon's own stderr is diagnostic noise about itself, not about
	// this rgit invocation; discard it rather than let it interleave with
	// rgit's output for a process the caller never explicitly started.
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return
	}
	// Detach: the daemon outlives this process by design, so there is
	// nothing here to Wait() on.
	_ = cmd.Process.Release()
}

// acquireSpawnLock claims the O_EXCL lock beside sockPath. A lock already
// held usually means another invocation is mid-spawn; one older than
// staleLockAge instead belongs to a process that died before its own
// deferred cleanup ran. That stale lock is cleared and the claim retried
// once in the same invocation rather than leaving the actual spawn to
// whatever invocation happens to run next (finding 10b) -- otherwise the
// invocation that notices the stale lock is never the one that benefits
// from clearing it. The lock is an optimization, not correctness
// (specs/design.md): the worst a lost race over it costs is one extra
// doomed gopls process and its stderr noise.
func acquireSpawnLock(sockPath string) (*os.File, bool) {
	lockPath := sockPath + ".lock"
	for range 2 {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return lock, true
		}
		info, statErr := os.Stat(lockPath)
		if statErr != nil || time.Since(info.ModTime()) <= staleLockAge {
			return nil, false
		}
		_ = os.Remove(lockPath)
	}
	return nil, false
}

// unlinkDeadSocket removes a leftover unix socket special file at sockPath
// so a freshly spawned daemon's own net.Listen does not fail EADDRINUSE
// against it. It is only ever called from inside trySpawnDaemon's spawn
// lock, so the short dial here costs nothing and keeps this function
// correct standalone rather than relying on some earlier caller having
// already proven the path dead. A live daemon actually listening at
// sockPath answers the dial and is left alone.
func unlinkDeadSocket(sockPath string) {
	if _, err := os.Stat(sockPath); err != nil {
		return
	}
	conn, err := (&net.Dialer{Timeout: DialBudget}).DialContext(context.Background(), "unix", sockPath)
	if err != nil {
		_ = os.Remove(sockPath)
		return
	}
	_ = conn.Close()
}

// dialStdio spawns spec's server fresh: vtsls and pyright have no
// listen-mode daemon (specs/design.md), so every query is a new process.
// The whole spawn+handshake is bounded by
// DialBudget+QueryDeadline; a server still indexing when that expires is
// killed and this invocation degrades rather than waits.
func dialStdio(ctx context.Context, spec serverSpec, repoRoot string) (*Client, bool) {
	if _, err := exec.LookPath(spec.bin); err != nil {
		return nil, true
	}

	// CommandContext, unlike the daemon spawn above: this process is meant
	// to live only as long as the query, so a cancelled context must take
	// it down rather than leave it running on a pipe nobody reads.
	cmd := exec.CommandContext(ctx, spec.bin, spec.stdioArgs...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, true
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, true
	}
	// As in trySpawnDaemon, the server's own stderr is not rgit's to show.
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, true
	}
	// killAndReap is cleanup after the connection is already down, never
	// the first step: Kill on an already-exited process is a harmless
	// no-op (its error is intentionally discarded), and Wait reaps the
	// process so it does not linger as a zombie.
	killAndReap := func() error {
		_ = cmd.Process.Kill()
		return cmd.Wait()
	}

	handshakeCtx, cancel := context.WithTimeout(ctx, DialBudget+QueryDeadline)
	defer cancel()
	client, err := NewClient(handshakeCtx, pipeRWC{stdout, stdin}, repoRoot)
	if err != nil {
		// NewClient already closed the connection (and, through it, the
		// pipes -- client.go's own close-on-error) on this path; only the
		// process itself still needs cleaning up.
		_ = killAndReap()
		return nil, true
	}
	// This is a one-shot subprocess dedicated to this query -- unlike the
	// socket daemon case, nothing will ever reuse it, so closing the
	// client means shutting the whole thing down, not merely
	// disconnecting from a daemon that outlives this process.
	//
	// Order matters: closing the connection first lets jsonrpc2's own
	// read goroutine and pipeRWC.Close's EOF-then-close sequence shut
	// down cleanly and gives the subprocess a chance to exit on its own;
	// killing first races SIGKILL against that same read, which can
	// surface as a spurious protocol error instead of a clean shutdown.
	// killAndReap afterward is insurance, not the primary shutdown path,
	// and reaps whatever the close left behind -- nothing will reuse this
	// process either way, so an already-exited one is not a problem to
	// solve, only a Wait() to perform.
	client.closeFn = func() error {
		connErr := client.conn.Close()
		waitErr := killAndReap()
		if connErr != nil {
			return connErr
		}
		return waitErr
	}
	return client, false
}

// pipeRWC adapts a subprocess's separate stdin/stdout pipes to the single
// io.ReadWriteCloser jsonrpc2.NewStream expects.
type pipeRWC struct {
	io.ReadCloser
	io.WriteCloser
}

func (p pipeRWC) Close() error {
	// Closing stdin first signals EOF to the subprocess so it can exit on
	// its own; closing stdout then unblocks any pending read regardless.
	werr := p.WriteCloser.Close()
	rerr := p.ReadCloser.Close()
	if werr != nil {
		return werr
	}
	return rerr
}
