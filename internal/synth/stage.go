package synth

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

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

	// Path is the file this row reports on, and the label a caller sees.
	// For a symbol anchor it is the anchored file. For a pathspec it is
	// one of the files that pathspec stages -- a directory or glob
	// produces one result per file, so a listing breaks down the same way
	// `rgit diff` does rather than collapsing to a single total nobody can
	// act on. It falls back to the pathspec itself when nothing matched.
	//
	// Path and start order the results: alphabetical by file, then
	// ascending by position within it, matching `rgit diff` and `git
	// status`. Listing targets in the order the caller happened to name
	// them makes output unstable between runs and awkward to grep.
	Path  string
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
	// worktreeExists is the worktree copy specifically. Index-backed
	// current bytes still set workExists so anchors can resolve, but
	// resolveMode must not os.Stat a path that is only in the index.
	worktreeExists bool
	workOrder      []string // qualified anchor names in worktree declaration order
	ops            []editOp

	// headFile and workFile are the two sources parsed once and held open;
	// every anchor in this file resolves against them rather than re-parsing.
	// Either is nil when that side has no such file. close() releases both.
	headFile  *resolve.File
	workFile  *resolve.File
	escalated []string // member anchors widened to their enclosing container

	// pendingCrossCheck accumulates every worktree resolution classify
	// resolved for this file that still needs verifying against a live
	// language server (deferCrossCheck). crossCheckPending drains it in one
	// batched query per file instead of classify dialing once per anchor --
	// N documentSymbol round trips for N anchors in one file, where
	// internal/diff's own crossCheckFile already pays one.
	pendingCrossCheck []*resolve.Resolution
}

// Plan is a resolved, not-yet-applied stage: every target has been
// classified and cross-checked -- a pure read -- but nothing has touched
// the git index or written an object yet. A caller that needs to inspect
// what resolution found before writing anything at all (rgit commit's
// --dry-run, and its "every named target already matches HEAD" exit-11
// rule) reads the fields below, decides, and calls Apply second.
type Plan struct {
	pathspecs []string
	files     []*filePlan

	// Results reports what resolution found for each target, in the order
	// PlanStage received them.
	Results []TargetResult

	// TSOnly reports whether any anchor degraded to tree-sitter-only
	// resolution because no live language server answered in time for its
	// cross-check -- normal, not an error, but the
	// caller's job to announce once on stderr.
	TSOnly bool

	// Preamble lists files whose @header/@imports were staged for them
	// because the file is new -- docs/ANCHORS.md's "both are staged
	// automatically for an untracked file", without which the synthesized
	// blob is a bare function body with no package clause. Ordinals lists
	// anchors that resolved by position ("init#2") rather than by a unique
	// or container-qualified name, a last resort because an inserted symbol
	// repoints it. Escalated lists member anchors widened to their
	// enclosing container because HEAD has neither -- a method cannot be
	// added to a class that does not exist yet. All three are the caller's
	// to announce on stderr; synth writes to no stream of its own.
	Preamble  []string
	Ordinals  []string
	Escalated []string

	// CountingWarnings holds one message per pathspec whose --dry-run line
	// counts could not be fully computed. The commit itself does not
	// depend on them -- Apply stages a pathspec via plain `git add`
	// regardless -- so a counting failure never fails the plan; it would
	// only make a preview understate its own totals with nothing to say
	// so, which is what this exists to prevent.
	CountingWarnings []string
}

