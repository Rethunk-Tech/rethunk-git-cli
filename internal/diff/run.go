package diff

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/util"
)

// Run resolves opts' scope, enumerates every changed file within it, builds
// each file's report, applies --sym/--file filtering, and returns the
// result in a stable order ready for RenderText or RenderPorcelain.
func Run(ctx context.Context, repo *gitx.Repo, root string, opts Options) (*Report, error) {
	scope, err := ResolveScope(ctx, repo, opts)
	if err != nil {
		return nil, err
	}

	canonicalSyms, err := validateSyms(ctx, repo, root, scope, opts.Syms)
	if err != nil {
		return nil, err
	}

	// One session for the invocation; Dial caches per language inside it, so
	// a repository of many changed files pays for at most one handshake each.
	// Only the worktree side can be cross-checked -- a language server has no
	// view of an arbitrary revision -- so a revision-to-revision diff skips it.
	var sess *lsp.Session
	if scope.New.kind == sideWorktree {
		sess = lsp.NewSession()
		defer sess.Close()
	}

	// git's own "<blob> <blob>" two-argument form (RevPaths' own NumstatArgs)
	// takes no trailing pathspec at all -- unlike every other diff form,
	// its usage string has no "[--] <path>" tail -- and there is nothing
	// to further scope anyway: the two blob refs already name the one file
	// completely. --sym still narrows rendering afterward (applyFilters);
	// only the git-level pathspec is skipped here.
	var pathspecs []string
	if len(opts.RevPaths) != 2 {
		pathspecs = effectivePathspecs(opts)
	}

	entries, err := repo.DiffNumstat(ctx, withPathspecs(scope.NumstatArgs, pathspecs)...)
	if err != nil {
		return nil, err
	}

	report := &Report{}
	if opts.Patch {
		// Same scope.NumstatArgs + pathspecs DiffNumstat above just used, so
		// the patch body is guaranteed to cover the identical comparison and
		// the identical pathspec filter as the symbol table alongside it.
		patch, perr := repo.DiffPatch(ctx, withPathspecs(scope.NumstatArgs, pathspecs)...)
		if perr != nil {
			return nil, perr
		}
		report.Patch = patch
	}
	// A container-escalation notice is only worth printing when --sym is
	// narrowing the listing: with no filter every sibling member already
	// has its own row, so the notice would just repeat what is already
	// visible instead of surfacing something a filtered view would hide.
	symFiltered := len(canonicalSyms) > 0

	oldPaths := make([]string, len(entries))
	newPaths := make([]string, len(entries))
	for i, e := range entries {
		oldPaths[i], newPaths[i] = NumstatPath(e.Path)
	}
	// One batched git-backed blob read for every changed file's Old and New
	// side, instead of buildFileReport's own loop paying for a `git
	// cat-file` subprocess per file per side -- internal/gitx.Repo.
	// BatchCatFile's own doc comment has the batching itself; prefetchBlobs
	// is just this call site's own (side, path) list.
	cache, err := prefetchBlobs(ctx, repo, scope, oldPaths, newPaths)
	if err != nil {
		return nil, err
	}

	trackedFiles, trackedWarnings, trackedTSOnly, err := parallelFileReports(len(entries), func(i int) (*FileReport, []string, bool, error) {
		return buildFileReport(ctx, repo, root, scope, oldPaths[i], newPaths[i], entries[i].Added, entries[i].Deleted, sess, symFiltered, cache)
	})
	if err != nil {
		return nil, err
	}
	report.Files = append(report.Files, trackedFiles...)
	report.Warnings = append(report.Warnings, trackedWarnings...)
	report.TSOnly = report.TSOnly || trackedTSOnly

	if scope.IncludeUntracked {
		untracked, uerr := repo.LsFilesOthers(ctx, withPathspecs(nil, pathspecs)...)
		if uerr != nil {
			return nil, uerr
		}
		untrackedFiles, untrackedWarnings, untrackedTSOnly, uerr := parallelFileReports(len(untracked), func(i int) (*FileReport, []string, bool, error) {
			return buildUntrackedReport(ctx, repo, root, untracked[i], sess, symFiltered)
		})
		if uerr != nil {
			return nil, uerr
		}
		report.Files = append(report.Files, untrackedFiles...)
		report.Warnings = append(report.Warnings, untrackedWarnings...)
		report.TSOnly = report.TSOnly || untrackedTSOnly
	}

	applyFilters(report, canonicalSyms)
	sortReport(report)
	return report, nil
}

