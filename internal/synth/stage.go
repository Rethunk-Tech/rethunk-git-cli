package synth

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/diff"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/util"
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

	// Added and Deleted are the line counts for this target's own extent,
	// so a --dry-run preview can report magnitude and not just names. They
	// come from the same counter `rgit diff` uses; a preview that disagreed
	// with the diff it previews would be worse than none. Both are zero for
	// a pathspec target these cover the whole path, matching what
	// `rgit diff` reports for a file with no addressable symbols.
	Added   int
	Deleted int

	// path and start order the results: alphabetical by file, then
	// ascending by position within it, matching `rgit diff` and `git
	// status`. Listing targets in the order the caller happened to name
	// them makes output unstable between runs and awkward to grep.
	path  string
	start uint
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

	// headFile and workFile are the two sources parsed once and held open;
	// every anchor in this file resolves against them rather than re-parsing.
	// Either is nil when that side has no such file. close() releases both.
	headFile  *resolve.File
	workFile  *resolve.File
	escalated []string // member anchors widened to their enclosing container
}

// stagePlan is the pure-read result of resolving every target: nothing in
// it has touched the git index or written an object yet.
type stagePlan struct {
	pathspecs []string
	files     []*filePlan
	results   []TargetResult
	tsOnly    bool

	// preamble lists files whose @header/@imports were staged for them
	// because the file is new, and ordinals lists anchors that resolved
	// positionally. Both are the caller's to announce on stderr
	// (docs/ANCHORS.md); synth writes to no stream of its own.
	preamble  []string
	ordinals  []string
	escalated []string
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

// Preamble lists the files whose @header and @imports were staged alongside
// the symbols actually named, because the file does not exist in HEAD --
// docs/ANCHORS.md's "both are staged automatically for an untracked file".
// Without them the synthesized blob is a bare function body with no package
// clause, which does not compile.
func (p *Plan) Preamble() []string { return p.plan.preamble }

// Ordinals lists anchors that resolved by position ("init#2") rather than by
// a unique or container-qualified name. docs/ANCHORS.md calls the form a last
// resort because an inserted symbol repoints it.
func (p *Plan) Ordinals() []string { return p.plan.ordinals }

// Escalated lists member anchors that were widened to their enclosing
// container because HEAD has neither -- a method cannot be added to a class
// that does not exist yet. The caller announces it; staging more than was
// named is not something to do quietly.
func (p *Plan) Escalated() []string { return p.plan.escalated }

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
	// Anchors the caller named per path, so the new-file preamble pass below
	// does not stage a second copy of one they asked for themselves.
	named := map[string]map[string]bool{}

	sess := lsp.NewSession()
	defer sess.Close()
	// The trees are needed only while resolving; apply works from the byte
	// offsets and text the plan already holds.
	defer func() {
		for _, fp := range plan.files {
			fp.close()
		}
	}()

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

			// Pathspec targets are always reported Staged, never Unchanged:
			// `git status` scoped to a pathspec cannot distinguish "already
			// clean" from "matches nothing at all", and folding a
			// nonexistent path into a silent "nothing to commit" warning
			// would swallow git add's own fatal "did not match any files"
			// error -- exactly the kind of divergence AGENTS.md's governing
			// principle forbids. `git add` is left to answer both cases
			// itself, at apply time, the way plain git would.
			added, deleted := pathspecLineCounts(ctx, repo, root, t.Pathspec)
			plan.results = append(plan.results, TargetResult{
				Target:  t,
				Outcome: Staged,
				Added:   added,
				Deleted: deleted,
				path:    t.Pathspec,
			})
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

		op, unchanged, tsOnly, err := fp.classify(ctx, sess, root, t.Symbol.Anchor)
		if err != nil {
			return nil, err
		}
		fp.ops = append(fp.ops, op)
		if tsOnly {
			plan.tsOnly = true
		}
		if named[fp.path] == nil {
			named[fp.path] = map[string]bool{}
		}
		named[fp.path][t.Symbol.Anchor] = true
		if isOrdinalAnchor(t.Symbol.Anchor) {
			plan.ordinals = append(plan.ordinals, fp.path+":"+t.Symbol.Anchor)
		}
		outcome := Staged
		if unchanged {
			outcome = Unchanged
		}
		added, deleted := opLineCounts(fp, op)
		plan.results = append(plan.results, TargetResult{
			Target:  t,
			Outcome: outcome,
			Added:   added,
			Deleted: deleted,
			path:    t.Symbol.Path,
			start:   op.start,
		})
	}

	for _, fp := range plan.files {
		if fp.addPreamble(named[fp.path]) {
			plan.preamble = append(plan.preamble, fp.path)
		}
		for _, e := range fp.escalated {
			plan.escalated = append(plan.escalated, fp.path+":"+e)
		}
	}

	sortResults(plan.results)
	return plan, nil
}

