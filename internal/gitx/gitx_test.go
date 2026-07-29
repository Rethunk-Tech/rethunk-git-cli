package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLsFilesStageAndMergeBase(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	runCmd := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	runCmd("init")
	runCmd("checkout", "-B", "main")
	runCmd("config", "user.name", "test")
	runCmd("config", "user.email", "test@example.com")

	filePath := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := New(dir)

	// LsFilesStage for untracked file
	_, found, err := repo.LsFilesStage(ctx, "file.txt")
	if err != nil {
		t.Fatalf("LsFilesStage error: %v", err)
	}
	if found {
		t.Errorf("expected untracked file not to be found in stage")
	}

	// Add file and check LsFilesStage
	runCmd("add", "file.txt")
	mode, found, err := repo.LsFilesStage(ctx, "file.txt")
	if err != nil {
		t.Fatalf("LsFilesStage error: %v", err)
	}
	if !found || mode == "" {
		t.Errorf("expected tracked file to be found in stage, got mode=%q found=%v", mode, found)
	}

	// Commit initial commit for MergeBase testing
	runCmd("commit", "-m", "initial")
	branch1SHA, _, err := repo.RevParseVerify(ctx, "HEAD")
	if err != nil {
		t.Fatalf("RevParseVerify error: %v", err)
	}

	// Create branch feature
	runCmd("checkout", "-b", "feature")
	if err := os.WriteFile(filePath, []byte("feature content"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd("commit", "-am", "feature commit")

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
	gerr := &GitError{
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
	eerr := &ExecError{Args: []string{"rev-parse", "HEAD"}, Err: cause}
	if got, want := eerr.Error(), "git rev-parse HEAD: "+cause.Error(); got != want {
		t.Errorf("ExecError.Error() = %q; want %q", got, want)
	}
	if !errors.Is(eerr, cause) {
		t.Error("ExecError does not unwrap to its cause")
	}
}