// PlanStage resolves every target against repo's worktree (root) and HEAD
// without writing anything -- the pure read that must succeed for all of
// them before Apply writes any synthesized blob, so a failure leaves the
// index exactly as found (AGENTS.md's invariant table). A caller that never calls the returned Plan's Apply (a
// --dry-run preview, or the "nothing to commit" exit-11 case) never touches
// the index at all.
func PlanStage(ctx context.Context, repo *gitx.Repo, root string, targets []Target) (*Plan, error) {
	plan := &Plan{}
	byPath := map[string]*filePlan{}
	// Anchors the caller named per path, so the new-file preamble pass below
	// does not stage a second copy of one they asked for themselves.
	named := map[string]map[string]bool{}

	// sess is created lazily, on the first symbol target: a pathspec-only
	// commit never resolves an anchor, so it never needs a language server.
	// lsp.Session's own Dial and Close are both nil-receiver safe, so a
	// plan that never assigns sess still cleans up correctly, and a dial
	// failure partway through a multi-target plan still closes whatever
	// was already cached before returning.
	var sess *lsp.Session
	// A closure, not defer sess.Close(): the latter binds the receiver at
	// this defer statement, which is nil here -- a symbol target reassigns
	// sess below, and the deferred call must see that assignment.
	defer func() { sess.Close() }()
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
			// One row per file, not one per pathspec: naming a directory
			// stages every file under it, and a single summed total says
			// nothing about which. `rgit diff` already breaks the same
			// change down this way, and the two are supposed to agree.
			files, warnings := pathspecFileCounts(ctx, repo, root, t.Pathspec)
			plan.CountingWarnings = append(plan.CountingWarnings, warnings...)
			if len(files) == 0 {
				// Nothing matched, or nothing changed. Keep one row naming
				// the pathspec as given: it is still being staged, and the
				// "did not match any files" answer is git add's to give.
				plan.Results = append(plan.Results, TargetResult{
					Target:  t,
					Outcome: Staged,
					Path:    t.Pathspec,
				})
				continue
			}
			for _, pf := range files {
				plan.Results = append(plan.Results, TargetResult{
					Target:  t,
					Outcome: Staged,
					Added:   pf.added,
					Deleted: pf.deleted,
					Path:    pf.path,
				})
			}
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

		if sess == nil {
			sess = lsp.NewSession()
		}
		op, unchanged, err := fp.classify(t.Symbol.Anchor)
		if err != nil {
			return nil, err
		}
		fp.ops = append(fp.ops, op)
		if named[fp.path] == nil {
			named[fp.path] = map[string]bool{}
		}
		named[fp.path][t.Symbol.Anchor] = true
		if isOrdinalAnchor(t.Symbol.Anchor) {
			plan.Ordinals = append(plan.Ordinals, fp.path+":"+t.Symbol.Anchor)
		}
		outcome := Staged
		if unchanged {
			outcome = Unchanged
		}
		added, deleted := opLineCounts(fp, op)
		plan.Results = append(plan.Results, TargetResult{
			Target:  t,
			Outcome: outcome,
			Added:   added,
			Deleted: deleted,
			Path:    t.Symbol.Path,
			start:   op.start,
		})
	}

	for _, fp := range plan.files {
		// Ahead of the cross-check, so a sibling pulled in here is verified
		// against the language server in the same batched query its anchor is.
		siblings, err := fp.addReferencedSiblings(named[fp.path])
		if err != nil {
			return nil, err
		}
		plan.addAutoResults(fp, siblings)

		// One batched language-server query per file, not one per anchor --
		// every anchor named in this file has already been
		// resolved and its op built above, so a mismatch here still aborts
		// the whole plan before Apply ever runs.
		tsOnly, err := fp.crossCheckPending(ctx, sess, root)
		if err != nil {
			return nil, err
		}
		if tsOnly {
			plan.TSOnly = true
		}

		pseudos := fp.addPreamble(named[fp.path])
		plan.addAutoResults(fp, pseudos)
		if len(pseudos) > 0 {
			plan.Preamble = append(plan.Preamble, fp.path)
		}
		for _, e := range fp.escalated {
			plan.Escalated = append(plan.Escalated, fp.path+":"+e)
		}
	}

	sortResults(plan.Results)
	return plan, nil
}

// autoOp pairs one anchor staged without the caller naming it -- a new
// file's "@header"/"@imports", or a new sibling an anchored symbol
// references -- with the editOp appended for it, so the caller can report the
// same magnitude it just staged rather than a bare file name.
type autoOp struct {
	name string
	op   editOp
}

// addAutoResults turns auto-staged anchors into their own result rows.
// Rolling their line counts into whichever symbol the caller named would
// misattribute bytes to a symbol that never touched them, and docs/USAGE.md
// requires a --dry-run preview and the commit it previews to be comparable
// line for line.
func (p *Plan) addAutoResults(fp *filePlan, autos []autoOp) {
	for _, a := range autos {
		added, deleted := opLineCounts(fp, a.op)
		p.Results = append(p.Results, TargetResult{
			Target:  AnchorTarget(fp.path, a.name),
			Outcome: Staged,
			Added:   added,
			Deleted: deleted,
			Path:    fp.path,
			start:   a.op.start,
		})
	}
}