// addPreamble stages @header and @imports for a file that does not exist in
// HEAD (docs/ANCHORS.md). Synthesizing only the symbols the caller named
// would write a blob holding a bare declaration with no package clause and
// no imports -- valid as an extent, but not as a file.
//
// Either region legitimately resolves to nothing: TypeScript has no header
// without a shebang, and a file need not import anything. TODO.md records
// that callers must tolerate the absence, so an unresolvable one is skipped
// rather than failing the commit.
//
// Ordering needs no special case: seq is the region's own worktree offset,
// the same rule insertionPoint uses, so the header sorts ahead of the
// imports and both ahead of every declaration by construction.
func (fp *filePlan) addPreamble(named map[string]bool) (added bool) {
	if fp.headExists || !fp.workExists {
		return false
	}
	for _, pseudo := range []string{"@header", "@imports"} {
		if named[pseudo] {
			continue
		}
		res, err := fp.workFile.Resolve(pseudo)
		if err != nil {
			continue
		}
		fp.ops = append(fp.ops, editOp{
			kind:  editInsert,
			start: 0,
			seq:   int(res.Extent.Start),
			text:  append([]byte(nil), fp.workSrc[res.Extent.Start:res.Extent.End]...),
		})
		added = true
	}
	return added
}

// isOrdinalAnchor reports whether anchor uses docs/ANCHORS.md's positional
// "Bare#N" form. No identifier in a supported grammar contains "#", so a
// suffix that parses as a number is unambiguous.
func isOrdinalAnchor(anchor string) bool {
	bare, ordinal, ok := strings.Cut(anchor, "#")
	if !ok || bare == "" {
		return false
	}
	n, err := strconv.Atoi(ordinal)
	return err == nil && n > 0
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
	workSrc, workExists, err := util.ReadFileIfExists(filepath.Join(root, path))
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
	if headExists {
		if fp.headFile, err = resolve.Open(lang, headSrc); err != nil {
			return nil, err
		}
	}
	if workExists {
		if fp.workFile, err = resolve.Open(lang, workSrc); err != nil {
			fp.close()
			return nil, err
		}
		fp.workOrder = fp.workFile.DeclOrder()
	}
	return fp, nil
}

// close releases both parse trees. Extents already resolved out of them are
// plain byte offsets, so anything the plan is holding stays valid.
func (fp *filePlan) close() {
	fp.headFile.Close()
	fp.workFile.Close()
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
		content := inheritEOF(fp, applyEdits(fp.headSrc, fp.ops))

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

// inheritEOF supplies the trailing newline for a file with no HEAD blob to
// inherit one from. AGENTS.md's rule is that EOF newline is inherited and
// never normalized; for a file that exists only in the worktree, the
// worktree file is the only thing there is to inherit from. Splicing alone
// cannot know that -- it never manufactures a trailing newline -- so a new
// file would otherwise land with git's "\ No newline at end of file" against
// a worktree that plainly has one.
func inheritEOF(fp *filePlan, content []byte) []byte {
	if fp.headExists || !fp.workExists || len(content) == 0 {
		return content
	}
	nl := []byte("\n")
	if bytes.HasSuffix(fp.workSrc, nl) && !bytes.HasSuffix(content, nl) {
		return append(content, '\n')
	}
	return content
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
		return util.GitFileMode(info), nil
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

// opLineCounts reports how many lines one resolved edit adds and removes,
// using the same counter internal/diff renders with so a --dry-run preview
// and `rgit diff` cannot disagree about the same symbol.
func opLineCounts(fp *filePlan, op editOp) (added, deleted int) {
	var old []byte
	switch op.kind {
	case editReplace, editDelete:
		if int(op.end) <= len(fp.headSrc) && op.start <= op.end {
			old = fp.headSrc[op.start:op.end]
		}
	}
	return diff.LineCounts(old, op.text)
}

// pathspecLineCounts totals a whole pathspec's change, so a --dry-run preview
// reports the same +N/-M for a path target that `rgit diff` does. Scoped to
// HEAD rather than the index because that is what rgit commit would pick up:
// staged and unstaged together.
//
// A numstat against HEAD says nothing about a file git does not track yet, so
// an untracked path falls back to counting its lines as pure additions --
// which is how `rgit diff` reports an UNTRACKED row.
func pathspecLineCounts(ctx context.Context, repo *gitx.Repo, root, pathspec string) (added, deleted int) {
	if entries, err := repo.DiffNumstat(ctx, "HEAD", "--", pathspec); err == nil {
		for _, e := range entries {
			// git writes "-" for both counts on a binary file; there are no
			// lines to report and nothing to sum.
			if e.Added == "-" || e.Deleted == "-" {
				continue
			}
			a, aerr := strconv.Atoi(e.Added)
			d, derr := strconv.Atoi(e.Deleted)
			if aerr == nil && derr == nil {
				added += a
				deleted += d
			}
		}
	}
	if added != 0 || deleted != 0 {
		return added, deleted
	}

	others, err := repo.LsFilesOthers(ctx, "--", pathspec)
	if err != nil {
		return added, deleted
	}
	for _, rel := range others {
		content, rerr := os.ReadFile(filepath.Join(root, rel))
		if rerr != nil {
			continue
		}
		a, _ := diff.LineCounts(nil, content)
		added += a
	}
	return added, deleted
}

// sortResults orders targets alphabetically by file, then ascending by
// position within that file, with the anchor name as a final tiebreak so the
// order is total and every run of an unchanged tree prints the same thing.
func sortResults(results []TargetResult) {
	slices.SortStableFunc(results, func(a, b TargetResult) int {
		return cmp.Or(
			cmp.Compare(a.path, b.path),
			cmp.Compare(a.start, b.start),
			cmp.Compare(a.Target.Symbol.Anchor, b.Target.Symbol.Anchor),
		)
	})
}
