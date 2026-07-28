package gitx

import (
	"context"
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