// addPreamble stages @header and @imports for a file that does not exist in
// HEAD (docs/ANCHORS.md). Synthesizing only the symbols the caller named
// would write a blob holding a bare declaration with no package clause and
// no imports -- valid as an extent, but not as a file.
//
// Either region legitimately resolves to nothing: TypeScript has no header
// without a shebang, and a file need not import anything.
// docs/ANCHORS.md § Pseudo-anchors records that callers must tolerate the
// absence, so an unresolvable one is skipped rather than failing the commit.
//
// Ordering needs no special case: seq is the region's own worktree offset,
// the same rule insertionPoint uses, so the header sorts ahead of the
// imports and both ahead of every declaration by construction.
//
// Each pseudo-anchor's extent absorbs its mandatory trailing separator where
// the language owns one (resolve.OwnsTrailingSeparator): gofmt always leaves
// one blank line after Go's package clause and import block, so that line is
// as much part of "the header" as its own trailing newline -- which is what
// lets these rows sum to git's raw insertion count for a new file.
// The synthesized blob is unaffected
// either way: mergeInsertTies' joinWithSeparator renormalizes every insert's
// boundary regardless.
func (fp *filePlan) addPreamble(named map[string]bool) (pairs []autoOp) {
	if fp.headExists || !fp.workExists {
		return nil
	}

	// firstDeclStart bounds @imports' (or, absent @imports, @header's) own
	// trailing separator: the start of the first real declaration, or EOF
	// when the new file has none yet.
	firstDeclStart := uint(len(fp.workSrc))
	if len(fp.workOrder) > 0 {
		if res, err := fp.workFile.Resolve(fp.workOrder[0]); err == nil {
			firstDeclStart = res.Extent.Start
		}
	}
	// @header's own boundary is @imports' start when the file has imports,
	// else the same first declaration (or EOF).
	headerLimit := firstDeclStart
	if importsRes, err := fp.workFile.Resolve("@imports"); err == nil {
		headerLimit = importsRes.Extent.Start
	}
	limits := map[string]uint{"@header": headerLimit, "@imports": firstDeclStart}

	for _, pseudo := range []string{"@header", "@imports"} {
		if named[pseudo] {
			continue
		}
		res, err := fp.workFile.Resolve(pseudo)
		if err != nil {
			continue
		}
		ext := resolve.ExtendThroughOwnedSeparator(fp.lang, fp.workSrc, res.Extent, limits[pseudo])
		op := editOp{
			kind:  editInsert,
			start: 0,
			seq:   int(res.Extent.Start),
			text:  append([]byte(nil), fp.workSrc[ext.Start:ext.End]...),
		}
		fp.ops = append(fp.ops, op)
		pairs = append(pairs, autoOp{name: pseudo, op: op})
	}
	return pairs
}

// isOrdinalAnchor reports whether anchor uses docs/ANCHORS.md's positional
// "Bare#N" form (n > 0, non-empty bare), delegating to resolve.ParseOrdinal,
// the one parse of this shape.
func isOrdinalAnchor(anchor string) bool {
	_, _, ok := resolve.ParseOrdinal(anchor)
	return ok
}