// maxConcurrentFileReports bounds how many buildFileReport/buildUntrackedReport
// calls parallelFileReports runs at once. Each one may hold a live LSP round
// trip (crossCheckFile) in flight, so an unbounded fan-out on a commit
// touching hundreds of files would launch hundreds of simultaneous
// subprocess queries against a one-shot server (vtsls, pyright, ...).
// ponytail: a fixed cap, not GOMAXPROCS-scaled -- the work here is I/O-bound
// (LSP round trips, blob reads already prefetched), not CPU-bound, so there
// is no reason to tie it to core count; revisit only if measurement shows a
// wider commit wants more.
const maxConcurrentFileReports = 8

// parallelFileReports runs build(0)..build(n-1) with bounded concurrency and
// merges their results in index order, so the caller's own report ordering
// never depends on which goroutine happened to finish first --
// docs/CODES.md's output-record ordering guarantee is upheld by sortReport
// downstream regardless, but merging in order here keeps this function's own
// behaviour deterministic and its error the same one a sequential loop would
// have returned first.
//
// build's own callees -- resolve.Open (internal/resolve/resolver.go's
// parserCacheMu), the cached blobCache (read-only once prefetchBlobs
// returns), and a cached lsp.Session (internal/lsp/session.go's own mutex)
// -- are each already safe for concurrent use; this function does not gain
// any synchronization from being the caller, it exists only to bound
// fan-out and merge results back into one, ordered set of return values.
func parallelFileReports(n int, build func(i int) (*FileReport, []string, bool, error)) ([]FileReport, []string, bool, error) {
	if n == 0 {
		return nil, nil, false, nil
	}

	type result struct {
		fr       *FileReport
		warnings []string
		tsOnly   bool
		err      error
	}
	results := make([]result, n)

	sem := make(chan struct{}, maxConcurrentFileReports)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fr, warnings, tsOnly, err := build(i)
			results[i] = result{fr: fr, warnings: warnings, tsOnly: tsOnly, err: err}
		}(i)
	}
	wg.Wait()

	var files []FileReport
	var warnings []string
	var tsOnly bool
	for _, r := range results {
		if r.err != nil {
			return nil, nil, false, r.err
		}
		if r.fr != nil {
			files = append(files, *r.fr)
		}
		warnings = append(warnings, r.warnings...)
		tsOnly = tsOnly || r.tsOnly
	}
	return files, warnings, tsOnly, nil
}

