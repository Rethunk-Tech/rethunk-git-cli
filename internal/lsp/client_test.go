package lsp

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"go.lsp.dev/protocol"
)

// countingRWC wraps a net.Conn to count Close calls, so a test can assert
// exactly who closed it rather than merely that it eventually got closed.
type countingRWC struct {
	net.Conn
	closes atomic.Int32
}

func (c *countingRWC) Close() error {
	c.closes.Add(1)
	return c.Conn.Close()
}

// TestNewClient_ClosesConnExactlyOnceOnHandshakeFailure pins the single
// owner of a failed handshake: NewClient closes the connection when
// Initialize/Initialized errors, and closes it exactly once. Callers
// (dial.go's dialSocket and dialStdio) must not close it again on this
// path. An expired context against an unresponsive peer forces Initialize
// to fail fast without depending on any real server.
func TestNewClient_ClosesConnExactlyOnceOnHandshakeFailure(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	rwc := &countingRWC{Conn: clientConn}

	// net.Pipe is unbuffered and synchronous: the client's own Initialize
	// write would block forever with nothing on the other end to read it,
	// masking the handshake-context-expiry path this test wants to
	// exercise. Draining reads (and discarding them, never writing a
	// response) unblocks the write while still starving Initialize of a
	// reply, so it fails via ctx expiry rather than hanging.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := serverConn.Read(buf); err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := NewClient(ctx, rwc, t.TempDir()); err == nil {
		t.Fatal("NewClient against an unresponsive peer with an expired context = nil error; want one")
	}
	if got := rwc.closes.Load(); got != 1 {
		t.Errorf("rwc.Close called %d times; want exactly 1", got)
	}
}

func TestLanguageKindFor(t *testing.T) {
	tests := []struct {
		path   string
		want   protocol.LanguageKind
		wantOK bool
	}{
		{"foo.go", protocol.LanguageKindGo, true},
		{"foo.ts", protocol.LanguageKindTypeScript, true},
		{"foo.mts", protocol.LanguageKindTypeScript, true},
		{"foo.cts", protocol.LanguageKindTypeScript, true},
		{"foo.tsx", protocol.LanguageKindTypeScriptReact, true},
		{"foo.jsx", protocol.LanguageKindJavaScriptReact, true},
		{"foo.js", protocol.LanguageKindJavaScript, true},
		{"foo.mjs", protocol.LanguageKindJavaScript, true},
		{"foo.cjs", protocol.LanguageKindJavaScript, true},
		{"foo.py", protocol.LanguageKindPython, true},
		{"foo.pyi", protocol.LanguageKindPython, true},
		{"foo.sh", protocol.LanguageKindShellScript, true},
		{"foo.bash", protocol.LanguageKindShellScript, true},
		{"foo.yaml", protocol.LanguageKindYAML, true},
		{"foo.yml", protocol.LanguageKindYAML, true},
		{"foo.json", protocol.LanguageKindJSON, true},
		{"foo.css", protocol.LanguageKindCSS, true},
		{"foo.md", protocol.LanguageKindMarkdown, true},
		{"foo.markdown", protocol.LanguageKindMarkdown, true},
		// An extension with no mapping must report ok=false, never a
		// silent default: a default that always "succeeds" would open
		// every unknown file under some other language's server.
		{"foo.unknown", "", false},
		{"foo", "", false},
	}

	for _, tt := range tests {
		got, ok := languageKindFor(tt.path)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("languageKindFor(%q) = (%q, %v); want (%q, %v)", tt.path, got, ok, tt.want, tt.wantOK)
		}
	}
}

// TestFlatten_UnknownShapeIsNotOK pins that a DocumentSymbolResult
// matching neither known union member (here, the nil interface an explicit
// LSP "null" response decodes to -- go.lsp.dev/protocol's own
// unmarshalDocumentSymbolResultValue sets exactly this on a JSON "null")
// must report ok=false, not a silent empty success indistinguishable from
// a file that genuinely has zero symbols. The sealed union
// (isDocumentSymbolResult is unexported to package protocol) means no
// third concrete type can be constructed from here to simulate a future
// protocol variant directly, but it would fall through the same type
// switch to the same default case this covers.
func TestFlatten_UnknownShapeIsNotOK(t *testing.T) {
	var result protocol.DocumentSymbolResult // nil: the "null" response shape
	syms, ok := flatten(result)
	if ok {
		t.Errorf("flatten(nil) ok = true; want false")
	}
	if syms != nil {
		t.Errorf("flatten(nil) syms = %v; want nil", syms)
	}
}

// TestFlatten_RecognizedEmptyIsStillOK is the other side of that rule: a
// recognized shape with genuinely zero symbols (an empty outline, not an
// unrecognized one) still reports ok=true, so a real empty file does not
// error just because this package is strict about shapes it cannot
// account for.
func TestFlatten_RecognizedEmptyIsStillOK(t *testing.T) {
	syms, ok := flatten(protocol.DocumentSymbolSlice{})
	if !ok {
		t.Error("flatten(DocumentSymbolSlice{}) ok = false; want true")
	}
	if len(syms) != 0 {
		t.Errorf("flatten(DocumentSymbolSlice{}) syms = %v; want empty", syms)
	}

	syms, ok = flatten(protocol.SymbolInformationSlice{})
	if !ok {
		t.Error("flatten(SymbolInformationSlice{}) ok = false; want true")
	}
	if len(syms) != 0 {
		t.Errorf("flatten(SymbolInformationSlice{}) syms = %v; want empty", syms)
	}
}

// TestTrimTrailingBlankLines pins the guarantee trimTrailingBlankLines
// exists for: a yaml-language-server range for a nested container
// consistently extends one line past its own last real content, through a
// blank line separating it from a following sibling, and trimming it back
// must not depend on a live server to verify. This is the unit-lane
// guarantee (CONTRIBUTING.md) -- it must fail here, unlike the server-dial
// tests in servers_test.go which need a live one.
func TestTrimTrailingBlankLines(t *testing.T) {
	src := []byte("build:\n  a: 1\n\ntest:\n  b: 2\n")
	// Lines: 0 "build:", 1 "  a: 1", 2 "", 3 "test:", 4 "  b: 2", then a
	// trailing empty element from the final newline at index 5.
	tests := []struct {
		name           string
		start, end     uint32
		wantTrimmedEnd uint32
	}{
		{"trims the single blank separator before a sibling", 0, 2, 1},
		{"stops at a non-blank line immediately", 0, 1, 1},
		{"never trims down to or past its own StartLine", 2, 2, 2},
		{"never trims the file's own final element (EOF, no sibling follows)", 3, 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syms := []Symbol{{Name: "x", StartLine: tt.start, EndLine: tt.end}}
			trimTrailingBlankLines(src, syms)
			if syms[0].EndLine != tt.wantTrimmedEnd {
				t.Errorf("EndLine = %d; want %d", syms[0].EndLine, tt.wantTrimmedEnd)
			}
		})
	}
}
