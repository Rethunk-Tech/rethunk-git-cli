package gitx_test

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/app"
	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gitx"
)

// An index commit (rgit commit with no targets) must wait for a --only commit
// to finish: landing between git's ref update and the --only commit's index
// sync would commit the real index's stale a.txt and revert it. The sibling
// is cancelled once it announces on stderr that it is waiting for the lock.
func TestIndexCommitWaitsForOnlyCommit(t *testing.T) {
	dir, _ := gittest.RepoWithFile(t.Context(), t, "a.txt", "a\n", "chore: initial")
	gittest.Write(t, dir, "b.txt", "b\n")
	gittest.Git(t.Context(), t, dir, "add", "b.txt")
	gittest.Write(t, dir, "a.txt", "a changed\n")
	t.Chdir(dir)

	defer gitx.SetAfterOnlyGitCommit(func() {
		pr, pw, err := os.Pipe()
		if err != nil {
			t.Error(err)
			return
		}
		prev := os.Stderr
		os.Stderr = pw
		defer func() {
			os.Stderr = prev
			_ = pw.Close()
			_ = pr.Close()
		}()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan int, 1)
		go func() {
			var out, errOut strings.Builder
			done <- int(app.Run(ctx, "test", []string{"commit", "--allow-empty", "-m", "chore: sibling index commit"}, &out, &errOut))
		}()
		waiting := make(chan struct{})
		go func() {
			if _, err := bufio.NewReader(pr).ReadString('\n'); err == nil {
				close(waiting)
			}
		}()
		select {
		case <-waiting:
			cancel()
			<-done
		case <-done:
		}
	})()

	var out, errOut strings.Builder
	if code := app.Run(t.Context(), "test", []string{"commit", "--only", "-m", "chore: change a", "a.txt"}, &out, &errOut); code != 0 {
		t.Fatalf("--only commit exit %d: %s", code, errOut.String())
	}
	if got := gittest.Git(t.Context(), t, dir, "show", "HEAD:a.txt"); !strings.Contains(got, "changed") {
		t.Errorf("HEAD:a.txt = %q; a sibling index commit reverted the --only commit", got)
	}
	if got := gittest.Git(t.Context(), t, dir, "log", "--format=%s"); strings.Contains(got, "sibling") {
		t.Errorf("sibling index commit landed during the --only commit:\n%s", got)
	}
}
