package gitx

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLockCommitsNamesTheHolderWhileWaiting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	repo := New(dir)
	ctx := t.Context()

	unlock, err := repo.LockCommits(ctx)
	if err != nil {
		t.Fatalf("first LockCommits: %v", err)
	}
	var notice bytes.Buffer
	released := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		unlock()
		close(released)
	}()
	second, err := repo.lockCommits(ctx, &notice, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("second lockCommits: %v", err)
	}
	second()
	<-released
	if want := "pid " + strconv.Itoa(os.Getpid()); !strings.Contains(notice.String(), want) {
		t.Errorf("notice = %q; want it to name %q", notice.String(), want)
	}
}