// buildFileReport classifies one changed path and dispatches to the right
// row shape: BINARY and MODE short-circuit before any grammar lookup (a
// mode-only change has zero content diff to attribute, and a binary file
// has none to attempt); an unsupported language falls back to the file's
// numstat total under StatusNoSymbols; everything else parses each side
// exactly once and goes through crossCheckFile and attributeSymbolsOpen,
// sharing that one parse of newSrc between them instead of each opening
// its own -- specs/design.md § Blob synthesis' held-parse gain, applied
// here the same way internal/synth already holds one *resolve.File per
// side across every anchor it resolves.
//
// warnings and tsOnly are returned rather than written into a shared
// *Report directly: parallelFileReports runs many of these concurrently, and
// a shared Report's Warnings slice/TSOnly bool would be a data race under
// that fan-out (the sequential loop this replaced could get away with it,
// concurrent callers cannot) -- the caller merges every call's own return
// values back into the one Report once all of them have finished.
func buildFileReport(ctx context.Context, repo *gitx.Repo, root string, scope Scope, oldPath, newPath, addedStr, deletedStr string, sess *lsp.Session, symFiltered bool, cache *blobCache) (fr *FileReport, warnings []string, tsOnly bool, err error) {
	if addedStr == "-" && deletedStr == "-" {
		return &FileReport{Path: newPath, Rows: []Row{{Status: StatusBinary, Added: "-", Deleted: "-"}}}, nil, false, nil
	}

	added, err := strconv.Atoi(addedStr)
	if err != nil {
		return nil, nil, false, fmt.Errorf("diff: malformed numstat added count %q for %s", addedStr, newPath)
	}
	deleted, err := strconv.Atoi(deletedStr)
	if err != nil {
		return nil, nil, false, fmt.Errorf("diff: malformed numstat deleted count %q for %s", deletedStr, newPath)
	}

	if added == 0 && deleted == 0 {
		oldMode, oldFound, merr := scope.Old.mode(ctx, repo, root, oldPath)
		if merr != nil {
			return nil, nil, false, merr
		}
		newMode, newFound, merr := scope.New.mode(ctx, repo, root, newPath)
		if merr != nil {
			return nil, nil, false, merr
		}
		note := formatModeNote(oldMode, oldFound, newMode, newFound)
		return &FileReport{Path: newPath, Rows: []Row{{Status: StatusMode, Added: "0", Deleted: "0", ModeNote: note}}}, nil, false, nil
	}

	ignoreCase, err := repo.IgnoreCase(ctx)
	if err != nil {
		return nil, nil, false, err
	}

	// A worktree copy of newPath may still carry a recognizable "#!" line --
	// an extensionless git hook or bin/ entry, resolve.ForPath's case. When
	// the worktree copy is absent, the shared resolver samples HEAD instead,
	// so deleted extensionless scripts retain their grammar.
	lang, ok, _, langErr := resolve.LanguageForPathFolding(root, newPath, ignoreCase, func() ([]byte, bool, error) {
		return repo.CatFileSample(ctx, "HEAD", newPath, resolve.ShebangPeekBytes)
	})
	if langErr != nil {
		return nil, nil, false, langErr
	}
	if !ok {
		return &FileReport{Path: newPath, Rows: []Row{{Status: StatusNoSymbols, Added: addedStr, Deleted: deletedStr}}}, nil, false, nil
	}

	oldSrc, _, err := scope.Old.read(ctx, repo, root, oldPath, cache)
	if err != nil {
		return nil, nil, false, err
	}
	newSrc, _, err := scope.New.read(ctx, repo, root, newPath, cache)
	if err != nil {
		return nil, nil, false, err
	}

	oldFile, err := resolve.Open(lang, oldSrc)
	if err != nil {
		return nil, nil, false, err
	}
	defer oldFile.Close()
	newFile, err := resolve.Open(lang, newSrc)
	if err != nil {
		return nil, nil, false, err
	}
	defer newFile.Close()

	if sess != nil {
		tsOnly, warnings = crossCheckFile(ctx, sess, lang, root, newPath, newSrc, newFile)
	}

	rows, notices, err := attributeSymbolsOpen(lang, oldSrc, newSrc, oldFile, newFile, added, deleted)
	if err != nil {
		return nil, nil, false, err
	}
	if symFiltered {
		for _, n := range notices {
			warnings = append(warnings, newPath+": "+n)
		}
	}
	if len(rows) == 0 {
		return nil, warnings, tsOnly, nil
	}
	return &FileReport{Path: newPath, Rows: rows, lang: lang.Name()}, warnings, tsOnly, nil
}

