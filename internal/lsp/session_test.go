package lsp

import (
	"context"
	"testing"
)

// TestSession_NilDialDegradesWithoutDialing pins finding 33: a nil *Session
// must degrade directly rather than falling through to the package-level
// Dial. internal/diff/run.go's only production use of a nil Session (a
// revision-to-revision diff, which has no worktree for a language server to
// view) already guards every call site on nil before ever reaching this
// method -- so if this fell through to Dial and Dial actually reached a
// live server, the returned Client would be owned by nobody able to Close
// it. "go" is dialled here specifically because it is the one language with
// a real spawn-on-demand path (dialSocket) that could otherwise leak a
// daemon; if this regresses to the old fallthrough, this test's own nil
// Session would successfully dial (or spawn) it.
func TestSession_NilDialDegradesWithoutDialing(t *testing.T) {
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
	var nilSess *Session
	nilSess.Close() // must not panic

	sess := NewSession()
	sess.Close() // nothing cached; must not panic
}
