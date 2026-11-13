package gitx

import (
	"bytes"
	"context"
	"errors"
	"io"
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
	initCmd := exec.CommandContext(t.Context(), "git", "init", "-q", dir) //nolint:gosec // dir is a test temp path passed as argv, never through a shell
	if out, err := initCmd.CombinedOutput(); err != nil {
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

func TestLockCommitsReturnsOnCancel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	initCmd := exec.CommandContext(t.Context(), "git", "init", "-q", dir) //nolint:gosec // dir is a test temp path passed as argv, never through a shell
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	repo := New(dir)
	unlock, err := repo.LockCommits(t.Context())
	if err != nil {
		t.Fatalf("first LockCommits: %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	done := make(chan error, 1)
	go func() {
		_, err := repo.lockCommits(ctx, io.Discard, 2*time.Second)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lockCommits after cancel = %v; want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lockCommits still blocked 1s after cancel")
	}
}