// buildUntrackedReport attributes a file git does not track at all by
// symbol, the same MOD/(unanchorable) rows a brand-new tracked file gets
// (buildFileReport's oldSrc==nil path): there is no HEAD blob to diff
// against, so every declared symbol's own extent is wholly new, exactly
// what attributeSymbolsOpen already computes when the old side is empty.
// Counted directly from the worktree file rather than via `git diff
// --no-index`, whose exit-1-on-differences convention (unlike every other
// `git diff` invocation this package makes) would otherwise have to be
// special-cased.
//
// A binary file or one whose language has no grammar keeps the single
// collapsed StatusUntracked row -- there is nothing to split out, and for
// an unsupported language HintSymbol still points a caller at --sym/--file.
//
// warnings and tsOnly are returned rather than written into a shared
// *Report, the same reason buildFileReport's own signature does -- see its
// doc comment.
func buildUntrackedReport(ctx context.Context, repo *gitx.Repo, root, path string, sess *lsp.Session, symFiltered bool) (fr *FileReport, warnings []string, tsOnly bool, err error) {
	content, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return nil, nil, false, err
	}
	if util.LooksBinary(content) {
		return &FileReport{Path: path, Rows: []Row{{Status: StatusUntracked, Added: "-", Deleted: "-"}}}, nil, false, nil
	}

	ignoreCase, err := repo.IgnoreCase(ctx)
	if err != nil {
		return nil, nil, false, err
	}

	// content is already fully read above (needed for the binary check and
	// line count regardless), so ForPath's shebang fallback costs nothing
	// extra here -- unlike the other two call sites, there is no separate
	// bounded peek to reason about.
	lang, ok := resolve.ForPathFolding(path, content, ignoreCase)
	if !ok {
		return &FileReport{Path: path, Rows: []Row{{Status: StatusUntracked, Added: strconv.Itoa(countLines(content)), Deleted: "0"}}}, nil, false, nil
	}

	newFile, err := resolve.Open(lang, content)
	if err != nil {
		return nil, nil, false, err
	}
	defer newFile.Close()
	oldFile, err := resolve.Open(lang, nil)
	if err != nil {
		return nil, nil, false, err
	}
	defer oldFile.Close()

	if sess != nil {
		tsOnly, warnings = crossCheckFile(ctx, sess, lang, root, path, content, newFile)
	}

	rows, notices, err := attributeSymbolsOpen(lang, nil, content, oldFile, newFile, countLines(content), 0)
	if err != nil {
		return nil, nil, false, err
	}
	if symFiltered {
		for _, n := range notices {
			warnings = append(warnings, path+": "+n)
		}
	}
	if len(rows) == 0 {
		// No declarations at all (e.g. a comment-only or empty file): fall
		// back to the collapsed row rather than an empty Rows slice.
		return &FileReport{Path: path, Rows: []Row{{Status: StatusUntracked, Added: strconv.Itoa(countLines(content)), Deleted: "0"}}}, warnings, tsOnly, nil
	}
	return &FileReport{Path: path, Rows: rows, lang: lang.Name()}, warnings, tsOnly, nil
}

// formatModeNote renders "644->755"-shaped mode notes from git's full
// 6-digit modes, using "?" for a side that has no entry at all — reachable
// only if a mode-only numstat row somehow named a path absent from both
// sides, which git itself would not produce.
func formatModeNote(oldMode string, oldFound bool, newMode string, newFound bool) string {
	trim := func(m string) string {
		if len(m) > 3 {
			return m[len(m)-3:]
		}
		return m
	}
	o, n := "?", "?"
	if oldFound {
		o = trim(oldMode)
	}
	if newFound {
		n = trim(newMode)
	}
	return o + "->" + n
}

// NumstatPath parses gitx.NumstatEntry.Path, which may carry git's own
// rename shorthand — a full "old => new" or a common-prefix
// "dir/{old => new}" form — into the two paths content resolution needs.
// For a non-rename entry, old and new are identical.
//
// Exported because internal/synth reads the same numstat output when it
// breaks a pathspec target into per-file rows, and a second parser for
// git's shorthand would be free to disagree about which path a rename
// lands on.
func NumstatPath(raw string) (oldPath, newPath string) {
	if braceStart := strings.Index(raw, "{"); braceStart >= 0 {
		if braceEnd := strings.Index(raw[braceStart:], "}"); braceEnd >= 0 {
			braceEnd += braceStart
			inner := raw[braceStart+1 : braceEnd]
			if before, after, ok := strings.Cut(inner, " => "); ok {
				prefix, suffix := raw[:braceStart], raw[braceEnd+1:]
				return prefix + before + suffix, prefix + after + suffix
			}
		}
	}
	if before, after, ok := strings.Cut(raw, " => "); ok {
		return before, after
	}
	return raw, raw
}

// effectivePathspecs unions --file/bare-pathspec targets with --sym/bare-
// anchor targets' own files, so the git-level query only ever has to look
// at files that could possibly appear in the filtered output.
func effectivePathspecs(opts Options) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, f := range opts.Files {
		add(f)
	}
	for _, s := range opts.Syms {
		add(s.File)
	}
	return out
}

func withPathspecs(args, pathspecs []string) []string {
	if len(pathspecs) == 0 {
		return args
	}
	out := make([]string, 0, len(args)+1+len(pathspecs))
	out = append(out, args...)
	out = append(out, "--")
	out = append(out, pathspecs...)
	return out
}

