package lsp

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
)

// Dial obtains a Client cross-checking source in lang (a resolve.Language's
// Name(): "go", "typescript", "tsx", or "python"), rooted at repoRoot.
//
// degraded=true (with client=nil) means no live, timely language server was
// reachable and the caller must proceed in [ts-only] mode rather than treat
// this as an error — specs/design.md is explicit that this is the normal
// case, not a failure: no daemon, a cold index, an unsupported language, or
// (for the two stdio-only servers) simply not having finished starting up
// within budget all degrade rather than abort the invocation. The one
// caller in scope for this deliverable is internal/resolve's
// CrossCheckExtent; see AGENTS.md's Seam protocol for why nothing in
// internal/app calls this yet.
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
	sockPath := defaultSocketPath(spec.name)

	for _, candidate := range socketCandidates(sockPath) {
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
			_ = conn.Close()
			return nil, true
		}
		return client, false
	}

	trySpawnDaemon(spec, sockPath)
	return nil, true
}

// socketCandidates returns the probe order from specs/design.md:
// $RGIT_LSP_SOCKET first (an existing socket the caller points at
// explicitly), then the default path.
func socketCandidates(defaultPath string) []string {
	candidates := make([]string, 0, 2)
	if v := os.Getenv("RGIT_LSP_SOCKET"); v != "" {
		candidates = append(candidates, v)
	}
	return append(candidates, defaultPath)
}

func defaultSocketPath(serverName string) string {
	return filepath.Join(runtimeDir(), "rgit-"+serverName+".sock")
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

	lockPath := sockPath + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return // another invocation is already spawning this daemon
	}
	defer os.Remove(lockPath)
	defer lock.Close()

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

// dialStdio spawns spec's server fresh, as measured necessary in
// specs/design.md: vtsls and pyright have no listen-mode daemon, so every
// query is a new process. The whole spawn+handshake is bounded by
// DialBudget+QueryDeadline; a server still indexing when that expires is
// killed and this invocation degrades rather than waits.
func dialStdio(ctx context.Context, spec serverSpec, repoRoot string) (*Client, bool) {
	if _, err := exec.LookPath(spec.bin); err != nil {
		return nil, true
	}

	cmd := exec.Command(spec.bin, spec.stdioArgs...)
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
	kill := func() error {
		_ = cmd.Process.Kill()
		return cmd.Wait()
	}

	handshakeCtx, cancel := context.WithTimeout(ctx, DialBudget+QueryDeadline)
	defer cancel()
	client, err := NewClient(handshakeCtx, pipeRWC{stdout, stdin}, repoRoot)
	if err != nil {
		_ = kill()
		return nil, true
	}
	// This is a one-shot subprocess dedicated to this query -- unlike the
	// socket daemon case, nothing will ever reuse it, so closing the
	// client means killing it.
	client.closeFn = kill
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
