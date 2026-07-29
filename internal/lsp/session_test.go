package lsp

import (
	"context"
	"net"
	"testing"
)

// TestSession_NilDialDegradesWithoutDialing pins the guarantee: a nil
// *Session must degrade directly rather than falling through to the
// package-level Dial. internal/diff/run.go's only production use of a nil
// Session (a revision-to-revision diff, which has no worktree for a
// language server to view) already guards every call site on nil before
// ever reaching this method -- so if this fell through to Dial and Dial
// actually reached a live server, the returned Client would be owned by
// nobody able to Close it. "go" is dialled here specifically because it is
// the one language with a real spawn-on-demand path (dialSocket) that
// could otherwise leak a daemon; if this regresses to the old fallthrough,
// this test's own nil Session would successfully dial (or spawn) it.
func TestSession_NilDialDegradesWithoutDialing(t *testing.T) {
	t.Parallel()
	var sess *Session

	client, degraded := sess.Dial(context.Background(), "go", t.TempDir())
	if client != nil {
		t.Errorf("nil Session.Dial client = %v; want nil", client)
	}
	if !degraded {
		t.Error("nil Session.Dial degraded = false; want true")
	}
}

// TestSession_CachesClientAndDegradedState covers the guarantee NewSession
// exists for: a language that degrades on its first Dial within one
// invocation must not be redialled on a second call for the same language
// (a server absent for the first anchor has not appeared by the second),
// and an unsupported language name degrades identically to Dial's own
// contract for one.
func TestSession_CachesClientAndDegradedState(t *testing.T) {
	t.Parallel()
	sess := NewSession()

	client1, degraded1 := sess.Dial(context.Background(), "no-such-language", t.TempDir())
	if client1 != nil || !degraded1 {
		t.Fatalf("first Dial = (%v, %v); want (nil, true)", client1, degraded1)
	}
	if !sess.degraded["no-such-language"] {
		t.Error("Session did not remember the degraded language")
	}

	client2, degraded2 := sess.Dial(context.Background(), "no-such-language", t.TempDir())
	if client2 != nil || !degraded2 {
		t.Fatalf("second Dial = (%v, %v); want (nil, true)", client2, degraded2)
	}
}

// TestSession_CloseIsNilSafe covers Close on both a nil Session (callers
// that never dialled anything for a revision-to-revision diff still call
// Close unconditionally in some paths) and a fresh Session with nothing
// cached.
func TestSession_CloseIsNilSafe(t *testing.T) {
	t.Parallel()
	var nilSess *Session
	nilSess.Close() // must not panic

	sess := NewSession()
	sess.Close() // nothing cached; must not panic
}

// TestSession_ReusesCachedClientAndClosesIt covers the two branches
// TestSession_CachesClientAndDegradedState's own degraded-language case does
// not: a language dialled successfully once must be handed back on a second
// Dial for the same language without redialling, and Close must actually
// close every client it has cached. Both are otherwise gated behind an
// installed language server and never run under -short -- Dial itself is
// bypassed here by seeding sess.clients directly (an in-package field, since
// this file is package lsp) with a real *Client over an in-memory pipe, the
// same client_test.go mock (didCloseObserver, countingRWC) the didClose
// coverage already uses, so this proves Session's own caching and teardown
// rather than re-proving NewClient's handshake.
func TestSession_ReusesCachedClientAndClosesIt(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	observer := &didCloseObserver{conn: serverConn}
	errCh := make(chan error, 1)
	go func() { errCh <- observer.serve(`[]`) }()

	rwc := &countingRWC{Conn: clientConn}
	client, err := NewClient(context.Background(), rwc, t.TempDir())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	sess := NewSession()
	sess.clients["go"] = client

	got, degraded := sess.Dial(context.Background(), "go", t.TempDir())
	if degraded {
		t.Fatal("Dial() degraded = true for a cached language; want false")
	}
	if got != client {
		t.Error("Dial() returned a different *Client than the one cached; want the cached one reused, not a redial")
	}

	sess.Close()
	// At least once, not exactly once: unlike
	// TestNewClient_ClosesConnExactlyOnceOnHandshakeFailure's synchronous,
	// single-owner path, closeFn here is NewClient's default (rwc.Close
	// directly, never jsonrpc2.Conn.Close()) -- so this Close races
	// jsonrpc2's own read goroutine, which independently closes the same
	// stream once it observes the read error our Close causes (conn.go's
	// updateInFlight: idle + a non-nil readErr closes s.closer too).
	// Flaky at exactly 1 under -race -count=3 for exactly that reason: what
	// this test actually owns is proving Session.Close reaches the client
	// at all, not arbitrating a lower library's own internal teardown race.
	if closes := rwc.closes.Load(); closes < 1 {
		t.Errorf("cached client closed %d times by Session.Close(); want at least 1", closes)
	}

	if srvErr := <-errCh; srvErr != nil {
		t.Fatalf("mock server: %v", srvErr)
	}
}
