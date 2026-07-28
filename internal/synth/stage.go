package synth

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// SymbolTarget names one symbol anchor to stage within one file.
type SymbolTarget struct {
	Path   string
	Anchor string
}

// Target is one thing rgit commit was asked to stage. Exactly one of
// Pathspec or Symbol is meaningful: a plain path/pathspec is delegated
// straight to `git add`, never synthesized; a symbol anchor goes through
// blob synthesis (AGENTS.md's delegation boundary).
type Target struct {
	Pathspec string
	Symbol   SymbolTarget
}

// PathTarget wraps a plain pathspec.
func PathTarget(pathspec string) Target { return Target{Pathspec: pathspec} }

// AnchorTarget wraps a FILE:NAME symbol anchor.
func AnchorTarget(path, anchor string) Target {
	return Target{Symbol: SymbolTarget{Path: path, Anchor: anchor}}
}

// Outcome reports what resolving one Target found: real, uncommitted
// content worth staging, or an extent already byte-identical to HEAD --
// docs/USAGE.md § Targets with nothing to commit, which the caller turns
// into a per-target warning and the exit-11 rule.
type Outcome int

const (
	Staged Outcome = iota
	Unchanged
)

// TargetResult pairs a Target with what resolving it found, in the same
// order as the targets slice the caller passed to PlanStage or Stage.
type TargetResult struct {
	Target  Target
	Outcome Outcome
}

// filePlan accumulates every resolved edit for one file, plus the source
// state classify needs to resolve further anchors against it.
type filePlan struct {
	path       string
	lang       resolve.Language
	headSrc    []byte
	headExists bool
	workSrc    []byte
	workExists bool
	workOrder  []string // qualified anchor names in worktree declaration order
	ops        []editOp
}

// stagePlan is the pure-read result of resolving every target: nothing in
// it has touched the git index or written an object yet.
type stagePlan struct {
	pathspecs []string
	files     []*filePlan
	results   []TargetResult
	tsOnly    bool
}

// Plan is a resolved, not-yet-applied Stage. Every target has been
// classified and cross-checked -- a pure read -- but nothing has been
// written or staged. A caller that needs to inspect what resolution found
// before deciding whether to write anything at all (rgit commit's
// --dry-run, and its "every named target already matches HEAD" exit-11
// rule) resolves via PlanStage, decides, and calls Apply second; Stage
// itself is the two steps run back to back unconditionally.
type Plan struct {
	plan *stagePlan
}

// Results reports what resolution found for each target, in the order
// PlanStage (or Stage) received them.
func (p *Plan) Results() []TargetResult { return p.plan.results }

// TSOnly reports whether any anchor in the plan degraded to tree-sitter-only
// resolution because no live language server answered in time for its
// cross-check -- normal, not an error (specs/design.md), but the caller's
// job to announce once on stderr.
func (p *Plan) TSOnly() bool { return p.plan.tsOnly }

// Apply performs Plan's only side-effecting step: staging pathspecs via
// `git add` and writing + staging every file's synthesized blob.
func (p *Plan) Apply(ctx context.Context, repo *gitx.Repo, root string) error {
	return p.plan.apply(ctx, repo, root)
}

// PlanStage resolves every target against repo's worktree (root) and HEAD
// -- the pure-read half of Stage -- without writing anything. A caller that
// never calls the returned Plan's Apply (a --dry-run preview, or the
// "nothing to commit" exit-11 case) leaves the index exactly as found.
func PlanStage(ctx context.Context, repo *gitx.Repo, root string, targets []Target) (*Plan, error) {
	plan, err := planStage(ctx, repo, root, targets)
	if err != nil {
		return nil, err
	}
	return &Plan{plan: plan}, nil
}

// Stage resolves every target against repo's worktree (root) and HEAD,
// then -- only once all of them resolve cleanly -- writes the synthesized
// blobs and stages them. Resolution is a pure read, so a failure leaves
// the index exactly as found: the caller never ran `git add`, so nothing
// should have moved (specs/design.md § Blob synthesis; AGENTS.md's
// invariant table).
func Stage(ctx context.Context, repo *gitx.Repo, root string, targets []Target) error {
	plan, err := PlanStage(ctx, repo, root, targets)
	if err != nil {
		return err
	}
	return plan.Apply(ctx, repo, root)
}

