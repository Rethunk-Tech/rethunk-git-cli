// Package lsptest holds the LSP header-framed JSON-RPC codec shared by the
// hand-rolled mock language servers in internal/lsp's own tests and
// cmd/rgit's resolver tests. Both packages need to construct and decode the
// same Content-Length-framed wire format (go.lsp.dev/jsonrpc2's NewStream
// framing) to drive an in-process mock server; without a shared copy the two
// implementations can silently diverge.
//
// It exists as a plain package, not a _test.go helper, for the same reason
// internal/gittest does (see that package's doc comment): Go scopes test
// files to their own package, so two packages that each need this cannot
// share one _test.go copy.
//
// Nothing in the production build imports it.
package lsptest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxFrameBody bounds a single frame's Content-Length. This package is
// test-only -- nothing in the production build imports it -- but a mock
// server double is still a process ReadFrame trusts to report its own body
// size honestly; a buggy or deliberately hostile one claiming an enormous
// length should fail the read, not let make([]byte, length) try to
// allocate it.
const maxFrameBody = 1 << 20

// ReadFrame reads one LSP header-framed JSON-RPC message (Content-Length,
// blank line, JSON body) off r. ok=false at a clean EOF between frames --
// specifically, an io.EOF with no header bytes read yet in this call.
//
// Anything else that goes wrong reading the header -- a non-EOF error, or
// an EOF after at least one header byte has already been consumed (a
// truncated frame: a mock server that died mid-header, or between a
// complete Content-Length line and the blank line that should terminate
// it) -- is a real error, not a second clean-shutdown shape. A test helper
// that swallowed both identically would hide a mock server dying mid-frame
// behind the same "the peer hung up" result an orderly shutdown produces.
func ReadFrame(r *bufio.Reader) (msg map[string]any, ok bool, err error) {
	length := -1
	first := true
	for {
		line, rerr := r.ReadString('\n')
		if rerr != nil {
			if first && len(line) == 0 && errors.Is(rerr, io.EOF) {
				return nil, false, nil //nolint:nilerr // clean EOF before any header byte: the normal shutdown path between frames
			}
			return nil, false, fmt.Errorf("mock lsp server: read header: %w", rerr)
		}
		first = false
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if after, found := strings.CutPrefix(line, "Content-Length:"); found {
			n, convErr := strconv.Atoi(strings.TrimSpace(after))
			if convErr != nil {
				return nil, false, fmt.Errorf("mock lsp server: bad Content-Length %q: %w", line, convErr)
			}
			// A negative header value ("Content-Length: -1") parses cleanly
			// via Atoi and would otherwise collide with length's own -1
			// sentinel for "no Content-Length header seen at all", reporting
			// a malformed header as a missing one instead. Rejected here,
			// distinctly, before it ever reaches that check.
			if n < 0 {
				return nil, false, fmt.Errorf("mock lsp server: negative Content-Length %q", line)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, false, fmt.Errorf("mock lsp server: frame missing Content-Length")
	}
	// A hostile or merely buggy mock server can claim an arbitrarily large
	// Content-Length; capped rather than trusted outright so a bad test
	// double fails loudly instead of running the test process out of
	// memory trying to allocate the body up front.
	if length > maxFrameBody {
		return nil, false, fmt.Errorf("mock lsp server: Content-Length %d exceeds %d-byte test limit", length, maxFrameBody)
	}
	body := make([]byte, length)
	if _, rerr := io.ReadFull(r, body); rerr != nil {
		return nil, false, fmt.Errorf("mock lsp server: read body: %w", rerr)
	}
	if uErr := json.Unmarshal(body, &msg); uErr != nil {
		return nil, false, fmt.Errorf("mock lsp server: decode body: %w", uErr)
	}
	return msg, true, nil
}

// WriteFrame writes msg to w as one LSP header-framed JSON-RPC message.
func WriteFrame(w io.Writer, msg map[string]any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mock lsp server: encode: %w", err)
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}