// validateSyms confirms every --sym/bare-anchor filter opts named actually
// resolves against the applicable side of scope, before any rendering
// happens, and returns each one rewritten to the canonical anchor it
// resolved to. Without the resolve step, a typo in a --sym value was
// indistinguishable from "that symbol is clean": applyFilters only ever
// drops rows that do not match, so a name nothing resolves to and a name
// whose symbol has simply not changed both rendered as the same empty,
// exit-0 output -- inverting the check-before-commit workflow rgit diff
// exists to serve.
//
// The rewrite exists because applyFilters matches against Row.Symbol, which
// is always the canonical spelling rgit itself emits (resolve.DeclOrder) --
// never gopls's "(*A).Get" receiver spelling or a Markdown heading's raw
// text, both of which resolve.Resolve accepts on input but never produces on
// output (docs/ANCHORS.md). Filtering on the caller's literal string left
// those two accepted-on-input spellings resolving successfully (this
// function returns no error) while silently matching zero rows -- the exact
// hazard this function already exists to prevent, just one step later in
// the pipeline.
//
// This applies only to --sym/bare-anchor (rgit's own invention, where git
// has no opinion): a --file/bare-pathspec naming a file that does not exist
// keeps matching plain `git diff -- nosuch.py`'s own silent exit 0
// (AGENTS.md's one invariant).
func validateSyms(ctx context.Context, repo *gitx.Repo, root string, scope Scope, syms []SymRef) ([]SymRef, error) {
	canonical := make([]SymRef, len(syms))
	for i, s := range syms {
		name, err := validateSym(ctx, repo, root, scope, s)
		if err != nil {
			return nil, err
		}
		canonical[i] = SymRef{File: s.File, Name: name}
	}
	return canonical, nil
}

// validateSym resolves one anchor against whichever side of scope actually
// has the file, preferring New (the side a caller is normally asking "what
// changed" about) and falling back to Old for an anchor that only exists on
// a since-deleted side, and returns the canonical anchor it resolved to. A
// file present on neither side reads the same as an empty source: resolving
// any name against it fails exactly the way a real but absent symbol would,
// with the identical "unresolved" message rgit commit already produces for
// the same anchor (internal/resolve.ResolveError), so a missing file and a
// missing symbol need no separate message shape.
func validateSym(ctx context.Context, repo *gitx.Repo, root string, scope Scope, s SymRef) (string, error) {
	ignoreCase, err := repo.IgnoreCase(ctx)
	if err != nil {
		return "", err
	}

	// Same shared shebang fallback as buildFileReport: a --sym anchor
	// naming an extensionless script uses HEAD when its worktree copy is gone.
	lang, ok, _, langErr := resolve.LanguageForPathFolding(root, s.File, ignoreCase, func() ([]byte, bool, error) {
		return repo.CatFileSample(ctx, "HEAD", s.File, resolve.ShebangPeekBytes)
	})
	if langErr != nil {
		return "", langErr
	}
	if !ok {
		// No grammar to resolve against at all -- rgit commit's own exit 9
		// ("unsupported language for a symbol anchor") is the closer match
		// than pretending the name might resolve. Path is set here (and
		// below) so runDiff (internal/app/diff.go) can recover the failed
		// anchor's own file straight from the error, rather than matching
		// ResolveError.Anchor's bare name back against its own --sym list --
		// the fragile lookup that broke whenever two files shared a bare
		// symbol name (resolve.ResolveError's own doc comment).
		return "", &resolve.ResolveError{Code: exitcode.UnsupportedLanguage, Anchor: s.Name, Path: s.File}
	}

	// Unbatched: this validates the (typically few) explicit --sym targets
	// before the main file loop even runs, not once per changed file, so
	// there is nothing here worth batching the way buildFileReport's own
	// loop is (prefetchBlobs, above).
	src, exists, err := scope.New.read(ctx, repo, root, s.File, nil)
	if err != nil {
		return "", err
	}
	if !exists {
		src, _, err = scope.Old.read(ctx, repo, root, s.File, nil)
		if err != nil {
			return "", err
		}
	}

	res, err := resolve.Resolve(lang, src, s.Name)
	if err != nil {
		// resolve.Resolve's own *ResolveError construction sites have no
		// path argument to attach one from; validateSym is the one place
		// that path is in scope for this particular failure, so it is
		// filled in here rather than left for every caller of Resolve to
		// do without.
		if rerr, ok := errors.AsType[*resolve.ResolveError](err); ok {
			rerr.Path = s.File
		}
		return "", err
	}
	return res.Anchor, nil
}

