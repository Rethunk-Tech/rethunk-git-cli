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
//
// cache, when non-nil, is checked before falling back to a live CatFile
// call — blobCache's own doc comment explains why a miss is a fallback and
// never an error: a cache built from the same (side, path) pairs the
// caller is about to read misses only when that invariant does not hold,
// which is a caller bug to surface as a wrong answer, not a panic.
func (s contentSide) read(ctx context.Context, repo *gitx.Repo, root, path string, cache *blobCache) (content []byte, exists bool, err error) {
	switch s.kind {
	case sideWorktree:
		return util.ReadFileIfExists(root, path)
	case sideIndex:
		// CatFile builds rev+":"+path; an empty rev yields ":path", which
		// git reads as the index's stage-0 entry.
		unmerged, err := repo.IsUnmerged(ctx, path)
		if err != nil {
			return nil, false, err
		}
		if unmerged {
			return util.ReadFileIfExists(root, path)
		}
		if cache != nil {
			if res, ok := cache.lookup("", path); ok {
				if res.Exists {
					return res.Content, true, nil
				}
				return readUnmergedWorktree(ctx, repo, root, path)
			}
		}
		content, exists, err := repo.CatFile(ctx, "", path)
		if err != nil || exists {
			return content, exists, err
		}
		return readUnmergedWorktree(ctx, repo, root, path)
	default:
		if cache != nil {
			if res, ok := cache.lookup(s.rev, path); ok {
				return res.Content, res.Exists, nil
			}
		}
		return repo.CatFile(ctx, s.rev, path)
	}
}

func readUnmergedWorktree(ctx context.Context, repo *gitx.Repo, root, path string) ([]byte, bool, error) {
	unmerged, err := repo.IsUnmerged(ctx, path)
	if err != nil {
		return nil, false, err
	}
	if !unmerged {
		return nil, false, nil
	}
	return util.ReadFileIfExists(root, path)
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

// blobCache holds every git-backed blob buildFileReport's own per-file loop
// (run.go) is about to read, fetched once up front through
// gitx.Repo.BatchCatFile instead of one `git cat-file` subprocess per file
// per side -- the batching internal/gitx.BatchCatFile's own doc comment
// describes, applied here at the one call site that reads N files in a
// loop. Keyed by rev+":"+path, the identical string CatFile itself builds,
// so sideIndex's own empty rev ("" -> ":path") and sideRev(rev)'s explicit
// one never collide.
//
// A cache miss is never an error: prefetchBlobs only ever populates it from
// the exact (side, path) pairs the same invocation is about to read, so a
// miss reaching contentSide.read's fallback would mean prefetchBlobs and
// the file loop disagreed about which paths matter -- a bug to keep
// contentSide.read's live CatFile fallback available for, not a state this
// cache needs to guard against with its own error path.
type blobCache struct {
	byKey map[string]gitx.BatchCatFileResult
}

func blobCacheKey(rev, path string) string { return rev + ":" + path }

func (c *blobCache) lookup(rev, path string) (gitx.BatchCatFileResult, bool) {
	if c == nil {
		return gitx.BatchCatFileResult{}, false
	}
	res, ok := c.byKey[blobCacheKey(rev, path)]
	return res, ok
}

// prefetchBlobs batches every git-backed read buildFileReport's own loop
// (run.go) is about to make -- scope.Old and scope.New, for every changed
// path, skipping sideWorktree entirely since that reads the local
// filesystem directly and was never a subprocess to batch. oldPaths and
// newPaths are parallel slices, one entry per changed file (a rename's two
// names differ; every other change repeats the same path in both).
func prefetchBlobs(ctx context.Context, repo *gitx.Repo, scope Scope, oldPaths, newPaths []string) (*blobCache, error) {
	var requests []gitx.BatchCatFileRequest
	addSide := func(s contentSide, path string) {
		switch s.kind {
		case sideWorktree:
			return
		case sideIndex:
			requests = append(requests, gitx.BatchCatFileRequest{Path: path})
		default:
			requests = append(requests, gitx.BatchCatFileRequest{Rev: s.rev, Path: path})
		}
	}
	for i := range oldPaths {
		addSide(scope.Old, oldPaths[i])
		addSide(scope.New, newPaths[i])
	}
	if len(requests) == 0 {
		return &blobCache{byKey: map[string]gitx.BatchCatFileResult{}}, nil
	}

	results, err := repo.BatchCatFile(ctx, requests)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]gitx.BatchCatFileResult, len(requests))
	for i, req := range requests {
		byKey[blobCacheKey(req.Rev, req.Path)] = results[i]
	}
	return &blobCache{byKey: byKey}, nil
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
	if len(opts.RevPaths) == 2 && (opts.Staged || opts.Unstaged || hasRevArgs || len(opts.Files) > 0) {
		return Scope{}, &UsageError{Msg: "a two-blob \"A:f.go B:f.go\" scope is exclusive of every other scope selector and pathspec"}
	}

	switch {
	case len(opts.RevPaths) == 2:
		return resolveRevPathScope(opts.RevPaths[0], opts.RevPaths[1]), nil
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
		base, err := repo.CommittableBase(ctx)
		if err != nil {
			return Scope{}, err
		}
		return Scope{Old: revSide(base), New: worktreeSide(), NumstatArgs: []string{base}, IncludeUntracked: true}, nil
	}
}

// resolveRevPathScope builds the two-blob "A:f.go B:f.go" scope from a
// pair BucketClassified already verified name the identical path.
// NumstatArgs passes both blob refs straight through to `git diff
// --numstat`, which accepts them exactly like any other two comparison
// endpoints and reports one ordinary numstat row for the path -- no
// synthetic file key needed, since the path itself is already the row's
// own key. contentSide's existing revSide covers reading each side's blob
// (CatFile(rev, path)), so the rest of Run needs no changes at all to
// attribute this scope by symbol the same way any other does.
func resolveRevPathScope(a, b cli.RevPath) Scope {
	return Scope{
		Old:         revSide(a.Rev),
		New:         revSide(b.Rev),
		NumstatArgs: []string{a.Rev + ":" + a.Path, b.Rev + ":" + b.Path},
	}
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
// Only a token with no existing worktree, index, or HEAD path of that exact
// name is treated as a range: git forbids ".." in ref names, but a legitimate
// relative pathspec like "../shared/util.go" also contains "..", and path
// existence must win over the heuristic.
func ExtractRangeToken(ctx context.Context, args []string, paths cli.PathChecker) (token string, rest []string, err error) {
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
	var foundToken string
	for i := 0; i < limit; i++ {
		a := args[i]
		if strings.HasPrefix(a, ":") || !strings.Contains(a, "..") {
			continue
		}
		exists, perr := paths.ExistsInWorktreeOrHEAD(ctx, a)
		if perr != nil {
			return "", nil, perr
		}
		if exists {
			continue
		}
		if found >= 0 {
			return "", nil, &UsageError{Msg: fmt.Sprintf("multiple revision-range-shaped arguments given: %q and %q", foundToken, a)}
		}
		found = i
		foundToken = a
	}
	if found < 0 {
		return "", args, nil
	}

	rest = make([]string, 0, len(args)-1)
	rest = append(rest, args[:found]...)
	rest = append(rest, args[found+1:]...)
	return foundToken, rest, nil
}