// openFilePlan performs every pure read a file's anchors need before any
// of them can be classified: the gitignore/special-path refusal checks,
// language lookup, HEAD and worktree content, and (when the worktree has
// the file) its declaration order for nearest-sibling insertion.
func openFilePlan(ctx context.Context, repo *gitx.Repo, root, path string) (*filePlan, error) {
	if err := checkGitignoreRefusal(ctx, repo, path); err != nil {
		return nil, err
	}
	ignoreCase, err := repo.IgnoreCase(ctx)
	if err != nil {
		return nil, err
	}

	kind, err := classifyPath(ctx, repo, root, path)
	if err != nil {
		return nil, err
	}
	if err := refusalFor(path, kind); err != nil {
		return nil, err
	}

	workSrc, worktreeExists, err := util.ReadFileIfExists(filepath.Join(root, path))
	if err != nil {
		return nil, err
	}
	headSrc, headExists, err := repo.CatFile(ctx, "HEAD", path)
	if err != nil {
		return nil, err
	}
	workExists := worktreeExists
	if !workExists && !headExists {
		indexSrc, indexExists, err := repo.CatFile(ctx, "", path)
		if err != nil {
			return nil, err
		}
		if indexExists {
			if len(indexSrc) == 0 {
				return nil, &PathError{
					Code:   exitcode.SpecialPathRefused,
					Path:   path,
					Reason: "worktree file is missing and the index contains an empty intent-to-add blob; restore the worktree file",
				}
			}
			workSrc, workExists = indexSrc, true
		}
	}

	ext := filepath.Ext(path)
	// Extension lookup found nothing; a worktree copy may still carry a
	// recognizable "#!" interpreter line -- the case an extensionless git
	// hook or bin/ entry is in. When that copy is absent, the shared resolver
	// samples HEAD instead, so a deleted script can still resolve its anchors.
	lang, ok, shebangSniffed, langErr := resolve.LanguageForPathFolding(root, path, ignoreCase, func() ([]byte, bool, error) {
		if workExists {
			sample := workSrc
			if len(sample) > resolve.ShebangPeekBytes {
				sample = sample[:resolve.ShebangPeekBytes]
			}
			return sample, true, nil
		}
		return repo.CatFileSample(ctx, "HEAD", path, resolve.ShebangPeekBytes)
	})
	if langErr != nil {
		return nil, langErr
	}
	if !ok {
		return nil, &PathError{Code: exitcode.UnsupportedLanguage, Path: path, Reason: unsupportedLanguageReason(path, ext, shebangSniffed)}
	}

	fp := &filePlan{
		path:           path,
		lang:           lang,
		headSrc:        headSrc,
		headExists:     headExists,
		workSrc:        workSrc,
		workExists:     workExists,
		worktreeExists: worktreeExists,
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

// unsupportedLanguageReason builds openFilePlan's exit-9 PathError message.
// An extensionless path (a git hook, a bin/ entry) or an unmapped shebang
// leaves ext == ""; this names the path plainly and says which of the two
// lookups actually ran.
func unsupportedLanguageReason(path, ext string, shebangSniffed bool) string {
	shebang := "no worktree file to sniff a shebang from"
	if shebangSniffed {
		shebang = "shebang unmapped"
	}
	return fmt.Sprintf("no grammar registered for %s (extension %q, %s)", path, ext, shebang)
}

// close releases both parse trees. Extents already resolved out of them are
// plain byte offsets, so anything the plan is holding stays valid.
func (fp *filePlan) close() {
	fp.headFile.Close()
	fp.workFile.Close()
}

// Apply is the plan's only side-effecting step: pathspecs delegate to one
// `git add`, and every file's synthesized blob is written and staged through
// its own hash-object + update-index pair. PlanStage already resolved every
// target, so a resolution failure never reaches here.
//
// Staging is atomic at the index level. Every index write goes to a temp
// index file seeded as a byte copy of the caller's own, and the temp is
// swapped over the caller's index only after the last write succeeds -- so
// an I/O failure mid-loop (a full disk, a permission race, a worktree file
// deleted between resolution and staging) leaves the caller's index exactly
// as found instead of leaving earlier files staged. Only index entries
// move; no worktree file is ever written.
//
// A filePlan whose every op is Unchanged is not skipped, even though its
// blob is byte-identical to fp.headSrc. `git add path` re-stages path's
// current bytes unconditionally whenever a caller names it, so a
// "skip if identical" case would special-case rgit away from the tool it
// matches. It would also carve out a content-dependent exception for a path
// with different content already staged from outside this invocation: today
// naming any anchor there collapses its index entry back to
// HEAD-plus-named-anchors, the same as every other named path.
func (p *Plan) Apply(ctx context.Context, repo *gitx.Repo, root string) error {
	if len(p.pathspecs) == 0 && len(p.files) == 0 {
		return nil
	}
	staging, err := newTempStaging(ctx, root)
	if err != nil {
		return err
	}
	swapped := false
	defer func() {
		if !swapped {
			staging.abort()
		}
	}()
	if len(p.pathspecs) > 0 {
		if err := staging.repo.Add(ctx, p.pathspecs...); err != nil {
			return err
		}
	}
	for _, fp := range p.files {
		content := inheritEOF(fp, applyEdits(fp.headSrc, fp.ops))

		// Reads go to the caller's repo: its index is the pre-staging
		// state the temp copy was seeded from, and no write below touches
		// another path's entry, so per-path answers agree either way.
		mode, err := resolveMode(ctx, repo, root, fp.path, fp.worktreeExists)
		if err != nil {
			return err
		}
		// hash-object writes an object, never the index, so it is safe on
		// either repo; only the update-index below must target the temp.
		sha, err := repo.HashObject(ctx, fp.path, content)
		if err != nil {
			return err
		}
		if err := staging.repo.UpdateIndexCacheinfo(ctx, mode, sha, fp.path); err != nil {
			return err
		}
	}
	if err := staging.swap(); err != nil {
		return err
	}
	swapped = true
	return nil
}

// tempStaging is an Apply in progress: a temp index file seeded from the
// caller's own, plus a Repo pointed at it. abort discards the temp (the
// caller's index was never touched); swap moves it over the caller's index.
type tempStaging struct {
	repo  *gitx.Repo
	tmp   string
	final string
	mode  os.FileMode
}

// newTempStaging seeds a temp index file beside the caller's own (same
// directory, so the later swap is an atomic rename) as a byte copy of it,
// and returns a Repo writing to the temp. A caller with no index file yet
// -- a fresh repository with nothing staged -- leaves the temp missing too,
// so git creates it on first write exactly as it would the real one.
func newTempStaging(ctx context.Context, root string) (*tempStaging, error) {
	final, err := callerIndexPath(ctx, root)
	if err != nil {
		return nil, err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(final), "rgit-index-*")
	if err != nil {
		return nil, err
	}
	tmp := tmpFile.Name()
	seed, err := os.ReadFile(final)
	switch {
	case err == nil:
		if _, err := tmpFile.Write(seed); err != nil {
			if cerr := tmpFile.Close(); cerr != nil {
				_ = os.Remove(tmp)
				return nil, cerr
			}
			_ = os.Remove(tmp)
			return nil, err
		}
		if info, serr := os.Stat(final); serr == nil {
			// Best effort: the swap below renames, which preserves the
			// temp's own mode, so matching it to the original keeps the
			// caller's index permissions stable across a stage.
			_ = tmpFile.Chmod(info.Mode())
		}
		if err := tmpFile.Close(); err != nil {
			_ = os.Remove(tmp)
			return nil, err
		}
		// git trusts a matching stat only for entries older than the index
		// file's own mtime (racy-git). A copy stamped "now" would make an
		// edit that kept size and mtime tick read as clean, so `git add`
		// stages nothing; carrying the original mtime keeps git's check.
		if info, serr := os.Stat(final); serr == nil {
			if err := os.Chtimes(tmp, time.Time{}, info.ModTime()); err != nil {
				_ = os.Remove(tmp)
				return nil, err
			}
		}
	case os.IsNotExist(err):
		if err := tmpFile.Close(); err != nil {
			_ = os.Remove(tmp)
			return nil, err
		}
		if err := os.Remove(tmp); err != nil {
			return nil, err
		}
	default:
		if cerr := tmpFile.Close(); cerr != nil {
			_ = os.Remove(tmp)
			return nil, cerr
		}
		_ = os.Remove(tmp)
		return nil, err
	}
	var mode os.FileMode = 0o644
	if info, serr := os.Stat(final); serr == nil {
		mode = info.Mode()
	}
	// Repo carries env; construction must not mutate the process.
	// callerIndexPath still reads process GIT_INDEX_FILE when the caller
	// set it -- that is git's own contract. Only the temp-index pointer
	// lives on the Repo.
	tr := gitx.New(root).WithIndexFile(tmp)
	return &tempStaging{repo: tr, tmp: tmp, final: final, mode: mode}, nil
}

// swap moves the staged temp index over the caller's index. Both live in
// the same directory, so a rename does it atomically; the byte copy is only
// a cross-device fallback that same-directory placement already rules out.
// A temp nothing was ever written to -- e.g. Add's already-staged no-op
// path -- means the caller's index is already exactly right and there is
// nothing to move.
func (s *tempStaging) swap() error {
	if _, err := os.Stat(s.tmp); os.IsNotExist(err) {
		return nil
	}
	if err := os.Rename(s.tmp, s.final); err == nil {
		return nil
	} else if data, rerr := os.ReadFile(s.tmp); rerr != nil {
		return rerr
	} else if werr := os.WriteFile(s.final, data, s.mode); werr != nil {
		return werr
	} else {
		_ = os.Remove(s.tmp)
		return nil
	}
}

// abort discards the temp index after a staging failure. The caller's index
// was never written, so removing the temp is the whole rollback.
func (s *tempStaging) abort() {
	_ = os.Remove(s.tmp)
}

// callerIndexPath resolves the index file the caller's git invocations use:
// $GIT_INDEX_FILE when set (a relative value resolves against root, the
// directory every git invocation here runs from), else <gitdir>/index. The
// git directory comes from git itself rather than assuming root/.git, so
// linked worktrees, submodules, and GIT_DIR overrides all resolve to the
// file git would actually use.
//
// This shells out to one read-only `git rev-parse` probe. Package gitx is
// otherwise the sole place that execs git; the probe lives here because
// gitx exposes no index-path accessor, and it delegates no git behaviour --
// it only asks git where its own file is, so staging and rollback can treat
// that file opaquely.
func callerIndexPath(ctx context.Context, root string) (string, error) {
	if v, ok := os.LookupEnv("GIT_INDEX_FILE"); ok && v != "" {
		if filepath.IsAbs(v) {
			return v, nil
		}
		return filepath.Join(root, v), nil
	}
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--absolute-git-dir").Output() //nolint:gosec // root is rgit's selected repository path, passed as git -C argv rather than shell input
	if err != nil {
		return "", err
	}
	return filepath.Join(strings.TrimSpace(string(out)), "index"), nil
}

// IndexSnapshot is a byte copy of the caller's index file, taken so a later
// failure (a hook rejecting the commit) can put the index back exactly as
// found. Data is nil when no index file existed; restoring that removes the
// file again rather than leaving an empty one behind.
type IndexSnapshot struct {
	path    string
	data    []byte
	mode    os.FileMode
	existed bool
}

// SnapshotIndex copies the caller's current index file. Pure read: it never
// writes the index, the temp, or any worktree file.
func SnapshotIndex(ctx context.Context, root string) (*IndexSnapshot, error) {
	path, err := callerIndexPath(ctx, root)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &IndexSnapshot{path: path}, nil
		}
		return nil, err
	}
	mode := os.FileMode(0o644)
	if info, serr := os.Stat(path); serr == nil {
		mode = info.Mode()
	}
	return &IndexSnapshot{path: path, data: data, mode: mode, existed: true}, nil
}

