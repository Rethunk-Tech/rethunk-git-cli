package util

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestLooksBinaryFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	textPath := filepath.Join(dir, "text.txt")
	if err := os.WriteFile(textPath, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if binary, err := LooksBinaryFile(textPath); err != nil || binary {
		t.Errorf("LooksBinaryFile(text) = %v, %v; want false, nil", binary, err)
	}

	binPath := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(binPath, []byte("a\x00b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if binary, err := LooksBinaryFile(binPath); err != nil || !binary {
		t.Errorf("LooksBinaryFile(binary) = %v, %v; want true, nil", binary, err)
	}

	// A file larger than BinarySampleLimit, with its only NUL byte past the
	// sample window, must read as text -- otherwise LooksBinaryFile is
	// pointlessly inspecting bytes past the sample it claims to bound.
	largePath := filepath.Join(dir, "large.txt")
	large := make([]byte, BinarySampleLimit+1000)
	for i := range large {
		large[i] = 'a'
	}
	large[BinarySampleLimit+500] = 0
	if err := os.WriteFile(largePath, large, 0o644); err != nil {
		t.Fatal(err)
	}
	if binary, err := LooksBinaryFile(largePath); err != nil || binary {
		t.Errorf("LooksBinaryFile(large, NUL past limit) = %v, %v; want false, nil", binary, err)
	}

	if _, err := LooksBinaryFile(filepath.Join(dir, "missing")); err == nil {
		t.Error("LooksBinaryFile(missing) = nil error; want a real error")
	}
}