func planStage(ctx context.Context, repo *gitx.Repo, root string, targets []Target) (*stagePlan, error) {
	plan := &stagePlan{}
	byPath := map[string]*filePlan{}

	for _, t := range targets {
		if t.Pathspec != "" {
			// Pathspec magic (leading ":") is a pattern, not a path --
			// check-ignore does not accept the same magic forms `git add`
			// does (e.g. ":(glob)**/*.txt" is not a valid check-ignore
			// argument), and git add itself does not refuse a glob or
			// magic pathspec up front the way it refuses a literal
			// gitignored path. The refusal in docs/ANCHORS.md is about a
			// caller naming one concrete path, so it only applies there.
			magic := strings.HasPrefix(t.Pathspec, ":")
			if !magic {
				if err := checkGitignoreRefusal(ctx, repo, t.Pathspec); err != nil {
					return nil, err
				}
			}
			plan.pathspecs = append(plan.pathspecs, t.Pathspec)

			outcome := Staged
			if !magic {
				// A magic pathspec is a filter over many files, not one
				// verifiable target, so it is never reported unchanged --
				// only a literal path's own status is a meaningful answer.
				unchanged, err := pathspecUnchanged(ctx, repo, t.Pathspec)
				if err != nil {
					return nil, err
				}
				if unchanged {
					outcome = Unchanged
				}
			}
			plan.results = append(plan.results, TargetResult{Target: t, Outcome: outcome})
			continue
		}

		fp, ok := byPath[t.Symbol.Path]
		if !ok {
			var err error
			fp, err = openFilePlan(ctx, repo, root, t.Symbol.Path)
			if err != nil {
				return nil, err
			}
			byPath[t.Symbol.Path] = fp
			plan.files = append(plan.files, fp)
		}

		op, unchanged, tsOnly, err := fp.classify(ctx, root, t.Symbol.Anchor)
		if err != nil {
			return nil, err
		}
		fp.ops = append(fp.ops, op)
		if tsOnly {
			plan.tsOnly = true
		}
		outcome := Staged
		if unchanged {
			outcome = Unchanged
		}
		plan.results = append(plan.results, TargetResult{Target: t, Outcome: outcome})
	}

	return plan, nil
}

// pathspecUnchanged answers docs/USAGE.md's "target has no uncommitted
// changes" question for a literal pathspec: `git status --porcelain`
// scoped to exactly that path. An empty result means the path is already
// clean relative to HEAD (and, for a tracked path, the index) -- staging
// it would be a real no-op, not merely a low-diff change.
func pathspecUnchanged(ctx context.Context, repo *gitx.Repo, pathspec string) (bool, error) {
	out, err := repo.Status(ctx, "--", pathspec)
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(out)) == 0, nil
}

// openFilePlan performs every pure read a file's anchors need before any
// of them can be classified: the gitignore/special-path refusal checks,
// language lookup, HEAD and worktree content, and (when the worktree has
// the file) its declaration order for nearest-sibling insertion.
func openFilePlan(ctx context.Context, repo *gitx.Repo, root, path string) (*filePlan, error) {
	if err := checkGitignoreRefusal(ctx, repo, path); err != nil {
		return nil, err
	}

	kind, err := classifyPath(ctx, repo, root, path)
	if err != nil {
		return nil, err
	}
	if err := refusalFor(path, kind); err != nil {
		return nil, err
	}

	ext := filepath.Ext(path)
	lang, ok := resolve.ForExtension(ext)
	if !ok {
		return nil, &PathError{Code: exitcode.UnsupportedLanguage, Path: path, Reason: "no grammar registered for " + ext}
	}

	headSrc, headExists, err := repo.CatFile(ctx, "HEAD", path)
	if err != nil {
		return nil, err
	}
	workSrc, workExists, err := readWorktreeFile(root, path)
	if err != nil {
		return nil, err
	}

	fp := &filePlan{
		path:       path,
		lang:       lang,
		headSrc:    headSrc,
		headExists: headExists,
		workSrc:    workSrc,
		workExists: workExists,
	}
	if workExists {
		fp.workOrder, err = resolve.DeclOrder(lang, workSrc)
		if err != nil {
			return nil, err
		}
	}
	return fp, nil
}

func readWorktreeFile(root, path string) (content []byte, exists bool, err error) {
	content, err = os.ReadFile(filepath.Join(root, path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return content, true, nil
}

// apply is the plan's only side-effecting step: pathspecs delegate to
// `git add`, and every file's synthesized blob is written and staged.
func (p *stagePlan) apply(ctx context.Context, repo *gitx.Repo, root string) error {
	if len(p.pathspecs) > 0 {
		if err := repo.Add(ctx, p.pathspecs...); err != nil {
			return err
		}
	}
	for _, fp := range p.files {
		content := applyEdits(fp.headSrc, fp.ops)

		mode, err := resolveMode(ctx, repo, root, fp.path, fp.workExists)
		if err != nil {
			return err
		}
		sha, err := repo.HashObject(ctx, fp.path, content)
		if err != nil {
			return err
		}
		if err := repo.UpdateIndexCacheinfo(ctx, mode, sha, fp.path); err != nil {
			return err
		}
	}
	return nil
}

// resolveMode reports the git file mode the staged blob should carry:
// os.Stat's executable bit when the worktree has the file, or the mode
// already recorded at HEAD when it does not -- staging a symbol deletion
// from a file that was itself deleted has no worktree entry to stat
// (AGENTS.md's invariant table).
func resolveMode(ctx context.Context, repo *gitx.Repo, root, path string, workExists bool) (string, error) {
	if workExists {
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil {
			return "", err
		}
		if info.Mode()&0o111 != 0 {
			return "100755", nil
		}
		return "100644", nil
	}

	entry, found, err := repo.LsTree(ctx, "HEAD", path)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("synth: %s: no HEAD entry to derive mode for a deleted worktree file", path)
	}
	return entry.Mode, nil
}
