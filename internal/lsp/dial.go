package lsp

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// staleLockAge is how old a spawn lock must be before it is treated as
// abandoned. A daemon spawn completes in well under a second; anything
// this old belongs to a process that died before its deferred cleanup.
const staleLockAge = time.Minute

// Dial obtains a Client cross-checking source in lang (a resolve.Language's
// Name(): any key of servers, in servers.go), rooted at repoRoot.
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

// dialSocket implements the probe/spawn sequence:
// try $RGIT_LSP_SOCKET, then the default socket path, each within
// dialBudget; if neither answers, spawn a daemon for a future invocation to
// find and degrade this one to [ts-only] rather than wait for it.
func dialSocket(ctx context.Context, spec serverSpec, repoRoot string) (*Client, bool) {
	sockPath, managedOK := defaultSocketPath(spec.name)

	for _, candidate := range socketCandidates(sockPath, managedOK) {
		managed := managedOK && candidate == sockPath

		// Re-verify the managed directory right before dialing into it --
		// defaultSocketPath's own privateSocketDir call happened earlier,
		// possibly after a dialBudget-bounded probe of an earlier
		// candidate. A user-supplied $RGIT_LSP_SOCKET has no directory of
		// rgit's to re-check here; it was never ours to vouch for.
		if managed && !verifyPrivateDir(filepath.Dir(candidate)) {
			continue
		}

		dialer := net.Dialer{Timeout: dialBudget()}
		conn, err := dialer.DialContext(ctx, "unix", candidate)
		if err != nil {
			continue
		}

		handshakeCtx, cancel := context.WithTimeout(ctx, dialBudget()+queryDeadline())
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
				// invocation staying pinned to a dead listener forever.
				//
				// Accepted best-effort gap: unlinking the directory entry
				// does not stop whatever process is still listening on the
				// inode behind it. If that process is a genuinely live
				// (if slow or stuck) gopls rather than a truly dead one, a
				// respawn here binds a fresh inode at the same path and
				// strands the old process, unreachable, until its own
				// -listen.timeout idle shutdown (servers.go's daemonArgs)
				// reclaims it. A handshake failure this deep into
				// dialBudget+queryDeadline is itself strong evidence of a
				// stuck process (a healthy gopls answers in
				// single-digit milliseconds), so this is not
				// treated as a case worth a shutdown RPC or kill-by-pid:
				// no dialled server here exposes either, and there is
				// nothing to key a kill on beyond the socket path itself.
				_ = os.Remove(candidate)
			}
			continue
		}
		return client, false
	}

	// Re-verify here too, immediately before the spawn attempt, for the
	// same reason as the dial above -- trySpawnDaemon itself takes a bare
	// sockPath and does not re-check its directory (its own unit tests
	// deliberately drive it against an arbitrary path, decoupled from
	// privateSocketDir's trust check; folding the check into trySpawnDaemon
	// would force every one of those to also construct a verified private
	// directory just to exercise the lock/spawn logic they actually test).
	if managedOK && verifyPrivateDir(filepath.Dir(sockPath)) {
		trySpawnDaemon(spec, sockPath)
	}
	return nil, true
}

// socketCandidates returns the probe order:
// $RGIT_LSP_SOCKET first (an existing socket the caller points at
// explicitly), then the managed default path -- omitted entirely when
// hasDefault is false, meaning privateSocketDir could not vouch for a
// directory to hold it.
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
// textDocument/didOpen.
func privateSocketDir() (dir string, ok bool) {
	dir = filepath.Join(runtimeDir(), fmt.Sprintf("rgit-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", false
	}
	if !verifyPrivateDir(dir) {
		return "", false
	}
	return dir, true
}

// verifyPrivateDir reports whether dir is still a private, UID-owned,
// non-symlink directory carrying no group/other permission bits -- the
// same check privateSocketDir performs when it first vouches for one.
//
// It exists as its own function so dialSocket and trySpawnDaemon can
// re-run it immediately before they actually dial or spawn into the
// managed default, not only once when privateSocketDir first returned it.
// Those two operations happen later than the original check -- after a
// dialBudget-bounded probe of every other candidate, in dialSocket's
// case -- leaving a TOCTOU window in which the parent directory could in
// principle be rewritten to swap dir out from under a caller that trusted
// an earlier verification. This narrows that window; it does not close
// it, and closing it fully was rejected: Go's net.Dial for a unix socket
// takes a path string rather than a directory-relative descriptor, so an
// openat-style dial would mean hand-rolled syscalls buying real
// protection only where sameOwner is not already a deliberate no-op
// (owner_windows.go), against a swap the environments this matters in (a
// sticky /tmp, a systemd-managed $XDG_RUNTIME_DIR) already refuse.
func verifyPrivateDir(dir string) bool {
	// Lstat, not Stat: a symlink at this exact path -- planted by another
	// user pointing somewhere they control -- must be rejected outright,
	// never followed.
	info, err := os.Lstat(dir)
	if err != nil {
		return false
	}
	return info.Mode().IsDir() && info.Mode().Perm()&0o077 == 0 && sameOwner(info)
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
// (gopls itself survives the bind race unaided; the
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
	// spawn-on-demand would otherwise silently never recover. Only a socket
	// nothing answers is removed -- a live daemon actually listening there
	// is left alone.
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
// whatever invocation happens to run next -- otherwise the
// invocation that notices the stale lock is never the one that benefits
// from clearing it. The lock is an optimization, not correctness: the
// worst a lost race over it costs is one extra
// doomed gopls process and its stderr noise -- so a lock this function
// cannot vouch for (below) is simply left in place and treated as held,
// same as any other stat failure, rather than escalated into a hard error.
//
// Lstat, not Stat, and a regular-file check before trusting ModTime -- the
// same reasoning verifyPrivateDir gives the socket directory itself: this
// path sits in the same world-writable-by-default runtime directory, and
// following a symlink another user planted here would read (and age) a
// file of their choosing instead of the lock rgit itself created, letting
// a crafted target's mtime convince this invocation a live lock is stale
// and worth removing out from under whatever actually holds it.
func acquireSpawnLock(sockPath string) (*os.File, bool) {
	lockPath := sockPath + ".lock"
	for range 2 {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return lock, true
		}
		info, statErr := os.Lstat(lockPath)
		if statErr != nil || !info.Mode().IsRegular() || time.Since(info.ModTime()) <= staleLockAge {
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
	conn, err := (&net.Dialer{Timeout: dialBudget()}).DialContext(context.Background(), "unix", sockPath)
	if err != nil {
		_ = os.Remove(sockPath)
		return
	}
	_ = conn.Close()
}

// dialStdio spawns spec's server fresh: vtsls and pyright have no
// listen-mode daemon, so every query is a new process. The whole
// spawn+handshake is bounded by dialBudget+queryDeadline; a server still indexing when that expires is
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

	handshakeCtx, cancel := context.WithTimeout(ctx, dialBudget()+queryDeadline())
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
	var closeOnce sync.Once
	var closeErr error
	client.closeFn = func() error {
		closeOnce.Do(func() {
			connErr := client.conn.Close()
			<-client.conn.Done()
			waitErr := killAndReap()
			if connErr != nil {
				closeErr = connErr
				return
			}
			closeErr = waitErr
		})
		return closeErr
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
