package lsptest

import (
	"context"
	"net"
	"testing"
)

// Listen opens a unix socket at path and accepts connections until the test
// ends, handing each one to handle on its own goroutine.
//
// A real socket file rather than net.Pipe: the code under test dials a path
// and reasons about the file at it (whether a stale one gets unlinked,
// whether a live one is left alone), so an in-memory pipe cannot stand in.
//
// Closing the listener is what ends the accept loop, and that is registered
// as test cleanup rather than left to the caller -- a leaked loop outlives
// the test that started it and goes on accepting on a path a later test may
// reuse.
func Listen(t testing.TB, path string, handle func(conn net.Conn)) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go handle(conn)
		}
	}()
}

// HangUp is the handler for a listener that accepts and immediately closes
// without ever speaking the handshake -- the shape a stale or incompatible
// daemon leaves behind, which callers must report as degraded rather than
// reachable.
func HangUp(conn net.Conn) { _ = conn.Close() }
