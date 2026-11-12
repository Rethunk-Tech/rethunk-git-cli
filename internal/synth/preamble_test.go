package synth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gittest"
)

// TestPlanStage_NewFileCarriesHeaderAndImports pins the new-file preamble:
// naming one symbol in a file HEAD has never seen must still land a blob with
// its package clause and import block, or the commit is a bare function body
// that cannot compile. It also pins that each region gets its own result row,
// so a --dry-run preview accounts for the bytes it is about to add.
func TestPlanStage_NewFileCarriesHeaderAndImports(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()
	// A committed sibling gives HEAD a tree, so the new file is an addition
	// rather than the repository's root commit.
	gittest.Write(t, dir, "existing.go", "package sample\n")
	gittest.Commit(ctx, t, dir, "chore: initial")

	const src = "// Package sample is new.\npackage sample\n\nimport \"fmt\"\n\nfunc New() {\n\tfmt.Println()\n}\n\nfunc Other() {}\n"
	gittest.Write(t, dir, "new.go", src)

	plan, err := PlanStage(ctx, repo, dir, []Target{AnchorTarget("new.go", "New")})
	if err != nil {
		t.Fatalf("PlanStage: %v", err)
	}
	if len(plan.Preamble) != 1 || plan.Preamble[0] != "new.go" {
		t.Errorf("Preamble = %v; want [new.go]", plan.Preamble)
	}

	staged := map[string]bool{}
	for _, res := range plan.Results {
		if res.Target.Symbol.Anchor == "@header" || res.Target.Symbol.Anchor == "@imports" {
			staged[res.Target.Symbol.Anchor] = true
		}
	}
	for _, anchor := range []string{"@header", "@imports"} {
		if !staged[anchor] {
			t.Errorf("plan.Results has no %s row; want the preamble accounted for", anchor)
		}
	}

	if err := plan.Apply(ctx, repo, dir); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	blob := gittest.Git(ctx, t, dir, "show", ":new.go")
	for _, want := range []string{"package sample", "import \"fmt\"", "func New()"} {
		if !strings.Contains(blob, want) {
			t.Errorf("staged new.go = %q; want it to carry %q", blob, want)
		}
	}
	// Naming one symbol stages that symbol, not the whole new file: the
	// sibling the caller never named stays out, exactly as `git add` of one
	// hunk would leave it.
	if strings.Contains(blob, "func Other()") {
		t.Errorf("staged new.go = %q; want the unnamed sibling excluded", blob)
	}
}

// TestPlanStage_PathTargetRefusesIgnoredPathButNotMagic pins the asymmetry in
// planPathspec: the "gitignored and untracked" refusal is about a caller
// naming one concrete path, so a pathspec magic form passes through to git
// add, which is the thing that decides what a glob is allowed to match.
func TestPlanStage_PathTargetRefusesIgnoredPathButNotMagic(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	gittest.Write(t, dir, ".gitignore", "*.log\n")
	gittest.Commit(ctx, t, dir, "chore: ignore logs")
	gittest.Write(t, dir, "trace.log", "ignored\n")

	_, err := PlanStage(ctx, repo, dir, []Target{PathTarget("trace.log")})
	var perr *PathError
	if !errors.As(err, &perr) {
		t.Fatalf("PlanStage(literal ignored path) = %v; want *PathError", err)
	}
	if perr.Code != exitcode.PathRefused {
		t.Errorf("PlanStage(literal ignored path) code = %v; want %v", perr.Code, exitcode.PathRefused)
	}

	plan, err := PlanStage(ctx, repo, dir, []Target{PathTarget(":(glob)**/*.log")})
	if err != nil {
		t.Fatalf("PlanStage(magic pathspec) = %v; want it forwarded to git add", err)
	}
	if len(plan.Results) != 1 || plan.Results[0].Outcome != Staged {
		t.Errorf("Results = %+v; want one Staged row for the pathspec", plan.Results)
	}
	if plan.Results[0].Path == "" {
		t.Error("Results[0].Path is empty; want the pathspec or one of the files it matched")
	}
}

