package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

// TestRunCommit_CountingWarningsReachStderr proves synth.Plan's
// CountingWarnings actually reaches a caller now that commit.go reads it,
// rather than staying dead code. An untracked file on an unborn branch is
// a real, reproducible case where `git diff --numstat HEAD` genuinely
// fails -- there is no HEAD yet -- which is exactly the "line counts
// could not be fully computed" case CountingWarnings exists for, not a
// fake or injected error.
func TestRunCommit_CountingWarningsReachStderr(t *testing.T) {
	dir, _ := gittest.New(t)
	gittest.Write(t, dir, "new.txt", "hello\n")
	t.Chdir(dir)

	var stdout, stderr strings.Builder
	code := runCommit(context.Background(), "", []string{"-m", "chore: add new.txt", "--dry-run", "new.txt"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runCommit --dry-run = %v; stdout: %s; stderr: %s", code, stdout.String(), stderr.String())
	}

	got := stderr.String()
	if !strings.Contains(got, "[warning]") || !strings.Contains(got, "tracked line counts unavailable") {
		t.Errorf("stderr = %q; want a [warning] line about unavailable tracked line counts", got)
	}
}

// TestCommitTargets_UnhandledKindErrorsRatherThanSkips pins n10: an
// unexpected cli.Classification.Kind must return a named internal error
// rather than silently omitting the target it was classified from.
// Unreachable today through runCommit itself -- classified is always built
// with allowRevisions=false, so only KindPathspec and KindAnchor ever
// reach here -- but a future flip of that flag must fail loudly the
// moment it does, not stage a partial commit with nothing on stderr to
// say why.
func TestCommitTargets_UnhandledKindErrorsRatherThanSkips(t *testing.T) {
	_, err := commitTargets("/repo", "", []cli.Classification{{Kind: cli.KindRevision, Revision: "HEAD"}}, nil, nil)
	if err == nil {
		t.Fatal("commitTargets: want an error for an unhandled classification kind, got nil")
	}
	if !strings.Contains(err.Error(), "unhandled classification kind") {
		t.Errorf("commitTargets error = %q; want it to name the unhandled kind", err.Error())
	}
}
