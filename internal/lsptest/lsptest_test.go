package lsptest

import (
	"bufio"
	"fmt"
	"strings"
	"testing"
)

// TestReadFrame_HappyPath covers the ordinary decode: WriteFrame's own
// output, framed back through ReadFrame, round-trips the message it wrote.
func TestReadFrame_HappyPath(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	if err := WriteFrame(&buf, map[string]any{"jsonrpc": "2.0", "id": float64(1), "method": "initialize"}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	msg, ok, err := ReadFrame(bufio.NewReader(strings.NewReader(buf.String())))
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !ok {
		t.Fatal("ReadFrame() ok = false; want true for a well-formed frame")
	}
	if msg["method"] != "initialize" {
		t.Errorf("ReadFrame() method = %v; want %q", msg["method"], "initialize")
	}
}

// TestReadFrame_EdgeCases pins the boundary this package exists to get
// right: a clean EOF before any header byte is read (ok=false, err=nil) is
// the normal shutdown path both mock servers rely on to exit their read
// loops -- but every other way a frame can go wrong, including dying
// partway through its own header, must surface as a real error rather than
// the same silent ok=false. Before this fix, ReadFrame's header loop
// returned the clean-shutdown shape for any read error at all, so a mock
// server dying mid-frame was indistinguishable from an orderly one.
func TestReadFrame_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantOK  bool
		wantErr bool
	}{
		{
			name:    "clean EOF before any header byte",
			input:   "",
			wantOK:  false,
			wantErr: false,
		},
		{
			name:    "bad Content-Length value",
			input:   "Content-Length: abc\r\n\r\n",
			wantOK:  false,
			wantErr: true,
		},
		{
			name:    "body shorter than the announced Content-Length",
			input:   "Content-Length: 10\r\n\r\n{}",
			wantOK:  false,
			wantErr: true,
		},
		{
			name: "truncated header: EOF after a complete Content-Length line " +
				"but before the terminating blank line",
			input:   "Content-Length: 13\r\n",
			wantOK:  false,
			wantErr: true,
		},
		{
			// "-1" parses cleanly via strconv.Atoi, which would otherwise
			// collide with length's own -1 sentinel for "no header seen at
			// all" and misreport this malformed header as a missing one.
			name:    "negative Content-Length",
			input:   "Content-Length: -1\r\n\r\n",
			wantOK:  false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, ok, err := ReadFrame(bufio.NewReader(strings.NewReader(tt.input)))
			if ok != tt.wantOK {
				t.Errorf("ReadFrame() ok = %v; want %v", ok, tt.wantOK)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("ReadFrame() err = %v; want error presence = %v", err, tt.wantErr)
			}
		})
	}
}

// TestReadFrame_ContentLengthOverLimitIsRejected guards maxFrameBody: a mock
// server double claiming an enormous body size must fail the read instead of
// letting make([]byte, length) try to allocate it, since this package trusts
// a test-only stand-in's own header for how much to allocate up front.
func TestReadFrame_ContentLengthOverLimitIsRejected(t *testing.T) {
	t.Parallel()

	input := fmt.Sprintf("Content-Length: %d\r\n\r\n", maxFrameBody+1)
	_, ok, err := ReadFrame(bufio.NewReader(strings.NewReader(input)))
	if ok {
		t.Error("ReadFrame() ok = true; want false for a Content-Length over the test limit")
	}
	if err == nil {
		t.Error("ReadFrame() err = nil; want an error rejecting the oversized Content-Length")
	}
}
