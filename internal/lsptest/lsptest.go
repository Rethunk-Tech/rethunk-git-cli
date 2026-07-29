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
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ReadFrame reads one LSP header-framed JSON-RPC message (Content-Length,
// blank line, JSON body) off r. ok=false at a clean EOF.
func ReadFrame(r *bufio.Reader) (msg map[string]any, ok bool, err error) {
	length := -1
	for {
		line, rerr := r.ReadString('\n')
		if rerr != nil {
			return nil, false, nil //nolint:nilerr // EOF between frames is the normal shutdown path
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if after, found := strings.CutPrefix(line, "Content-Length:"); found {
			n, convErr := strconv.Atoi(strings.TrimSpace(after))
			if convErr != nil {
				return nil, false, fmt.Errorf("mock lsp server: bad Content-Length %q: %w", line, convErr)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, false, fmt.Errorf("mock lsp server: frame missing Content-Length")
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
