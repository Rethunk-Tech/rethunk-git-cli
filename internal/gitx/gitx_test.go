// Package gitx_test is an external test package (rather than the whitebox
// "package gitx" the rest of this package's tests could use) so it can
// build its fixtures through internal/gittest: gittest itself imports gitx,
// and a whitebox test file importing gittest back would be a cycle.
package gitx_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

func TestLsFilesStageAndMergeBase(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	gittest.Write(t, dir, "file.txt", "content")

	// LsFilesStage for untracked file
	_, found, err := repo.LsFilesStage(ctx, "file.txt")
	if err != nil {
		t.Fatalf("LsFilesStage error: %v", err)
	}
	if found {
		t.Errorf("expected untracked file not to be found in stage")
	}

	// Add file and check LsFilesStage
	gittest.Git(t, dir, "add", "file.txt")
	mode, found, err := repo.LsFilesStage(ctx, "file.txt")
	if err != nil {
		t.Fatalf("LsFilesStage error: %v", err)
	}
	if !found || mode == "" {
		t.Errorf("expected tracked file to be found in stage, got mode=%q found=%v", mode, found)
	}

	// Commit initial commit for MergeBase testing
	gittest.Commit(t, dir, "initial")
	branch1SHA, _, err := repo.RevParseVerify(ctx, "HEAD")
	if err != nil {
		t.Fatalf("RevParseVerify error: %v", err)
	}

	// Create branch feature
	gittest.Git(t, dir, "checkout", "-b", "feature")
	gittest.Write(t, dir, "file.txt", "feature content")
	gittest.Git(t, dir, "commit", "-am", "feature commit")

	mbSHA, ok, err := repo.MergeBase(ctx, "main", "feature")
	if err != nil || !ok {
		t.Fatalf("MergeBase error: %v ok=%v", err, ok)
	}
	if mbSHA != branch1SHA {
		t.Errorf("MergeBase SHA = %q; want %q", mbSHA, branch1SHA)
	}
}

// TestErrorMessagesNameTheCommand pins what a caller actually reads when
// something goes wrong. Both types are surfaced verbatim by internal/app's
// error mapping, so their text is the whole failure report -- and an
// unasserted message is how it can quietly lose the one detail that makes
// it actionable.
func TestErrorMessagesNameTheCommand(t *testing.T) {
	t.Parallel()

	// GitError: git ran and chose a status. The command, the status, and
	// git's own stderr all have to survive, with the stderr trimmed so the
	// message stays one line.
	gerr := &gitx.GitError{
		Args:     []string{"commit", "-m", "x"},
		ExitCode: 128,
		Stderr:   []byte("fatal: nothing to commit\n\n"),
	}
	if got, want := gerr.Error(), "git commit -m x: exit 128: fatal: nothing to commit"; got != want {
		t.Errorf("GitError.Error() = %q; want %q", got, want)
	}

	// ExecError: git never ran. It wraps the cause, so errors.Is/As still
	// reach it -- callers distinguish "git failed" from "git is missing".
	cause := os.ErrNotExist
	eerr := &gitx.ExecError{Args: []string{"rev-parse", "HEAD"}, Err: cause}
	if got, want := eerr.Error(), "git rev-parse HEAD: "+cause.Error(); got != want {
		t.Errorf("ExecError.Error() = %q; want %q", got, want)
	}
	if !errors.Is(eerr, cause) {
		t.Error("ExecError does not unwrap to its cause")
	}
}
