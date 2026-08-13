package diff

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
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

	// One case per separator, not one per missing side: scope.go's own
	// check is a single `a == "" || b == ""`, so a missing left endpoint
	// and a missing right endpoint hit the identical branch -- a second
	// case per separator would cover nothing the first does not already.
	for _, tc := range []struct {
		name  string
		token string
		want  string
	}{
		{"three-dot missing left endpoint", "...HEAD", `malformed revision range "...HEAD"`},
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
	if _, ok := errors.AsType[*UsageError](err); ok {
		t.Fatalf("resolveRangeScope error = %v; want a *gitx.ExecError, not *UsageError", err)
	}
	if _, ok := errors.AsType[*gitx.ExecError](err); !ok {
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

func TestContentSideRead_UnmergedFallsBackToWorktree(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "conflict.txt", "base\n")
	gittest.Commit(t, dir, "chore: add conflict fixture")
	gittest.Write(t, dir, "conflict.txt", "<<<<<<< ours\nworktree\n>>>>>>> theirs\n")

	blob := strings.TrimSpace(gittest.Git(t, dir, "rev-parse", "HEAD:conflict.txt"))
	setUnmergedIndex(t, dir, blob, "conflict.txt")

	content, exists, err := indexSide().read(context.Background(), repo, dir, "conflict.txt", nil)
	if err != nil {
		t.Fatalf("indexSide.read: %v", err)
	}
	if !exists {
		t.Fatal("indexSide.read exists = false; want true")
	}
	want := "<<<<<<< ours\nworktree\n>>>>>>> theirs\n"
	if string(content) != want {
		t.Errorf("indexSide.read content = %q; want %q", content, want)
	}
}

func setUnmergedIndex(t *testing.T, dir, blob, path string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "update-index", "--index-info")
	cmd.Stdin = strings.NewReader(fmt.Sprintf(
		"100644 %s 1\t%s\n100644 %s 2\t%s\n100644 %s 3\t%s\n",
		blob, path, blob, path, blob, path,
	))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git update-index --index-info: %v: %s", err, out)
	}
}

// TestExtractRangeToken_DetectsRangeNotAPath covers ExtractRangeToken's
// main cold path: a ".."-shaped argument that does not exist as a path in
// the worktree, index, or HEAD is the range token, pulled out of args
// rather than left for cli.ClassifyArgs to fail on. The real GitPathChecker
// drives this rather than a stand-in -- CONTRIBUTING's "prefer the real
// dependency" rule -- since the whole point of the check is a real git
// ls-tree/os.Stat answer, not this package's belief about one.
func TestExtractRangeToken_DetectsRangeNotAPath(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	checker := &cli.GitPathChecker{Root: dir, Repo: repo}
	ctx := context.Background()

	t.Run("range token pulled out, non-range args left in rest", func(t *testing.T) {
		token, rest, err := ExtractRangeToken(ctx, []string{"a.go", "HEAD..HEAD~1"}, checker)
		if err != nil {
			t.Fatalf("ExtractRangeToken: %v", err)
		}
		if token != "HEAD..HEAD~1" {
			t.Errorf("token = %q; want %q", token, "HEAD..HEAD~1")
		}
		if len(rest) != 1 || rest[0] != "a.go" {
			t.Errorf("rest = %v; want [\"a.go\"]", rest)
		}
	})

	// "--" ends the scan outright (docs/USAGE.md's own "everything after --
	// is a pathspec, always"): a range-shaped token past it must never be
	// pulled out, even though it would qualify on its own.
	t.Run("-- stops the range scan", func(t *testing.T) {
		args := []string{"a.go", "--", "HEAD..HEAD~1"}
		token, rest, err := ExtractRangeToken(ctx, args, checker)
		if err != nil {
			t.Fatalf("ExtractRangeToken: %v", err)
		}
		if token != "" {
			t.Errorf("token = %q; want \"\" (nothing before -- is range-shaped)", token)
		}
		if len(rest) != len(args) {
			t.Errorf("rest = %v; want args returned unchanged", rest)
		}
	})

	// Two range-shaped, non-existent args in the same invocation is
	// ambiguous -- ExtractRangeToken cannot guess which one the caller
	// meant, so both a rgit diff and a --range flag can only ever supply
	// one.
	t.Run("multiple range-shaped arguments is an error", func(t *testing.T) {
		_, _, err := ExtractRangeToken(ctx, []string{"main..feature", "HEAD..HEAD~1"}, checker)
		want := `multiple revision-range-shaped arguments given: "main..feature" and "HEAD..HEAD~1"`
		if err == nil || err.Error() != want {
			t.Errorf("err = %v; want %q", err, want)
		}
		// Typed as *UsageError, the same as every other scope-shape problem
		// this package detects itself (Run's own ResolveScope checks,
		// TestRun_ScopeUsageErrorsAreTyped) -- this is a caller mistake
		// (exit 129), never a git-level failure (exit 128), and internal/app
		// maps the two exit codes by this exact type.
		if _, ok := errors.AsType[*UsageError](err); !ok {
			t.Errorf("err = %v (%T); want *UsageError", err, err)
		}
	})

	// A relative pathspec containing ".." (typed from a subdirectory) has
	// to win over the range heuristic: path existence is checked first,
	// and PrefixPath's own root-relative rebasing is what makes "../shared/
	// util.go" resolve to the real "shared/util.go" from prefix "sub".
	t.Run("an existing ../path wins over range-shaped parsing", func(t *testing.T) {
		gittest.Write(t, dir, "shared/util.go", "package shared\n")
		subChecker := &cli.GitPathChecker{Root: dir, Prefix: "sub", Repo: repo}

		token, rest, err := ExtractRangeToken(ctx, []string{"../shared/util.go"}, subChecker)
		if err != nil {
			t.Fatalf("ExtractRangeToken: %v", err)
		}
		if token != "" {
			t.Errorf("token = %q; want \"\" (the path exists, so it is not a range)", token)
		}
		if len(rest) != 1 || rest[0] != "../shared/util.go" {
			t.Errorf("rest = %v; want ../shared/util.go left untouched", rest)
		}
	})
}