// Restore writes the snapshot back over the current index file, or removes
// the file when none existed at snapshot time. Index-only, like everything
// else here: no worktree byte moves.
func (s *IndexSnapshot) Restore() error {
	if s == nil {
		return nil
	}
	if !s.existed {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.WriteFile(s.path, s.data, s.mode); err != nil {
		return err
	}
	return os.Chmod(s.path, s.mode)
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
// os.Stat's executable bit when the worktree has the file, otherwise the
// index stage-0 mode, otherwise HEAD. A deleted worktree copy has no
// entry to stat (AGENTS.md's mode-inheritance invariant).
func resolveMode(ctx context.Context, repo *gitx.Repo, root, path string, worktreeExists bool) (string, error) {
	if worktreeExists {
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil {
			return "", err
		}
		return util.GitFileMode(info), nil
	}

	mode, found, err := repo.LsFilesStage(ctx, path)
	if err != nil {
		return "", err
	}
	if found {
		return mode, nil
	}

	entry, found, err := repo.LsTree(ctx, "HEAD", path)
	if err != nil {
		return "", err
	}
	if !found {
		// Defensive, and deliberately untested: reaching here means the
		// path is absent from the worktree AND from HEAD, but the only
		// caller that passes workExists=false is staging a deletion, which
		// classify only produces when the symbol resolved against HEAD --
		// so HEAD had the file. A concurrent rewrite of HEAD between
		// resolution and staging is the sole way in, which no test can
		// stage without racing the same window.
		return "", fmt.Errorf("synth: %s: no HEAD entry to derive mode for a deleted worktree file", path)
	}
	return entry.Mode, nil
}

// opLineCounts reports how many lines one resolved edit adds and removes,
// using the same counter internal/diff renders with so a --dry-run preview
// and `rgit diff` cannot disagree about the same symbol.
//
// A whole-region insert or delete also moves the blank line separating that
// region from its neighbour -- spliceInsert pads one in, spliceExcise
// collapses one out -- so the count includes it, by the same rule
// internal/diff attributes it with. Counting the extent alone is what made
// `rgit diff` report a phantom (unanchorable) remainder for a line the
// commit was going to move anyway.
func opLineCounts(fp *filePlan, op editOp) (added, deleted int) {
	var old []byte
	switch op.kind {
	case editReplace, editDelete:
		if int(op.end) <= len(fp.headSrc) && op.start <= op.end {
			old = fp.headSrc[op.start:op.end]
		}
	}
	added, deleted = diff.LineCounts(old, op.text)

	switch op.kind {
	case editDelete:
		deleted += diff.SeparatorLines(fp.headSrc, op.start, op.end, op.member)
	case editInsert:
		// spliceInsert joins onto whatever precedes the insertion point, so
		// there is a separator to add only when something precedes it: the
		// first declaration in an empty file is joined onto nothing.
		if !op.member && len(fp.headSrc) > 0 {
			added++
		}
	}
	return added, deleted
}

// pathFile is one file a pathspec stages, with its own line counts.
type pathFile struct {
	path           string
	added, deleted int
}

// pathspecFileCounts reports every file a pathspec stages and what each one
// changes, so a listing breaks the pathspec down the way `rgit diff` does
// instead of collapsing it to one total. Scoped to HEAD rather than the
// index because that is what rgit commit picks up: staged and unstaged
// together.
//
// A numstat against HEAD says nothing about a file git does not track yet,
// so untracked files are collected separately and counted as pure additions
// -- which is how `rgit diff` reports an UNTRACKED row. Both are included:
// a directory can hold tracked edits and brand new files at once, and
// `git add` stages both.
//
// A binary file is listed with zero counts rather than omitted. It is being
// staged, so leaving it out of the listing would be the more misleading of
// the two answers; git writes "-" for its numstat counts and there are no
// lines to report.
//
// warnings holds one message per query that failed outright -- neither
// query's failure changes what gets staged or committed: Apply stages the
// pathspec via plain `git add`, and --only hands git the pathspec itself to
// expand against the index, so this listing is never the commit list. But a
// preview built on a partial answer must say so rather than presenting an
// understated total as if it were exact.
func pathspecFileCounts(ctx context.Context, repo *gitx.Repo, root, pathspec string) (out []pathFile, warnings []string) {
	seen := map[string]bool{}

	base, err := repo.CommittableBase(ctx)
	var entries []gitx.NumstatEntry
	if err == nil {
		// --no-renames: a rename inside the pathspec is listed as the
		// deletion and the addition it stages, not one row naming the new path.
		entries, err = repo.DiffNumstat(ctx, "--no-renames", base, "--", pathspec)
	}
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("%s: tracked line counts unavailable: %v", pathspec, err))
	}
	for _, e := range entries {
		if seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		a, aerr := strconv.Atoi(e.Added)
		d, derr := strconv.Atoi(e.Deleted)
		if aerr != nil || derr != nil {
			a, d = 0, 0 // binary: git wrote "-" for both
		}
		out = append(out, pathFile{path: e.Path, added: a, deleted: d})
	}

	others, err := repo.LsFilesOthers(ctx, "--", pathspec)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("%s: untracked file counts unavailable: %v", pathspec, err))
		return out, warnings
	}
	for _, rel := range others {
		if seen[rel] {
			continue
		}
		seen[rel] = true
		content, rerr := os.ReadFile(filepath.Join(root, rel))
		if rerr != nil {
			// A transient read failure (permissions, a race with something
			// else removing the file) would otherwise understate the
			// preview's total with no sign that anything was skipped --
			// exactly the silent gap the numstat/ls-files failures above
			// already warn about.
			warnings = append(warnings, fmt.Sprintf("%s: line counts unavailable: %v", rel, rerr))
			continue
		}
		added, deleted := diff.LineCounts(nil, content)
		out = append(out, pathFile{path: rel, added: added, deleted: deleted})
	}
	return out, warnings
}

// sortResults orders targets alphabetically by file, then ascending by
// position within that file, with the anchor name as a final tiebreak so the
// order is total and every run of an unchanged tree prints the same thing.
func sortResults(results []TargetResult) {
	slices.SortStableFunc(results, func(a, b TargetResult) int {
		return cmp.Or(
			cmp.Compare(a.Path, b.Path),
			cmp.Compare(a.start, b.start),
			cmp.Compare(a.Target.Symbol.Anchor, b.Target.Symbol.Anchor),
		)
	})
}
