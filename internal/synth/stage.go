package synth

import (
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
}

// Stage resolves every target against repo's worktree (root) and HEAD,
// then -- only once all of them resolve cleanly -- writes the synthesized
// blobs and stages them. Resolution is a pure read, so a failure leaves
// the index exactly as found: the caller never ran `git add`, so nothing
// should have moved (specs/design.md § Blob synthesis; AGENTS.md's
// invariant table).
func Stage(ctx context.Context, repo *gitx.Repo, root string, targets []Target) error {
	plan, err := planStage(ctx, repo, root, targets)
	if err != nil {
		return err
	}
	return plan.apply(ctx, repo, root)
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
			if !strings.HasPrefix(t.Pathspec, ":") {
				if err := checkGitignoreRefusal(ctx, repo, t.Pathspec); err != nil {
					return nil, err
				}
			}
			plan.pathspecs = append(plan.pathspecs, t.Pathspec)
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

		op, err := fp.classify(t.Symbol.Anchor)
		if err != nil {
			return nil, err
		}
		fp.ops = append(fp.ops, op)
	}

	return plan, nil
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
