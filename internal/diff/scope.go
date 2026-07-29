package diff

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/util"
)

// sideKind is which of the three places a file's content for one side of a
// comparison comes from.
type sideKind int

const (
	sideWorktree sideKind = iota
	sideIndex
	sideRev
)

// contentSide is one endpoint of a diff comparison — worktree, index, or a
// specific revision — abstracting over docs/USAGE.md § Diff scope's four
// scopes so the rest of this package reads and stats a path without caring
// which scope produced it.
type contentSide struct {
	kind sideKind
	rev  string // meaningful only when kind == sideRev
}

func worktreeSide() contentSide      { return contentSide{kind: sideWorktree} }
func indexSide() contentSide         { return contentSide{kind: sideIndex} }
func revSide(rev string) contentSide { return contentSide{kind: sideRev, rev: rev} }

// read returns path's content on this side. exists is false when the side
// simply has no such path — new-in-this-diff and deleted-in-this-diff are
// both ordinary outcomes here, not errors, matching gitx.CatFile's own
// convention.
func (s contentSide) read(ctx context.Context, repo *gitx.Repo, root, path string) (content []byte, exists bool, err error) {
	switch s.kind {
	case sideWorktree:
		return util.ReadFileIfExists(filepath.Join(root, path))
	case sideIndex:
		// CatFile builds rev+":"+path; an empty rev yields ":path", which
		// git reads as the index's stage-0 entry.
		return repo.CatFile(ctx, "", path)
	default:
		return repo.CatFile(ctx, s.rev, path)
	}
}

// mode returns the git file mode recorded for path on this side, for
// StatusMode rendering. found mirrors read's "no such path" convention.
func (s contentSide) mode(ctx context.Context, repo *gitx.Repo, root, path string) (mode string, found bool, err error) {
	switch s.kind {
	case sideWorktree:
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil {
			if os.IsNotExist(err) {
				return "", false, nil
			}
			return "", false, err
		}
		return util.GitFileMode(info), true, nil
	case sideIndex:
		return repo.LsFilesStage(ctx, path)
	default:
		entry, found, err := repo.LsTree(ctx, s.rev, path)
		if err != nil || !found {
			return "", found, err
		}
		return entry.Mode, true, nil
	}
}

// Scope is one resolved rgit diff scope: the two sides to compare for
// symbol-level content, the extra arguments that select the identical
// comparison in `git diff --numstat`, and whether untracked files belong in
// the result.
type Scope struct {
	Old, New         contentSide
	NumstatArgs      []string
	IncludeUntracked bool
}

// ResolveScope implements docs/USAGE.md § Diff scope's four scopes plus the
// two extra positional-revision forms plain `git diff` itself accepts (one
// bare revision against the worktree, two revisions against each other) —
// both reachable once cli.ClassifyArgs's rule 3 has classified a positional
// as KindRevision.
func ResolveScope(ctx context.Context, repo *gitx.Repo, opts Options) (Scope, error) {
	rangeToken := opts.RangeFlag
	if opts.PositionalRange != "" {
		if rangeToken != "" {
			return Scope{}, &UsageError{Msg: "--range and a positional revision range are mutually exclusive"}
		}
		rangeToken = opts.PositionalRange
	}

	hasRevArgs := rangeToken != "" || len(opts.Revisions) > 0
	if (opts.Staged || opts.Unstaged) && hasRevArgs {
		return Scope{}, &UsageError{Msg: "--staged/--unstaged and a revision range are mutually exclusive"}
	}

	switch {
	case opts.Staged:
		return Scope{Old: revSide("HEAD"), New: indexSide(), NumstatArgs: []string{"--staged"}}, nil
	case opts.Unstaged:
		return Scope{Old: indexSide(), New: worktreeSide()}, nil
	case rangeToken != "":
		return resolveRangeScope(ctx, repo, rangeToken)
	case len(opts.Revisions) == 1:
		r := opts.Revisions[0]
		return Scope{Old: revSide(r), New: worktreeSide(), NumstatArgs: []string{r}}, nil
	case len(opts.Revisions) == 2:
		a, b := opts.Revisions[0], opts.Revisions[1]
		return Scope{Old: revSide(a), New: revSide(b), NumstatArgs: []string{a, b}}, nil
	case len(opts.Revisions) > 2:
		return Scope{}, &UsageError{Msg: "at most two revision arguments are accepted"}
	default:
		base, err := committableBase(ctx, repo)
		if err != nil {
			return Scope{}, err
		}
		return Scope{Old: revSide(base), New: worktreeSide(), NumstatArgs: []string{base}, IncludeUntracked: true}, nil
	}
}

