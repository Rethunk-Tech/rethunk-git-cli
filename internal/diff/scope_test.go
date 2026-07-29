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
// form's has to be A itself, unresolved -- a regression swapping the two
// (or resolving the two-dot form's endpoints too) must fail here, in the
// unit lane, not only in cmd/rgit/rgit_e2e_test.go's slow lane.
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

// TestResolveRangeScope_ErrorPaths covers every branch in
// internal/diff/scope.go's resolveRangeScope that reports rather than
// resolves: a malformed three- or two-dot range with an empty endpoint,
// and two revisions with no common ancestor at all. Each is reachable from
// ordinary CLI input (`rgit diff A...`, `rgit diff ..B`), and each has to
// come back as a *UsageError specifically -- internal/app maps that type,
// and only that type, to exit 129 rather than exit 128.
func TestResolveRangeScope_ErrorPaths(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "f.txt", "one\n")
	gittest.Commit(t, dir, "chore: fixture")
	ctx := context.Background()

	for _, tc := range []struct {
		name  string
		token string
		want  string
	}{
		{"three-dot missing left endpoint", "...HEAD", `malformed revision range "...HEAD"`},
		{"three-dot missing right endpoint", "HEAD...", `malformed revision range "HEAD..."`},
		{"two-dot missing left endpoint", "..HEAD", `malformed revision range "..HEAD"`},
		{"two-dot missing right endpoint", "HEAD..", `malformed revision range "HEAD.."`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveRangeScope(ctx, repo, tc.token)
			var uerr *UsageError
			if !errors.As(err, &uerr) {
				t.Fatalf("resolveRangeScope(%q) error = %v (%T); want *UsageError", tc.token, err, err)
			}
			if uerr.Error() != tc.want {
				t.Errorf("resolveRangeScope(%q) = %q; want %q", tc.token, uerr.Error(), tc.want)
			}
		})
	}

	t.Run("no merge base between two orphan branches", func(t *testing.T) {
		gittest.Git(t, dir, "checkout", "-q", "--orphan", "isolated")
		gittest.Git(t, dir, "commit", "-q", "-m", "chore: isolated root")
		gittest.Git(t, dir, "checkout", "-q", "main")

		_, err := resolveRangeScope(ctx, repo, "main...isolated")
		var uerr *UsageError
		if !errors.As(err, &uerr) {
			t.Fatalf("resolveRangeScope(main...isolated) error = %v (%T); want *UsageError", err, err)
		}
		want := `no merge base between "main" and "isolated"`
		if uerr.Error() != want {
			t.Errorf("resolveRangeScope(main...isolated) = %q; want %q", uerr.Error(), want)
		}
	})
}

// TestResolveRangeScope_MergeBaseExecFailureIsNotAUsageError separates "git
// itself could not be run" (a *gitx.ExecError, exit 128) from "git ran and
// found nothing" (the *UsageError case above, exit 129) -- the two error
// paths resolveRangeScope's three-dot branch can take from repo.MergeBase,
// and internal/app must route them to different exit codes.
func TestResolveRangeScope_MergeBaseExecFailureIsNotAUsageError(t *testing.T) {
	t.Parallel()
	_, repo, _ := newDivergentRepo(t)

	// A context already canceled before the call means git is never even
	// started (gitx's own contract: "binary missing, context canceled" is
	// an *ExecError, not a negative answer) -- deterministic, and it needs
	// no PATH surgery that t.Parallel siblings could race on.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := resolveRangeScope(ctx, repo, "main...feature")
	var uerr *UsageError
	if errors.As(err, &uerr) {
		t.Fatalf("resolveRangeScope error = %v; want a *gitx.ExecError, not *UsageError", err)
	}
	var execErr *gitx.ExecError
	if !errors.As(err, &execErr) {
		t.Fatalf("resolveRangeScope error = %v (%T); want *gitx.ExecError", err, err)
	}
}

// TestResolveRangeScope_ExplicitFormFallsBackToSingleRevision covers
// --range's third accepted shape: a token with no ".." at all, which
// scope.go treats exactly like a bare positional revision (old side the
// named revision, new side the worktree) rather than erroring for want of
// a range separator.
func TestResolveRangeScope_ExplicitFormFallsBackToSingleRevision(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "f.txt", "one\n")
	gittest.Commit(t, dir, "chore: fixture")

	scope, err := resolveRangeScope(context.Background(), repo, "HEAD")
	if err != nil {
		t.Fatalf("resolveRangeScope(HEAD): %v", err)
	}
	if scope.Old.kind != sideRev || scope.Old.rev != "HEAD" {
		t.Errorf("Old = %+v; want sideRev at literal \"HEAD\"", scope.Old)
	}
	if scope.New.kind != sideWorktree {
		t.Errorf("New = %+v; want sideWorktree", scope.New)
	}
	if len(scope.NumstatArgs) != 1 || scope.NumstatArgs[0] != "HEAD" {
		t.Errorf("NumstatArgs = %v; want [\"HEAD\"]", scope.NumstatArgs)
	}
}

// TestCommittableBase_UnbornBranchFallsBackToEmptyTree covers
// committableBase's own reason to exist: gittest.New's repo has no commit
// yet, so HEAD is unborn and `git diff HEAD` would fail outright --
// docs/USAGE.md § Diff scope's documented fallback.
func TestCommittableBase_UnbornBranchFallsBackToEmptyTree(t *testing.T) {
	t.Parallel()
	_, repo := gittest.New(t)

	base, err := committableBase(context.Background(), repo)
	if err != nil {
		t.Fatalf("committableBase: %v", err)
	}
	// git's own well-known empty tree SHA, constant across every repo.
	const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	if base != emptyTreeSHA {
		t.Errorf("base = %q; want the empty tree %q, not a literal \"HEAD\" (branch is unborn)", base, emptyTreeSHA)
	}
}
