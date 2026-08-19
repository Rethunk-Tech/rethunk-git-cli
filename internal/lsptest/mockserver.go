package lsptest

import (
	"bufio"
	"encoding/json"
	"io"
)

// MockServerHooks lets a caller of ServeMockLSP observe specific messages in
// the wire loop without forking the loop itself. Every hook is optional and
// called synchronously from ServeMockLSP's own goroutine, in message order --
// a nil hook simply means that message carries no observation for this
// caller, not that it goes unhandled: the loop's protocol behaviour (what
// gets a reply, what ends the exchange) is identical whether or not a hook
// is set.
type MockServerHooks struct {
	// OnDidOpen is called with the full decoded message for each
	// "textDocument/didOpen" notification.
	OnDidOpen func(msg map[string]any)
	// OnDidClose is called with the full decoded message for the
	// "textDocument/didClose" notification that ends the exchange --
	// before ServeMockLSP returns, so a hook that records something from
	// it (e.g. the closed URI) is guaranteed to have run by the time the
	// caller's errCh receives ServeMockLSP's own return value.
	OnDidClose func(msg map[string]any)
}

// ServeMockLSP serves one initialize/initialized/didOpen/documentSymbol/
// didClose exchange over conn, answering documentSymbol with resultJSON
// verbatim -- the raw union payload under test -- and returning on the
// didClose that ends it. It never calls a *testing.T method: it is meant to
// run on its own goroutine, and only Fatal-family calls are unsafe off the
// test goroutine.
//
// The exchange must be read to completion, not abandoned after the reply
// documentSymbol asks for: net.Pipe is unbuffered and synchronous, so a
// client write with nobody left reading blocks forever. internal/lsp's own
// DocumentSymbols sends didClose after every didOpen, so returning at
// documentSymbol deadlocks the client mid-teardown rather than ending the
// conversation.
//
// This is a hand-rolled double, not the real go.lsp.dev/jsonrpc2 framing a
// live server actually speaks over — CONTRIBUTING.md's own caution about a
// double "encoding what its author believed the dependency did and then
// stopping tracking it" applies here as much as anywhere else in this
// codebase. What keeps it honest is TestResolve_CrossCheckLiveGopls
// (cmd/rgit/resolver_test.go): the one case that skips this loop entirely
// and drives a real gopls instead, so a real language server's wire
// behaviour drifting out from under this mock's assumptions still has a
// path to be caught, rather than this double quietly becoming the only
// definition of "correct" either caller checks against.
//
// One gap this loop cannot model at all: a
// server-initiated request, e.g. workspace/configuration, which
// internal/lsp/client.go's configClient exists to answer for taplo. This is
// a client speaking to a server it drives, not server-side jsonrpc2 --
// nothing here reads an outbound request from conn and waits on this
// client's own reply the way a real server would, so no test can exercise
// configClient.Configuration through this double no matter what is added to
// the switch above. Extend this loop only if taplo is ever wired into the
// servers map for real; until then, configClient's live counterpart is
// TestResolve_CrossCheckLiveGopls's same real-server boundary, not this
// mock, and even that test would need taplo installed and wired to reach it.
func ServeMockLSP(conn io.ReadWriteCloser, resultJSON string, hooks MockServerHooks) error {
	r := bufio.NewReader(conn)
	for {
		msg, ok, err := ReadFrame(r)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}

		method, _ := msg["method"].(string)
		id, hasID := msg["id"]

		switch method {
		case "textDocument/didOpen":
			if hooks.OnDidOpen != nil {
				hooks.OnDidOpen(msg)
			}
			// Notifications get no reply.
		case "textDocument/didClose":
			if hooks.OnDidClose != nil {
				hooks.OnDidClose(msg)
			}
			// Nothing else is expected after the close that ends this
			// exchange; returning here (rather than looping to the next,
			// absent frame) lets the caller's own conn teardown produce a
			// clean EOF instead of a read error racing it.
			return nil
		case "textDocument/documentSymbol":
			if err := WriteFrame(conn, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  json.RawMessage(resultJSON),
			}); err != nil {
				return err
			}
		case "initialize":
			if err := WriteFrame(conn, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"capabilities": map[string]any{}},
			}); err != nil {
				return err
			}
		default:
			if hasID {
				if err := WriteFrame(conn, map[string]any{"jsonrpc": "2.0", "id": id, "result": nil}); err != nil {
					return err
				}
			}
			// Notifications (initialized) get no reply.
		}
	}
}