// committableBase is the old side of the default "everything committable"
// scope: HEAD, or the empty tree on an unborn branch, where HEAD names no
// commit and `git diff HEAD` fails outright. Every tracked path then reads
// as an addition, which is what rgit commit would write as a root commit.
func committableBase(ctx context.Context, repo *gitx.Repo) (string, error) {
	if _, ok, err := repo.RevParseVerify(ctx, "HEAD"); err != nil {
		return "", err
	} else if ok {
		return "HEAD", nil
	}
	return repo.EmptyTree(ctx)
}

// resolveRangeScope splits a "A..B" or "A...B" positional. The three-dot
// form's old-side content endpoint is the merge base, not A itself — git's
// own `--numstat A...B` already gets this right for the file-level totals,
// but per-symbol content diffing needs an actual revision to read, so the
// merge base has to be resolved explicitly.
func resolveRangeScope(ctx context.Context, repo *gitx.Repo, rangeToken string) (Scope, error) {
	if a, b, ok := strings.Cut(rangeToken, "..."); ok {
		if a == "" || b == "" {
			return Scope{}, &UsageError{Msg: fmt.Sprintf("malformed revision range %q", rangeToken)}
		}
		base, ok, err := repo.MergeBase(ctx, a, b)
		if err != nil {
			return Scope{}, err
		}
		if !ok {
			return Scope{}, &UsageError{Msg: fmt.Sprintf("no merge base between %q and %q", a, b)}
		}
		return Scope{Old: revSide(base), New: revSide(b), NumstatArgs: []string{rangeToken}}, nil
	}
	if a, b, ok := strings.Cut(rangeToken, ".."); ok {
		if a == "" || b == "" {
			return Scope{}, &UsageError{Msg: fmt.Sprintf("malformed revision range %q", rangeToken)}
		}
		return Scope{Old: revSide(a), New: revSide(b), NumstatArgs: []string{rangeToken}}, nil
	}
	// --range's explicit form accepts a single revision with no "..", same
	// as a bare positional revision would.
	return Scope{Old: revSide(rangeToken), New: worktreeSide(), NumstatArgs: []string{rangeToken}}, nil
}

// ExtractRangeToken pulls a bare ".."/"..." shaped revision-range
// positional out of args before cli.ClassifyArgs sees it: rule 3 verifies
// via `git rev-parse --verify`, which names exactly one object and fails
// outright on range syntax (exit 1, even though it echoes both endpoints
// to stdout) — so a range token would otherwise fall through every rule to
// an unresolvable-argument error instead of selecting a scope.
//
// Only a token with no worktree/HEAD path of that exact name is treated as
// a range: git forbids ".." in ref names, but a legitimate relative
// pathspec like "../shared/util.go" also contains "..", and path existence
// must win over the heuristic.
func ExtractRangeToken(args []string, paths cli.PathChecker) (token string, rest []string, err error) {
	dashAt := -1
	for i, a := range args {
		if a == "--" {
			dashAt = i
			break
		}
	}
	limit := len(args)
	if dashAt >= 0 {
		limit = dashAt
	}

	found := -1
	for i := 0; i < limit; i++ {
		a := args[i]
		if strings.HasPrefix(a, ":") || !strings.Contains(a, "..") {
			continue
		}
		exists, perr := paths.ExistsInWorktreeOrHEAD(a)
		if perr != nil {
			return "", nil, perr
		}
		if exists {
			continue
		}
		if found >= 0 {
			return "", nil, fmt.Errorf("multiple revision-range-shaped arguments given: %q and %q", args[found], a)
		}
		found = i
	}
	if found < 0 {
		return "", args, nil
	}

	rest = make([]string, 0, len(args)-1)
	rest = append(rest, args[:found]...)
	rest = append(rest, args[found+1:]...)
	return args[found], rest, nil
}