// applyFilters implements docs/USAGE.md's "--sym and --file filter output
// to specific targets; when filtered by --sym, (unanchorable) hunks in
// that file are omitted." A --file-only filter needs no Go-side pass at
// all: it already scoped the git-level query, so every row of every
// surviving file is shown, unanchorable included.
//
// syms must already be canonical (validateSyms' return value, never
// opts.Syms directly): Row.Symbol is always rgit's own emitted spelling, and
// matching it against a caller's raw input would silently drop every row for
// an accepted-on-input alias (docs/ANCHORS.md's gopls and Markdown
// raw-heading spellings).
func applyFilters(report *Report, syms []SymRef) {
	if len(syms) == 0 {
		return
	}
	want := map[string]map[string]bool{}
	for _, s := range syms {
		if want[s.File] == nil {
			want[s.File] = map[string]bool{}
		}
		want[s.File][s.Name] = true
	}

	kept := report.Files[:0]
	for _, f := range report.Files {
		names, ok := want[f.Path]
		if !ok {
			continue
		}
		var rows []Row
		for _, r := range f.Rows {
			if r.Symbol != "" && names[r.Symbol] {
				rows = append(rows, r)
			}
		}
		if len(rows) > 0 {
			f.Rows = rows
			kept = append(kept, f)
		}
	}
	report.Files = kept
}

// sortReport orders files by path, the "stable ordering" docs/USAGE.md §
// Output promises for --porcelain. Tracked and untracked files are
// discovered via two independent git calls with no shared ordering
// guarantee, so this is the one place that ordering is actually decided.
func sortReport(report *Report) {
	slices.SortFunc(report.Files, func(a, b FileReport) int { return cmp.Compare(a.Path, b.Path) })
}

// crossCheckFile verifies every declaration rgit would emit for one file
// against a live language server, in one query. It returns messages, never
// an error: rgit diff is a read-only report, and a server that is absent,
// slow or simply silent about a symbol is the normal case the whole
// resolution model is built to tolerate (specs/design.md).
//
// f is newSrc already parsed by buildFileReport's own call to resolve.Open
// -- this function does not open its own; a parse failure is buildFileReport's
// to report (it already fails loudly there, on the very next parse of the
// same file for attribution), not something crossCheckFile silently
// downgraded to "not degraded" while a sibling call moments later hit the
// identical failure as a hard error.
//
// degraded reports that this file had declarations to verify and none were,
// which the caller surfaces once per invocation as [ts-only]. A file with
// nothing to compare returns false: CrossCheckExtents answers degraded=true
// for an empty resolution list, which is not the same claim as "no server
// answered", and forwarding it would make an ordinary declaration-free file
// report a whole invocation as unverified.
func crossCheckFile(ctx context.Context, sess *lsp.Session, lang resolve.Language, root, path string, src []byte, f *resolve.File) (degraded bool, warnings []string) {
	names := f.DeclOrder()
	list := make([]*resolve.Resolution, 0, len(names))
	for _, name := range names {
		if res, rerr := f.Resolve(name); rerr == nil {
			list = append(list, res)
		}
	}
	if len(list) == 0 {
		return false, nil
	}

	degraded, mismatches := resolve.CrossCheckExtents(ctx, sess, lang, root, filepath.Join(root, path), src, list)
	return crossCheckOutcome(path, degraded, mismatches)
}

// crossCheckOutcome turns CrossCheckExtents' (degraded, mismatches) pair
// into what crossCheckFile reports. It is separate from crossCheckFile so
// the decision is testable without a live language server.
//
// degraded and mismatches are orthogonal: a file can simultaneously be
// partly unverifiable (a declaration absent from the server's outline) and
// carry a genuine disagreement on a declaration the server did name. Both
// must surface -- suppressing a mismatch because the batch was also
// degraded loses the one signal the user needs.
func crossCheckOutcome(path string, degraded bool, mismatches []error) (bool, []string) {
	if len(mismatches) == 0 {
		return degraded, nil
	}
	out := make([]string, 0, len(mismatches))
	for _, m := range mismatches {
		out = append(out, path+": "+m.Error())
	}
	return degraded, out
}