// TestPlanStage_PathTargetCountsABinaryFileAsZero pins the listing rule for a
// binary file a pathspec stages: git writes "-" for both counts, which is not
// a counting failure -- the file is being staged, so it appears with zero
// lines rather than vanishing or warning. It also pins PathError's message
// shape, the text a caller prints for every refusal synth makes.
func TestPlanStage_PathTargetCountsABinaryFileAsZero(t *testing.T) {
	t.Parallel()

	perr := &PathError{Path: "logo.png", Reason: "binary; name the path instead of a symbol"}
	if got, want := perr.Error(), "synth: logo.png: binary; name the path instead of a symbol"; got != want {
		t.Errorf("PathError.Error() = %q; want %q", got, want)
	}

	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	gittest.Write(t, dir, "a.txt", "one\n")
	gittest.Write(t, dir, "blob.bin", "a\x00b\n")
	gittest.Commit(ctx, t, dir, "chore: initial")
	gittest.Write(t, dir, "blob.bin", "a\x00c\n")
	gittest.Write(t, dir, "a.txt", "two\n")

	plan, err := PlanStage(ctx, repo, dir, []Target{PathTarget("blob.bin"), PathTarget("a.txt")})
	if err != nil {
		t.Fatalf("PlanStage: %v", err)
	}
	if len(plan.CountingWarnings) != 0 {
		t.Errorf("CountingWarnings = %v; want none", plan.CountingWarnings)
	}
	byPath := map[string]TargetResult{}
	for _, res := range plan.Results {
		byPath[res.Path] = res
	}
	if got, ok := byPath["blob.bin"]; !ok {
		t.Fatalf("Results = %+v; want a row for the untracked binary file", plan.Results)
	} else if got.Added != 0 || got.Deleted != 0 {
		t.Errorf("binary row = %+v; want zero counts", got)
	}
	if got, ok := byPath["a.txt"]; !ok {
		t.Fatalf("Results = %+v; want a row for the tracked edit", plan.Results)
	} else if got.Added != 1 || got.Deleted != 1 {
		t.Errorf("a.txt row = %+v; want 1 added and 1 deleted", got)
	}
}

// TestResolveMode_InheritsFromIndexThenHead pins the mode a deletion inherits,
// in the order the invariant names: the index's stage-0 mode first, HEAD's
// tree entry when the index has no entry for the path at all. Getting this
// wrong flips an executable file to 0644 in the same commit that removes it.
func TestResolveMode_InheritsFromIndexThenHead(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	gittest.Write(t, dir, "run.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o755); err != nil { //nolint:gosec // executable mode is the behavior under test
		t.Fatal(err)
	}
	gittest.Write(t, dir, "plain.txt", "plain\n")
	gittest.Commit(ctx, t, dir, "chore: initial")

	// In the index and in HEAD: the index answers.
	if mode, err := resolveMode(ctx, repo, dir, "run.sh", false); err != nil {
		t.Fatalf("resolveMode(indexed): %v", err)
	} else if mode != "100755" {
		t.Errorf("resolveMode(indexed) = %q; want 100755", mode)
	}

	// Removed from the worktree AND from the index, still in HEAD: HEAD's
	// tree is the only remaining source.
	gittest.Git(ctx, t, dir, "rm", "-q", "--cached", "plain.txt")
	if mode, err := resolveMode(ctx, repo, dir, "plain.txt", false); err != nil {
		t.Fatalf("resolveMode(HEAD only): %v", err)
	} else if mode != "100644" {
		t.Errorf("resolveMode(HEAD only) = %q; want 100644", mode)
	}

	// Present in the worktree: the executable bit is read off the file
	// itself, whatever the index says.
	if mode, err := resolveMode(ctx, repo, dir, "run.sh", true); err != nil {
		t.Fatalf("resolveMode(worktree): %v", err)
	} else if mode != "100755" {
		t.Errorf("resolveMode(worktree) = %q; want 100755", mode)
	}
}

// TestSnapshotIndex_RestoreRevertsOnlyAppliedPaths pins the rollback a
// rejected commit performs: the entries Apply staged go back to the snapshot,
// while staging another session did meanwhile survives.
func TestSnapshotIndex_RestoreRevertsOnlyAppliedPaths(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir, repo := gittest.New(ctx, t)
	gittest.Write(t, dir, "a.txt", "one\n")
	gittest.Commit(ctx, t, dir, "chore: initial")
	gittest.Write(t, dir, "a.txt", "two\n")
	gittest.Write(t, dir, "c.txt", "other\n")

	snap, err := SnapshotIndex(ctx, dir)
	if err != nil {
		t.Fatalf("SnapshotIndex: %v", err)
	}
	plan, err := PlanStage(ctx, repo, dir, []Target{PathTarget("a.txt")})
	if err != nil {
		t.Fatalf("PlanStage: %v", err)
	}
	if err := plan.ApplyWithSnapshot(ctx, repo, dir, snap); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	gittest.Git(ctx, t, dir, "add", "c.txt")

	if err := snap.Restore(ctx); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := gittest.Git(ctx, t, dir, "status", "--porcelain"); got != " M a.txt\nA  c.txt\n" {
		t.Errorf("status after Restore = %q; want a.txt unstaged and c.txt still staged", got)
	}
}
