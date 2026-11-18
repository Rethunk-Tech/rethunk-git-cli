package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckOnlyBaseAcceptsHEADBuiltOnTheSeededCommit(t *testing.T) {
	t.Parallel()
	dir, repo := commitRepo(t)
	writeCommit(t, dir, "a.txt", "one\n", "chore: one")
	base := revParse(t, dir, "HEAD")
	writeCommit(t, dir, "a.txt", "two\n", "chore: two")

	err := repo.checkOnlyBase(t.Context(), CommitOptions{
		Only: &OnlyIndex{BaseCommit: base},
	})
	if err != nil {
		t.Fatalf("checkOnlyBase(parent is base) = %v; want nil", err)
	}
}

func TestCheckOnlyBaseRefusesWhenHEADParentIsNotTheSeededCommit(t *testing.T) {
	t.Parallel()
	dir, repo := commitRepo(t)
	writeCommit(t, dir, "a.txt", "one\n", "chore: one")
	writeCommit(t, dir, "a.txt", "two\n", "chore: two")
	head := revParse(t, dir, "HEAD")

	err := repo.checkOnlyBase(t.Context(), CommitOptions{
		Only: &OnlyIndex{BaseCommit: head},
	})
	if err == nil {
		t.Fatal("checkOnlyBase(base is HEAD) = nil; want HEAD-moved error")
	}
	if !strings.Contains(err.Error(), "HEAD moved from") {
		t.Errorf("checkOnlyBase error = %q; want it to name the moved HEAD", err)
	}
}

func TestCheckOnlyBaseAmendAcceptsWhenParentsStillMatch(t *testing.T) {
	t.Parallel()
	dir, repo := commitRepo(t)
	writeCommit(t, dir, "a.txt", "one\n", "chore: one")
	writeCommit(t, dir, "a.txt", "two\n", "chore: two")
	base := revParse(t, dir, "HEAD")

	err := repo.checkOnlyBase(t.Context(), CommitOptions{
		Amend: true,
		Only:  &OnlyIndex{BaseCommit: base},
	})
	if err != nil {
		t.Fatalf("checkOnlyBase(amend, same parent) = %v; want nil", err)
	}
}

func TestCheckOnlyBaseAmendRefusesWhenAForeignCommitWasReplaced(t *testing.T) {
	t.Parallel()
	dir, repo := commitRepo(t)
	writeCommit(t, dir, "a.txt", "one\n", "chore: one")
	base := revParse(t, dir, "HEAD")
	writeCommit(t, dir, "a.txt", "two\n", "chore: two")

	err := repo.checkOnlyBase(t.Context(), CommitOptions{
		Amend: true,
		Only:  &OnlyIndex{BaseCommit: base},
	})
	if err == nil {
		t.Fatal("checkOnlyBase(amend after a later commit) = nil; want parent mismatch")
	}
	if !strings.Contains(err.Error(), "HEAD moved while amending") {
		t.Errorf("checkOnlyBase error = %q; want it to name the amend mismatch", err)
	}
}

func TestCheckOnlyBaseAmendOfARootCommitAcceptsARootHEAD(t *testing.T) {
	t.Parallel()
	dir, repo := commitRepo(t)
	writeCommit(t, dir, "a.txt", "one\n", "chore: one")
	base := revParse(t, dir, "HEAD")

	err := repo.checkOnlyBase(t.Context(), CommitOptions{
		Amend: true,
		Only:  &OnlyIndex{BaseCommit: base},
	})
	if err != nil {
		t.Fatalf("checkOnlyBase(amend root) = %v; want nil -- neither side has a parent", err)
	}
}

func commitRepo(t *testing.T) (dir string, repo *Repo) {
	t.Helper()
	dir = t.TempDir()
	init := exec.CommandContext(t.Context(), "git", "init", "-q", "-b", "main", dir) //nolint:gosec // dir is a test temp path passed as argv
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	runGit(t, dir, "config", "user.email", "rgit-test@example.com")
	runGit(t, dir, "config", "user.name", "rgit Test")
	return dir, New(dir)
}

func writeCommit(t *testing.T, dir, rel, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", rel)
	runGit(t, dir, "commit", "-q", "-m", msg)
}

func revParse(t *testing.T, dir, rev string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, dir, "rev-parse", "--verify", rev))
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // dir and args are fixture argv, never a shell
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}
