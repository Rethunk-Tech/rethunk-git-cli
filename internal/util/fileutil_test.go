package util

import "testing"

func TestLooksBinary(t *testing.T) {
	textData := []byte("hello world\nthis is plain text")
	if LooksBinary(textData) {
		t.Errorf("expected false for text data")
	}

	binaryData := []byte("hello\x00world")
	if !LooksBinary(binaryData) {
		t.Errorf("expected true for data containing NUL byte")
	}

	largeText := make([]byte, 10000)
	for i := range largeText {
		largeText[i] = 'a'
	}
	if LooksBinary(largeText) {
		t.Errorf("expected false for large text without NUL byte")
	}

	largeBinaryLate := make([]byte, 10000)
	for i := range largeBinaryLate {
		largeBinaryLate[i] = 'a'
	}
	largeBinaryLate[9000] = 0 // past sample limit
	if LooksBinary(largeBinaryLate) {
		t.Errorf("expected false for NUL byte past 8000 sample limit")
	}
}
