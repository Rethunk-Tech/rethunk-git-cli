package diff

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

// newDivergentRepo builds a repo with two branches sharing one ancestor
// commit: "main" and "feature" both continue from "base", each with one
// commit of their own. That is the minimum shape that makes A...B's merge
// base different from both A and B, which is the whole thing
// resolveRangeScope's three-dot form exists to get right.
func newDivergentRepo(t *testing.T) (dir string, repo *gitx.Repo, baseSHA string) {
	t.Helper()
	dir, repo = gittest.New(t)

	gittest.Write(t, dir, "f.txt", "base\n")
	gittest.Commit(t, dir, "chore: base")
	baseSHA = strings.TrimSpace(gittest.Git(t, dir, "rev-parse", "HEAD"))

	gittest.Git(t, dir, "checkout", "-q", "-b", "feature")
	gittest.Write(t, dir, "f.txt", "base\nfeature\n")
	gittest.Commit(t, dir, "chore: feature")

	gittest.Git(t, dir, "checkout", "-q", "main")
	gittest.Write(t, dir, "f.txt", "base\nmain\n")
	gittest.Commit(t, dir, "chore: main")

	return dir, repo, baseSHA
}

// TestResolveRangeScope_ThreeDotUsesMergeBaseTwoDotUsesLiteralA is
// resolveRangeScope's own doc comment turned into an assertion: the
// three-dot form's old side has to be the merge base, and the two-dot
// form's has to be A itself, unresolved. Before this test, only
// cmd/rgit/rgit_e2e_test.go exercised either form at all, so a regression
// swapping the two (or resolving the two-dot form's endpoints too) would
// pass `go test -short ./...` clean.
func TestResolveRangeScope_ThreeDotUsesMergeBaseTwoDotUsesLiteralA(t *testing.T) {
	t.Parallel()
	_, repo, base := newDivergentRepo(t)
	ctx := context.Background()

	threeDot, err := resolveRangeScope(ctx, repo, "main...feature")
	if err != nil {
		t.Fatalf("resolveRangeScope(main...feature): %v", err)
	}
	if threeDot.Old.kind != sideRev || threeDot.Old.rev != base {
		t.Errorf("three-dot Old = %+v; want sideRev at merge base %s", threeDot.Old, base)
	}
	if threeDot.New.kind != sideRev || threeDot.New.rev != "feature" {
		t.Errorf("three-dot New = %+v; want sideRev at literal %q", threeDot.New, "feature")
	}

	twoDot, err := resolveRangeScope(ctx, repo, "main..feature")
	if err != nil {
		t.Fatalf("resolveRangeScope(main..feature): %v", err)
	}
	if twoDot.Old.kind != sideRev || twoDot.Old.rev != "main" {
		t.Errorf("two-dot Old = %+v; want sideRev at literal \"main\" (not the merge base %s)", twoDot.Old, base)
	}
	if twoDot.New.kind != sideRev || twoDot.New.rev != "feature" {
		t.Errorf("two-dot New = %+v; want sideRev at literal %q", twoDot.New, "feature")
	}
}
