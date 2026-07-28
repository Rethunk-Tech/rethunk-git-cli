package diff

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

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

	pathspecs := effectivePathspecs(opts)

	entries, err := repo.DiffNumstat(ctx, withPathspecs(scope.NumstatArgs, pathspecs)...)
	if err != nil {
		return nil, err
	}

	report := &Report{}
	for _, e := range entries {
		oldPath, newPath := numstatPath(e.Path)
		fr, ferr := buildFileReport(ctx, repo, root, scope, oldPath, newPath, e.Added, e.Deleted, sess, report)
		if ferr != nil {
			return nil, ferr
		}
		if fr != nil {
			report.Files = append(report.Files, *fr)
		}
	}

	if scope.IncludeUntracked {
		untracked, uerr := repo.LsFilesOthers(ctx, withPathspecs(nil, pathspecs)...)
		if uerr != nil {
			return nil, uerr
		}
		for _, path := range untracked {
			fr, ferr := buildUntrackedReport(root, path)
			if ferr != nil {
				return nil, ferr
			}
			report.Files = append(report.Files, *fr)
		}
	}

	applyFilters(report, canonicalSyms)
	sortReport(report)
	return report, nil
}

// buildFileReport classifies one changed path and dispatches to the right
// row shape: BINARY and MODE short-circuit before any grammar lookup (a
// mode-only change has zero content diff to attribute, and a binary file
// has none to attempt); an unsupported language falls back to the file's
// numstat total under StatusNoSymbols; everything else goes through
// attributeSymbols.
func buildFileReport(ctx context.Context, repo *gitx.Repo, root string, scope Scope, oldPath, newPath, addedStr, deletedStr string, sess *lsp.Session, report *Report) (*FileReport, error) {
	if addedStr == "-" && deletedStr == "-" {
		return &FileReport{Path: newPath, Rows: []Row{{Status: StatusBinary, Added: "-", Deleted: "-"}}}, nil
	}

	added, err := strconv.Atoi(addedStr)
	if err != nil {
		return nil, fmt.Errorf("diff: malformed numstat added count %q for %s", addedStr, newPath)
	}
	deleted, err := strconv.Atoi(deletedStr)
	if err != nil {
		return nil, fmt.Errorf("diff: malformed numstat deleted count %q for %s", deletedStr, newPath)
	}

	if added == 0 && deleted == 0 {
		oldMode, oldFound, merr := scope.Old.mode(ctx, repo, root, oldPath)
		if merr != nil {
			return nil, merr
		}
		newMode, newFound, merr := scope.New.mode(ctx, repo, root, newPath)
		if merr != nil {
			return nil, merr
		}
		note := formatModeNote(oldMode, oldFound, newMode, newFound)
		return &FileReport{Path: newPath, Rows: []Row{{Status: StatusMode, Added: "0", Deleted: "0", ModeNote: note}}}, nil
	}

	lang, ok := resolve.ForExtension(filepath.Ext(newPath))
	if !ok {
		return &FileReport{Path: newPath, Rows: []Row{{Status: StatusNoSymbols, Added: addedStr, Deleted: deletedStr}}}, nil
	}

	oldSrc, _, err := scope.Old.read(ctx, repo, root, oldPath)
	if err != nil {
		return nil, err
	}
	newSrc, _, err := scope.New.read(ctx, repo, root, newPath)
	if err != nil {
		return nil, err
	}

	if sess != nil {
		report.Warnings = append(report.Warnings, crossCheckFile(ctx, sess, lang, root, newPath, newSrc)...)
	}

	rows, err := attributeSymbols(lang, oldSrc, newSrc, added, deleted)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &FileReport{Path: newPath, Rows: rows}, nil
}

// buildUntrackedReport renders a single collapsed row for a file git does
// not track at all (docs/USAGE.md § Commands' "(untracked)" example): its
// symbols are never split out, since none of them exist at any revision
// rgit diff --sym could resolve against yet. Counted directly from the
// worktree file rather than via `git diff --no-index`, whose exit-1-on-
// differences convention (unlike every other `git diff` invocation this
// package makes) would otherwise have to be special-cased.
func buildUntrackedReport(root, path string) (*FileReport, error) {
	content, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return nil, err
	}
	if util.LooksBinary(content) {
		return &FileReport{Path: path, Rows: []Row{{Status: StatusUntracked, Added: "-", Deleted: "-"}}}, nil
	}

	row := Row{Status: StatusUntracked, Added: itoa(countLines(content)), Deleted: "0"}
	if lang, ok := resolve.ForExtension(filepath.Ext(path)); ok {
		if names, derr := resolve.DeclOrder(lang, content); derr == nil && len(names) > 0 {
			row.HintSymbol = names[0]
		}
	}
	return &FileReport{Path: path, Rows: []Row{row}}, nil
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

// numstatPath parses gitx.NumstatEntry.Path, which may carry git's own
// rename shorthand — a full "old => new" or a common-prefix
// "dir/{old => new}" form — into the two paths content resolution needs.
// For a non-rename entry, old and new are identical.
func numstatPath(raw string) (oldPath, newPath string) {
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
	lang, ok := resolve.ForExtension(filepath.Ext(s.File))
	if !ok {
		// No grammar to resolve against at all -- rgit commit's own exit 9
		// ("unsupported language for a symbol anchor") is the closer match
		// than pretending the name might resolve.
		return "", &resolve.ResolveError{Code: exitcode.UnsupportedLanguage, Anchor: s.Name}
	}

	src, exists, err := scope.New.read(ctx, repo, root, s.File)
	if err != nil {
		return "", err
	}
	if !exists {
		src, _, err = scope.Old.read(ctx, repo, root, s.File)
		if err != nil {
			return "", err
		}
	}

	res, err := resolve.Resolve(lang, src, s.Name)
	if err != nil {
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
func crossCheckFile(ctx context.Context, sess *lsp.Session, lang resolve.Language, root, path string, src []byte) []string {
	f, err := resolve.Open(lang, src)
	if err != nil {
		return nil
	}
	defer f.Close()

	names := f.DeclOrder()
	list := make([]*resolve.Resolution, 0, len(names))
	for _, name := range names {
		if res, rerr := f.Resolve(name); rerr == nil {
			list = append(list, res)
		}
	}

	degraded, mismatches := resolve.CrossCheckExtents(ctx, sess, lang, root, filepath.Join(root, path), src, list)
	if degraded || len(mismatches) == 0 {
		return nil
	}
	out := make([]string, 0, len(mismatches))
	for _, m := range mismatches {
		out = append(out, path+": "+m.Error())
	}
	return out
}
