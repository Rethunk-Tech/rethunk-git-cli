package app

import "testing"

func TestStripControl(t *testing.T) {
	t.Parallel()
	in := "a\x1b]0;x\x07b\r\tc\n\xc2\x9bd\xc3\xa9\x7f"
	if got, want := string(stripControl([]byte(in))), "a]0;xb\tc\ndé"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
