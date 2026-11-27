package lsp

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSweepStdioTmp_RemovesOnlyDeadOwners(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	// A pid beyond pid_max is never alive.
	dead := filepath.Join(tmp, stdioTmpPrefix+"2147483646-123")
	live := filepath.Join(tmp, stdioTmpPrefix+strconv.Itoa(os.Getpid())+"-123")
	legacy := filepath.Join(tmp, stdioTmpPrefix+"123456")
	for _, d := range []string{dead, live, legacy} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sweepStdioTmp()

	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Errorf("dead owner's dir survived the sweep: %v", err)
	}
	for _, d := range []string{live, legacy} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s was removed: %v", d, err)
		}
	}
}

func TestDialStdio_SweepsAbandonedTmp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dead := filepath.Join(tmp, stdioTmpPrefix+"2147483646-123")
	if err := os.Mkdir(dead, 0o700); err != nil {
		t.Fatal(err)
	}
	dialStdio(t.Context(), serverSpec{name: "test", bin: "true"}, t.TempDir())
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Errorf("dialStdio left the dead owner's dir: %v", err)
	}
}
