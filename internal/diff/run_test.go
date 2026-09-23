package diff

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// newDiffTestRepo is a temp repo with one committed file, "b.py", holding
// a real Python symbol so an anchor can resolve against it either at HEAD
// or in the worktree.
func newDiffTestRepo(t *testing.T) (dir string, repo *gitx.Repo) {
	t.Helper()
	return gittest.RepoWithFile(t, "b.py", "def existing():\n    return 1\n", "chore: initial b.py")
}

// assertUnresolvable is the shape every --sym failure in this file shares:
// a typed *resolve.ResolveError carrying exit 3, never a bare error and
// never a silently clean report. It returns the error so a caller can go
// on to assert the message text.
func assertUnresolvable(t *testing.T, err error) *resolve.ResolveError {
	t.Helper()
	var rerr *resolve.ResolveError
	if !errors.As(err, &rerr) {
		t.Fatalf("Run error = %v (%T); want *resolve.ResolveError", err, err)
	}
	if rerr.Code != exitcode.AnchorUnresolvable {
		t.Errorf("Code = %v; want %v", rerr.Code, exitcode.AnchorUnresolvable)
	}
	return rerr
}

// TestRun_UnresolvableSymReturnsResolveError asserts that an unresolvable
// --sym request fails loudly: exit 3 via a typed *resolve.ResolveError,
// never a silent exit 0 with empty output that would be indistinguishable
// from "that symbol is clean" -- a typo must not silently invert the
// check-before-commit workflow rgit diff exists to serve.
// `rgit commit --dry-run` already refuses the identical value with a typed
// *resolve.ResolveError; Run must produce the same error, unfiltered by
// whether the named file happens to have any uncommitted changes at all.
func TestRun_UnresolvableSymReturnsResolveError(t *testing.T) {
	t.Parallel()
	dir, repo := newDiffTestRepo(t)
	// b.py has an uncommitted change, so a silent empty result would be
	// easy to mistake for success.
	gittest.Write(t, dir, "b.py", "def existing():\n    return 2\n")

	_, err := Run(context.Background(), repo, dir, Options{
		Syms: []SymRef{{File: "b.py", Name: "doesNotExist"}},
	})

	rerr := assertUnresolvable(t, err)
	if got, want := rerr.Error(), `resolve: "doesNotExist": unresolved`; got != want {
		t.Errorf("Error() = %q; want %q", got, want)
	}
}

// TestRun_SymOnNonexistentFileReturnsResolveError covers the second half of
// the same hazard: a --sym naming a file absent from every side of the
// scope must fail the identical way a bad symbol name does, not silently
// report nothing the way a --file pathspec would (git's own `git diff --
// nosuch.py` convention, which --file deliberately keeps matching).
func TestRun_SymOnNonexistentFileReturnsResolveError(t *testing.T) {
	t.Parallel()
	dir, repo := newDiffTestRepo(t)

	_, err := Run(context.Background(), repo, dir, Options{
		Syms: []SymRef{{File: "nosuch.py", Name: "foo"}},
	})

	// validateSym's own doc comment promises a missing file reads as an
	// empty source and fails exactly the way a real but absent symbol
	// would -- "no separate message shape". Asserting the message here,
	// not just the code, is what actually pins that promise: the two
	// failure modes must produce the identical string, not merely the
	// identical exit code.
	rerr := assertUnresolvable(t, err)
	if got, want := rerr.Error(), `resolve: "foo": unresolved`; got != want {
		t.Errorf("Error() = %q; want %q", got, want)
	}
}

// TestRun_SymResolvesButUnchangedIsNotAnError asserts the distinction that
// must hold: a symbol that genuinely exists but has no uncommitted changes
// is still a clean, exit-0 result with no row for it -- only an anchor
// that does not resolve at all is an error.
func TestRun_SymResolvesButUnchangedIsNotAnError(t *testing.T) {
	t.Parallel()
	dir, repo := newDiffTestRepo(t)
	// No worktree edits at all: "existing" resolves against HEAD, and there
	// is nothing uncommitted anywhere in the repo.

	report, err := Run(context.Background(), repo, dir, Options{
		Syms: []SymRef{{File: "b.py", Name: "existing"}},
	})
	if err != nil {
		t.Fatalf("Run error = %v; want nil", err)
	}
	if report.Dirty() {
		t.Errorf("report.Dirty() = true; want false (nothing changed)")
	}
	if len(report.Files) != 0 {
		t.Errorf("report.Files = %+v; want empty", report.Files)
	}
}

// TestRun_PatchPopulatesReportPatch pins Options.Patch against the default
// scope: opting in populates Report.Patch with a real patch body covering
// the same file the symbol-attributed report already names, and leaving it
// unset (the zero value, Options{}'s own default) leaves Report.Patch nil --
// the "purely additive, gated entirely behind Patch" requirement Run's own
// implementation promises.
func TestRun_PatchPopulatesReportPatch(t *testing.T) {
	t.Parallel()
	dir, repo := newDiffTestRepo(t)
	gittest.Write(t, dir, "b.py", "def existing():\n    return 2\n")

	withPatch, err := Run(context.Background(), repo, dir, Options{Patch: true})
	if err != nil {
		t.Fatalf("Run error = %v; want nil", err)
	}
	if !strings.Contains(string(withPatch.Patch), "diff --git") {
		t.Errorf("Report.Patch = %q; want a real patch body", withPatch.Patch)
	}
	if !strings.Contains(string(withPatch.Patch), "return 2") {
		t.Errorf("Report.Patch = %q; want the actual changed content", withPatch.Patch)
	}

	withoutPatch, err := Run(context.Background(), repo, dir, Options{})
	if err != nil {
		t.Fatalf("Run error = %v; want nil", err)
	}
	if withoutPatch.Patch != nil {
		t.Errorf("Report.Patch = %q; want nil when Options.Patch is false", withoutPatch.Patch)
	}
}

func TestRun_UnmergedPathUsesWorktreeConflictContent(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "conflict.txt", "base\n", "chore: add conflict fixture")
	gittest.Write(t, dir, "conflict.txt", "<<<<<<< ours\nworktree\n>>>>>>> theirs\n")

	blob := strings.TrimSpace(gittest.Git(t, dir, "rev-parse", "HEAD:conflict.txt"))
	gittest.Unmerged(t, dir, blob, "conflict.txt")

	report, err := Run(context.Background(), repo, dir, Options{
		Files: []string{"conflict.txt"},
		Patch: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	file, ok := findFile(report.Files, "conflict.txt")
	if !ok || len(file.Rows) == 0 {
		t.Fatalf("report.Files = %+v; want conflict.txt with rows", report.Files)
	}
	if !strings.Contains(string(report.Patch), "<<<<<<< ours") || !strings.Contains(string(report.Patch), "worktree") {
		t.Errorf("Report.Patch = %q; want worktree conflict content", report.Patch)
	}
}

// TestValidateSym_PrefersNewSideThenFallsBackToOld exercises validateSym
// directly against every side-availability combination applyFilters'
// hazard rests on: resolving on the New side when present, falling back to
// Old when the file was deleted from the worktree, and failing when the
// name exists on neither.
func TestValidateSym_PrefersNewSideThenFallsBackToOld(t *testing.T) {
	t.Parallel()
	dir, repo := newDiffTestRepo(t)
	ctx := context.Background()
	scope, err := ResolveScope(ctx, repo, Options{})
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}

	// New (worktree) has the file and the symbol: resolves clean, to its own
	// canonical spelling since "existing" is already canonical.
	if name, err := validateSym(ctx, repo, dir, scope, SymRef{File: "b.py", Name: "existing"}); err != nil {
		t.Errorf("worktree resolution: %v", err)
	} else if name != "existing" {
		t.Errorf("canonical anchor = %q; want %q", name, "existing")
	}

	// Delete the worktree file but keep it in HEAD: New has nothing, Old
	// (HEAD, the default scope's base) still resolves it.
	if err := os.Remove(filepath.Join(dir, "b.py")); err != nil {
		t.Fatal(err)
	}
	if _, err := validateSym(ctx, repo, dir, scope, SymRef{File: "b.py", Name: "existing"}); err != nil {
		t.Errorf("HEAD fallback resolution after worktree deletion: %v", err)
	}

	// Neither side has it: unresolvable.
	var rerr *resolve.ResolveError
	_, err = validateSym(ctx, repo, dir, scope, SymRef{File: "b.py", Name: "neverExisted"})
	if !errors.As(err, &rerr) || rerr.Code != exitcode.AnchorUnresolvable {
		t.Errorf("validateSym = %v; want *resolve.ResolveError{Code: AnchorUnresolvable}", err)
	}
}

// TestRun_ExtensionlessShebangEnumeratesSymbols closes the loop
// resolve.ForPath opened for rgit commit: rgit diff must reveal the same
// anchors a symbol-granular commit already accepts, or the documented
// read-diff-copy-anchor-commit workflow can never discover them for a
// git-hook-style script with no extension at all.
func TestRun_ExtensionlessShebangEnumeratesSymbols(t *testing.T) {
	t.Parallel()
	dir, repo := newDiffTestRepo(t)
	gittest.Write(t, dir, "pre-commit", "#!/usr/bin/env bash\n\nfoo() {\n  echo v1\n}\n\nbar() {\n  echo bar\n}\n")
	gittest.Git(t, dir, "add", "pre-commit")
	gittest.Git(t, dir, "commit", "-q", "-m", "add pre-commit")

	// Edit foo only; bar stays clean and must not appear as a row.
	gittest.Write(t, dir, "pre-commit", "#!/usr/bin/env bash\n\nfoo() {\n  echo v2\n}\n\nbar() {\n  echo bar\n}\n")

	report, err := Run(context.Background(), repo, dir, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var got *FileReport
	for i := range report.Files {
		if report.Files[i].Path == "pre-commit" {
			got = &report.Files[i]
		}
	}
	if got == nil {
		t.Fatalf("no report for pre-commit; report.Files = %+v", report.Files)
	}
	if len(got.Rows) != 1 || got.Rows[0].Symbol != "foo" {
		t.Fatalf("pre-commit rows = %+v; want exactly one row named foo", got.Rows)
	}

	// The anchor Run just printed must be exactly what validateSym (and so
	// rgit commit) accepts back -- the property the whole loop rests on.
	scope, err := ResolveScope(context.Background(), repo, Options{})
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}
	canonical, err := validateSym(context.Background(), repo, dir, scope, SymRef{File: "pre-commit", Name: got.Rows[0].Symbol})
	if err != nil {
		t.Fatalf("validateSym(%q): %v", got.Rows[0].Symbol, err)
	}
	if canonical != "foo" {
		t.Errorf("canonical anchor = %q; want %q", canonical, "foo")
	}

	// A deleted extensionless script has no worktree copy left to peek, so the
	// resolver samples HEAD and keeps the script's grammar.
	if err := os.Remove(filepath.Join(dir, "pre-commit")); err != nil {
		t.Fatal(err)
	}
	report, err = Run(context.Background(), repo, dir, Options{})
	if err != nil {
		t.Fatalf("Run after deletion: %v", err)
	}
	got = nil
	for i := range report.Files {
		if report.Files[i].Path == "pre-commit" {
			got = &report.Files[i]
		}
	}
	if got == nil {
		t.Fatalf("no report for deleted pre-commit; report.Files = %+v", report.Files)
	}
	wantSymbols := []string{"@header", "foo", "bar"}
	if len(got.Rows) != len(wantSymbols) {
		t.Fatalf("deleted pre-commit rows = %+v; want symbols %v", got.Rows, wantSymbols)
	}
	for i, want := range wantSymbols {
		if got.Rows[i].Symbol != want || got.Rows[i].Status != StatusDeleted {
			t.Errorf("deleted pre-commit row %d = %+v; want deleted %q", i, got.Rows[i], want)
		}
	}
}

// TestRun_SymFilterMatchesAnyAcceptedAliasSpelling guards against a
// silent-empty-diff hazard identical in shape to the one
// TestRun_UnresolvableSymReturnsResolveError already covers: applyFilters
// must match a --sym request against any spelling resolve.Resolve accepts
// on input, not only Row.Symbol's canonical, emitted spelling
// (resolve.DeclOrder) -- gopls's "(*A).Get" receiver form, or a Markdown
// heading's raw text (docs/ANCHORS.md), must match the same row the
// canonical name matches, not silently filter to zero rows, which would be
// indistinguishable from "that symbol is clean" -- the same false-negative
// this package's validateSyms already exists to prevent for a name that
// does not resolve at all.
//
// One fixture covers every aliasing shape at once rather than one test per
// language: a canonical name (must keep working), gopls's receiver spelling,
// and a Markdown heading's raw text.
func TestRun_SymFilterMatchesAnyAcceptedAliasSpelling(t *testing.T) {
	t.Parallel()
	dir, repo := newDiffTestRepo(t)

	gittest.Write(t, dir, "a.go", "package p\n\ntype A struct{}\n\nfunc (a *A) Get() int { return 1 }\n")
	gittest.Write(t, dir, "doc.md", "# Diff Scope\n\nOriginal.\n")
	gittest.Git(t, dir, "add", "a.go", "doc.md")
	gittest.Git(t, dir, "commit", "-q", "-m", "chore: add a.go and doc.md")

	gittest.Write(t, dir, "a.go", "package p\n\ntype A struct{}\n\nfunc (a *A) Get() int { return 2 }\n")
	gittest.Write(t, dir, "doc.md", "# Diff Scope\n\nEdited.\n")

	for _, tt := range []struct {
		name string
		sym  SymRef
	}{
		{"canonical Go receiver", SymRef{File: "a.go", Name: "A.Get"}},
		{"gopls receiver spelling", SymRef{File: "a.go", Name: "(*A).Get"}},
		{"Markdown raw heading text", SymRef{File: "doc.md", Name: "Diff Scope"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report, err := Run(context.Background(), repo, dir, Options{Syms: []SymRef{tt.sym}})
			if err != nil {
				t.Fatalf("Run(%+v): %v", tt.sym, err)
			}
			if len(report.Files) != 1 || len(report.Files[0].Rows) != 1 {
				t.Fatalf("Run(%+v) report = %+v; want exactly one row", tt.sym, report)
			}
		})
	}
}

// TestApplyFilters_KeepsOnlyNamedSymbolsAcrossFiles is a direct, repo-free
// unit test of applyFilters' own branches: a row matching a requested
// anchor survives, a row in a file the caller did not name is dropped
// wholesale, and a named file with no surviving rows drops out of the
// report entirely rather than leaving an empty FileReport behind.
func TestApplyFilters_KeepsOnlyNamedSymbolsAcrossFiles(t *testing.T) {
	t.Parallel()
	report := &Report{
		Files: []FileReport{
			{Path: "a.go", Rows: []Row{
				{Symbol: "A", Status: StatusMod, Added: "1", Deleted: "0"},
				{Symbol: "B", Status: StatusMod, Added: "2", Deleted: "0"},
			}},
			{Path: "b.go", Rows: []Row{
				{Symbol: "C", Status: StatusMod, Added: "3", Deleted: "0"},
			}},
			{Path: "c.go", Rows: []Row{
				// A file-level row with no Symbol never matches a --sym
				// filter, whatever name is requested against its file.
				{Status: StatusUnanchorable, Added: "4", Deleted: "0"},
			}},
		},
	}

	applyFilters(report, []SymRef{
		{File: "a.go", Name: "A"},
		{File: "c.go", Name: "AnythingAtAll"},
	})

	if len(report.Files) != 1 {
		t.Fatalf("report.Files = %+v; want exactly a.go", report.Files)
	}
	f := report.Files[0]
	if f.Path != "a.go" {
		t.Errorf("Path = %q; want a.go", f.Path)
	}
	if len(f.Rows) != 1 || f.Rows[0].Symbol != "A" {
		t.Errorf("Rows = %+v; want exactly the A row", f.Rows)
	}
}

// TestApplyFilters_NoSymsIsANoOp pins the fast path applyFilters' own doc
// comment describes: a --file-only invocation needs no Go-side filtering at
// all, since the git-level query already scoped it.
func TestApplyFilters_NoSymsIsANoOp(t *testing.T) {
	t.Parallel()
	report := &Report{Files: []FileReport{
		{Path: "a.go", Rows: []Row{{Status: StatusUnanchorable, Added: "1", Deleted: "0"}}},
	}}
	applyFilters(report, nil)
	if len(report.Files) != 1 {
		t.Errorf("report.Files = %+v; want unchanged", report.Files)
	}
}

// pathWithGitOnly returns a PATH holding nothing but git, so a test can
// guarantee no language server binary is discoverable while gitx still
// works. Emptying PATH outright would break git lookup itself, and pointing
// it at git's own directory is not enough on a machine where a server
// happens to live there too (bash-language-server ships in /usr/bin).
func pathWithGitOnly(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	bin := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	return bin
}

// countingGitWrapper installs a shell script named "git" ahead of the real
// one on PATH, counting every "cat-file" invocation to countFile before
// exec'ing straight through to it -- the concrete measurement
// prefetchBlobs' own subprocess-count claim needs, not just a structural
// argument that the code only calls BatchCatFile once. Skips cleanly (not
// a hard failure) on a platform with no /bin/sh, matching this package's
// own git-not-on-PATH skip precedent.
func countingGitWrapper(t *testing.T) (bin, countFile string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh not on PATH: %v", err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	bin = t.TempDir()
	countFile = filepath.Join(t.TempDir(), "cat-file.count")
	// Every call in this package goes through "-C <root> cat-file ...", not
	// "cat-file" as $1 -- matched against the whole argument list, not a
	// fixed position.
	script := "#!/bin/sh\n" +
		"case \" $* \" in *\\ cat-file\\ *) printf x >> " + shellQuote(countFile) + " ;; esac\n" +
		"exec " + shellQuote(gitPath) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, countFile
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// TestRun_BatchesGitCatFileAcrossManyChangedFiles is the measured half of
// the batching claim: buildFileReport's own read of each side, for N
// changed files each with a real content edit against a rev (not the
// worktree, which never shells out to cat-file at all), costs exactly one
// `git cat-file` subprocess total, regardless of N, because Run's own loop
// calls prefetchBlobs (and so BatchCatFile) a single time before the
// per-file loop even starts.
func TestRun_BatchesGitCatFileAcrossManyChangedFiles(t *testing.T) {
	// cannot Parallel because t.Setenv below
	dir, repo := gittest.New(t)
	const fileCount = 12
	for i := range fileCount {
		gittest.Write(t, dir, fmt.Sprintf("f%d.go", i), fmt.Sprintf("package p\n\nfunc F%d() int { return %d }\n", i, i))
	}
	gittest.Commit(t, dir, "chore: initial")
	for i := range fileCount {
		gittest.Write(t, dir, fmt.Sprintf("f%d.go", i), fmt.Sprintf("package p\n\nfunc F%d() int { return %d }\n", i, i+100))
	}
	gittest.Commit(t, dir, "chore: edit all")

	bin, countFile := countingGitWrapper(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A rev-to-rev comparison: both sides are git-backed (sideRev), so
	// every file's read would have gone through cat-file under the old,
	// unbatched path -- the case the batching exists for.
	report, err := Run(context.Background(), repo, dir, Options{Revisions: []string{"HEAD~1", "HEAD"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Files) != fileCount {
		t.Fatalf("len(report.Files) = %d; want %d", len(report.Files), fileCount)
	}

	countBytes, err := os.ReadFile(countFile)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	// Exactly one: prefetchBlobs makes exactly one BatchCatFile call for the
	// whole loop, regardless of how many files it covers -- not merely
	// "fewer than fileCount", which a smaller but still per-file improvement
	// would also satisfy.
	if count := len(countBytes); count != 1 {
		t.Errorf("cat-file invoked %d times for %d files; want exactly 1 (one batched call)", count, fileCount)
	}
}

// TestRun_DegradedCrossCheckSetsTSOnly asserts the signal docs/INSTALL.md §
// Verify's recipe greps for: crossCheckFile must propagate the degraded
// bool CrossCheckExtents returns, so `rgit diff` reports [ts-only]
// whenever no language server was reached to check a file's extents.
func TestRun_DegradedCrossCheckSetsTSOnly(t *testing.T) {
	dir, repo := newDiffTestRepo(t)
	gittest.Write(t, dir, "b.py", "def existing():\n    return 2\n")
	t.Setenv("PATH", pathWithGitOnly(t))

	report, err := Run(context.Background(), repo, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.TSOnly {
		t.Error("TSOnly = false; want true with no language server reachable")
	}
}

// TestRun_RevisionRangeIsNotDegraded separates "no comparison was possible"
// from "no comparison was attempted". A revision-to-revision diff skips the
// cross-check by design -- a language server has no view of an arbitrary
// revision, so Run never builds a session at all -- and reporting [ts-only]
// there would train the reader to ignore it.
func TestRun_RevisionRangeIsNotDegraded(t *testing.T) {
	dir, repo := newDiffTestRepo(t)
	t.Setenv("PATH", pathWithGitOnly(t))

	report, err := Run(context.Background(), repo, dir, Options{RangeFlag: "HEAD..HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if report.TSOnly {
		t.Error("TSOnly = true; want false when the cross-check is inapplicable, not degraded")
	}
}

// TestRun_NoResolvableDeclarationsIsNotDegraded guards the false positive the
// report-level flag invites: CrossCheckExtents returns degraded=true for an
// empty resolution list, which means "nothing to compare", not "no server".
// A file with no addressable declaration must not make a whole invocation
// claim its extents went unverified.
func TestRun_NoResolvableDeclarationsIsNotDegraded(t *testing.T) {
	dir, repo := newDiffTestRepo(t)
	// PATH is stripped so the assertion discriminates: the guard must skip
	// the dial entirely for a file with nothing to compare. Without it, the
	// dial would be attempted, fail for want of a server, and report
	// [ts-only] for a file that never had a symbol to verify.
	t.Setenv("PATH", pathWithGitOnly(t))
	// A Python file holding only a comment: parses, but declares nothing.
	gittest.Write(t, dir, "empty.py", "# no declarations here\n")
	gittest.Git(t, dir, "add", "empty.py")

	report, err := Run(context.Background(), repo, dir, Options{Files: []string{"empty.py"}})
	if err != nil {
		t.Fatal(err)
	}
	if report.TSOnly {
		t.Error("TSOnly = true; want false when the file had nothing to cross-check")
	}
}

// rowsFor runs a default-scope diff and returns the rows for one path, so a
// case can assert the exact row set a reader would see.
func rowsFor(t *testing.T, dir string, repo *gitx.Repo, path string) []Row {
	t.Helper()
	report, err := Run(context.Background(), repo, dir, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range report.Files {
		if f.Path == path {
			return f.Rows
		}
	}
	return nil
}

func describeRows(rows []Row) string {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "[%s %s +%s/-%s]", r.Symbol, r.Status.Porcelain(), r.Added, r.Deleted)
	}
	return b.String()
}

// TestAttribute_TopLevelSymbolOwnsOneSeparator guards against a row that
// tells the reader to do the one thing rgit exists to avoid: staging a
// whole file for a change already captured by the anchor.
//
// Adding or removing a top-level declaration also moves the blank line
// between it and its neighbour. internal/synth moves exactly one such line
// (joinWithSeparator for an insert, spliceExcise's gap collapse for a
// delete), so attribution must count that line as part of the
// declaration's own extent -- otherwise it falls to (unanchorable), a row
// carrying "-> use --file X", advising a whole-path stage that is already
// unnecessary.
//
// Python is the case that proves the rule is "one separator", not "all
// adjacent blank lines": PEP 8 writes two, synth still inserts one, and the
// second genuinely does remain unowned -- so exactly one (unanchorable)
// line must survive there.
func TestAttribute_TopLevelSymbolOwnsOneSeparator(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, path, head, work string
		wantRows               string
	}{{
		name: "go insertion",
		path: "a.go",
		head: "package p\n\nfunc Keep() int {\n\treturn 1\n}\n",
		work: "package p\n\nfunc Keep() int {\n\treturn 1\n}\n\nfunc Added() int {\n\treturn 9\n}\n",
		// The separator is the symbol's, so there is no remainder at all.
		wantRows: "[Added MOD +4/-0]",
	}, {
		name:     "go deletion",
		path:     "a.go",
		head:     "package p\n\nfunc Keep() int {\n\treturn 1\n}\n\nfunc Doomed() int {\n\treturn 2\n}\n",
		work:     "package p\n\nfunc Keep() int {\n\treturn 1\n}\n",
		wantRows: "[Doomed DELETED +0/-4]",
	}, {
		name:     "typescript insertion",
		path:     "b.ts",
		head:     "export function g(): number {\n  return 1;\n}\n",
		work:     "export function g(): number {\n  return 1;\n}\n\nexport function h(): number {\n  return 2;\n}\n",
		wantRows: "[h MOD +4/-0]",
	}, {
		name:     "python insertion leaves the second blank line unowned",
		path:     "c.py",
		head:     "def g():\n    return 1\n",
		work:     "def g():\n    return 1\n\n\ndef h():\n    return 2\n",
		wantRows: "[h MOD +3/-0][ UNANCHORABLE +1/-0]",
	}, {
		name: "a container member owns no blank line",
		path: "d.go",
		head: "package p\n\ntype S struct {\n\tA int\n}\n",
		work: "package p\n\ntype S struct {\n\tA int\n\tB int\n}\n",
		// A member is separated by one newline, not a blank line, so
		// nothing extra is attributed and no remainder appears.
		wantRows: "[S MOD +1/-0][S.B MOD +1/-0]",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			// Each case builds its own gittest repo under its own t.TempDir()
			// and touches no shared package state, no t.Setenv, no t.Chdir --
			// safe to run concurrently with its four siblings.
			t.Parallel()
			dir, repo := gittest.RepoWithFile(t, tc.path, tc.head, "chore: fixture")
			gittest.Write(t, dir, tc.path, tc.work)

			if got := describeRows(rowsFor(t, dir, repo, tc.path)); got != tc.wantRows {
				t.Errorf("rows = %s; want %s", got, tc.wantRows)
			}
		})
	}
}

// TestCrossCheckOutcome_DegradedAndMismatchAreOrthogonal pins that a file where
// nine of ten declarations verify clean and the tenth disagrees reports the
// disagreement, not [ts-only]. CrossCheckExtents can return degraded=true
// alongside mismatches; crossCheckFile must not treat degraded as permission
// to drop mismatches. Exercised directly against the (degraded, mismatches) pair
// rather than through a live language server: the bug is in how this
// package combines two already-computed signals, not in what a server
// says, so a real dial would only add flakiness without adding proof.
func TestCrossCheckOutcome_DegradedAndMismatchAreOrthogonal(t *testing.T) {
	t.Parallel()
	mismatch := &resolve.ResolveError{
		Code:            exitcode.ExtentMismatch,
		Anchor:          "Found",
		TreeSitterRange: "1..2",
		LSPRange:        "3..4",
	}

	degraded, warnings := crossCheckOutcome("a.go", true, []error{mismatch})

	if !degraded {
		t.Error("degraded = false; want true -- some declaration was genuinely not found")
	}
	if want := []string{"a.go: " + mismatch.Error()}; !slices.Equal(warnings, want) {
		t.Errorf("warnings = %v; want %v -- the mismatch must survive a degraded batch", warnings, want)
	}
}

// TestRun_ScopeUsageErrorsAreTyped covers the scope contradictions Run
// detects itself. They must arrive as *UsageError, because internal/app
// maps that type — and nothing else — to exit 129 rather than to the
// exit 128 a git-level failure gets; the distinction is what tells a caller
// whether they mistyped the command or whether git broke.
func TestRun_ScopeUsageErrorsAreTyped(t *testing.T) {
	t.Parallel()
	dir, repo := newDiffTestRepo(t)

	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{{
		name: "--range with a positional range",
		opts: Options{RangeFlag: "HEAD~1..HEAD", PositionalRange: "HEAD~2..HEAD"},
		want: "--range and a positional revision range are mutually exclusive",
	}, {
		name: "--staged with a revision range",
		opts: Options{Staged: true, RangeFlag: "HEAD~1..HEAD"},
		want: "--staged/--unstaged and a revision range are mutually exclusive",
	}, {
		name: "three revisions",
		opts: Options{Revisions: []string{"HEAD", "HEAD", "HEAD"}},
		want: "at most two revision arguments are accepted",
	}, {
		name: "a rev:path pair with a pathspec too",
		opts: Options{RevPaths: []cli.RevPath{{Rev: "HEAD~1", Path: "a.go"}, {Rev: "HEAD", Path: "a.go"}}, Files: []string{"a.go"}},
		want: "a two-blob \"A:f.go B:f.go\" scope is exclusive of every other scope selector and pathspec",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(context.Background(), repo, dir, tc.opts)

			var uerr *UsageError
			if !errors.As(err, &uerr) {
				t.Fatalf("Run error = %v (%T); want *UsageError", err, err)
			}
			if uerr.Error() != tc.want {
				t.Errorf("Error() = %q; want %q", uerr.Error(), tc.want)
			}
		})
	}
}

// TestRun_RevPathTwoBlobScope pins the "A:f.go B:f.go" scope end-to-end
// through Run: the identical path at two arbitrary revisions attributes by
// symbol exactly like any other scope, --sym filters it the same way, and
// a lone (unpaired) rev:path stays refused rather than silently becoming
// this feature by accident.
func TestRun_RevPathTwoBlobScope(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.RepoWithFile(t, "a.go", "package p\n\nfunc Foo() int { return 1 }\n\nfunc Bar() int { return 2 }\n", "chore: v1")
	gittest.Write(t, dir, "a.go", "package p\n\nfunc Foo() int { return 11 }\n\nfunc Bar() int { return 22 }\n")
	gittest.Commit(t, dir, "chore: v2")
	// A third, unrelated commit -- proves the comparison is strictly
	// between the two named revisions, not "HEAD and its parent" by
	// coincidence.
	gittest.Write(t, dir, "b.go", "package p\n\nfunc Unrelated() {}\n")
	gittest.Commit(t, dir, "chore: unrelated change")

	report, err := Run(context.Background(), repo, dir, Options{
		RevPaths: []cli.RevPath{{Rev: "HEAD~2", Path: "a.go"}, {Rev: "HEAD~1", Path: "a.go"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	fr, ok := findFile(report.Files, "a.go")
	if !ok {
		t.Fatalf("Files = %+v; want a.go present", report.Files)
	}
	var symbols []string
	for _, r := range fr.Rows {
		symbols = append(symbols, r.Symbol)
	}
	if !slices.Contains(symbols, "Foo") || !slices.Contains(symbols, "Bar") {
		t.Errorf("symbols = %v; want both Foo and Bar changed between HEAD~2 and HEAD~1", symbols)
	}
	if len(report.Files) != 1 {
		t.Errorf("Files = %+v; want only a.go -- b.go belongs to a later commit than either endpoint", report.Files)
	}

	filtered, err := Run(context.Background(), repo, dir, Options{
		RevPaths: []cli.RevPath{{Rev: "HEAD~2", Path: "a.go"}, {Rev: "HEAD~1", Path: "a.go"}},
		Syms:     []SymRef{{File: "a.go", Name: "Foo"}},
	})
	if err != nil {
		t.Fatalf("Run with --sym: %v", err)
	}
	fr, ok = findFile(filtered.Files, "a.go")
	if !ok || len(fr.Rows) != 1 || fr.Rows[0].Symbol != "Foo" {
		t.Fatalf("filtered Files = %+v; want exactly one Foo row", filtered.Files)
	}
}

// TestNumstatPath covers git's two numstat rename spellings plus the
// non-rename identity case: a full "old => new" (already exercised
// indirectly by every rename fixture elsewhere in this package), and the
// common-prefix "dir/{old => new}suffix" shorthand git switches to once a
// rename shares a path prefix, which nothing in this package's own tests
// reached before.
func TestNumstatPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		raw     string
		oldPath string
		newPath string
	}{
		{"no rename", "unchanged.go", "unchanged.go", "unchanged.go"},
		{"full rename", "old.go => new.go", "old.go", "new.go"},
		{"common-prefix brace shorthand", "pkg/{old => new}/file.go", "pkg/old/file.go", "pkg/new/file.go"},
		{"brace shorthand, empty old side", "a/{ => lib/server}/f.ts", "a/f.ts", "a/lib/server/f.ts"},
		{"brace shorthand, empty new side", "a/{lib => }/f.ts", "a/lib/f.ts", "a/f.ts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldPath, newPath := NumstatPath(tc.raw)
			if oldPath != tc.oldPath || newPath != tc.newPath {
				t.Errorf("NumstatPath(%q) = (%q, %q); want (%q, %q)", tc.raw, oldPath, newPath, tc.oldPath, tc.newPath)
			}
		})
	}
}

// TestRun_FileAndRowOrderIsPathThenPosition pins docs/USAGE.md's stable
// ordering contract at the unit lane: files sorted alphabetically by path
// (sortReport), rows within a file sorted by source position, not by
// symbol name (attributeSymbols' own SortStableFunc by pos). The e2e lane
// covers this too, but a regression in either sort would pass `go test -short ./...`
// without this unit case. Two files guard the path sort; each declaring Zebra before
// Apple in source order guards the position sort against an accidental
// alphabetical one.
func TestRun_FileAndRowOrderIsPathThenPosition(t *testing.T) {
	t.Parallel()
	src := "package p\n\nfunc Zebra() int { return 1 }\n\nfunc Apple() int { return 2 }\n"
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "b.go", src)
	gittest.Write(t, dir, "a.go", src)
	gittest.Commit(t, dir, "chore: fixture")

	edited := strings.NewReplacer("return 1", "return 11", "return 2", "return 22").Replace(src)
	gittest.Write(t, dir, "a.go", edited)
	gittest.Write(t, dir, "b.go", edited)

	report, err := Run(context.Background(), repo, dir, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Files) != 2 || report.Files[0].Path != "a.go" || report.Files[1].Path != "b.go" {
		t.Fatalf("Files = %+v; want a.go before b.go", report.Files)
	}
	for _, f := range report.Files {
		if len(f.Rows) != 2 || f.Rows[0].Symbol != "Zebra" || f.Rows[1].Symbol != "Apple" {
			t.Errorf("%s rows = %+v; want Zebra before Apple (source order, not alphabetical)", f.Path, f.Rows)
		}
	}
}

// TestRun_UntrackedFileAttributesPerSymbol pins buildUntrackedReport's own
// per-symbol attribution: an untracked file with two functions emits two
// MOD rows, not one collapsed (untracked) row, and --sym filters to just
// one of them exactly like it would for a brand-new tracked file.
func TestRun_UntrackedFileAttributesPerSymbol(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "new.go", "package p\n\nfunc First() int { return 1 }\n\nfunc Second() int { return 2 }\n")
	// Deliberately never `git add`ed -- LsFilesOthers is what surfaces it.

	report, err := Run(context.Background(), repo, dir, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	fr, ok := findFile(report.Files, "new.go")
	if !ok {
		t.Fatalf("Files = %+v; want new.go present", report.Files)
	}
	var symbols []string
	for _, r := range fr.Rows {
		if r.Status == StatusUntracked {
			t.Errorf("row %+v; want per-symbol MOD rows, not a collapsed UNTRACKED row", r)
		}
		if r.Symbol != "" {
			symbols = append(symbols, r.Symbol)
		}
	}
	if !slices.Contains(symbols, "First") || !slices.Contains(symbols, "Second") {
		t.Errorf("symbols = %v; want both First and Second", symbols)
	}

	filtered, err := Run(context.Background(), repo, dir, Options{Syms: []SymRef{{File: "new.go", Name: "Second"}}})
	if err != nil {
		t.Fatalf("Run with --sym: %v", err)
	}
	fr, ok = findFile(filtered.Files, "new.go")
	if !ok || len(fr.Rows) != 1 || fr.Rows[0].Symbol != "Second" {
		t.Fatalf("filtered Files = %+v; want exactly one Second row", filtered.Files)
	}
}

// TestRun_ContainerEscalationNoticeOnlyUnderSymFilter pins the diff-side
// half of internal/synth's own container escalation (classify.go's
// escalateToContainer): committing a member of a class new to HEAD widens
// staging to the whole class, and a --sym-filtered `rgit diff` on just that
// member would otherwise hide that its sibling is coming along too. The
// notice fires only under --sym -- the unfiltered listing already shows
// every sibling as its own row, so repeating it there would just be noise.
func TestRun_ContainerEscalationNoticeOnlyUnderSymFilter(t *testing.T) {
	// cannot Parallel because t.Setenv below
	dir, repo := gittest.New(t)
	t.Setenv("PATH", pathWithGitOnly(t))
	gittest.Write(t, dir, "new.ts", "class Widget {\n  foo(): number { return 1 }\n\n  bar(): number { return 2 }\n}\n")

	unfiltered, err := Run(context.Background(), repo, dir, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, w := range unfiltered.Warnings {
		if strings.Contains(w, "is new; rgit commit would stage the whole container") {
			t.Errorf("unfiltered Warnings = %v; want no escalation notice with no --sym filter", unfiltered.Warnings)
		}
	}

	filtered, err := Run(context.Background(), repo, dir, Options{Syms: []SymRef{{File: "new.ts", Name: "foo"}}})
	if err != nil {
		t.Fatalf("Run with --sym: %v", err)
	}
	found := false
	for _, w := range filtered.Warnings {
		if w == "new.ts: Widget.foo: Widget is new; rgit commit would stage the whole container, not just Widget.foo" {
			found = true
		}
	}
	if !found {
		t.Errorf("filtered Warnings = %v; want the container-escalation notice for Widget", filtered.Warnings)
	}
}

func findFile(files []FileReport, path string) (FileReport, bool) {
	for _, f := range files {
		if f.Path == path {
			return f, true
		}
	}
	return FileReport{}, false
}

// TestRun_BinaryChangeReportsDashCounts pins run.go's binary short-circuit
// (buildFileReport's addedStr == "-" && deletedStr == "-" branch) at the
// unit lane: rgit_e2e_test.go's TestDiff_BinaryRowUsesDashCounts proves a
// real worktree binary change surfaces as StatusBinary with "-" counts
// end to end, but render_test.go only ever constructs StatusBinary rows by
// hand for rendering, never exercising Run's own classification of one.
func TestRun_BinaryChangeReportsDashCounts(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	binary := []byte("PNGFAKE\x00\x01binary")
	gittest.Write(t, dir, "logo.bin", string(binary))
	gittest.Git(t, dir, "add", "--", "logo.bin")
	gittest.Git(t, dir, "commit", "-q", "-m", "add binary")

	changed := append(append([]byte(nil), binary...), 'X')
	gittest.Write(t, dir, "logo.bin", string(changed))

	report, err := Run(context.Background(), repo, dir, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var got *FileReport
	for i := range report.Files {
		if report.Files[i].Path == "logo.bin" {
			got = &report.Files[i]
		}
	}
	if got == nil || len(got.Rows) != 1 {
		t.Fatalf("report for logo.bin = %+v; want exactly one row", got)
	}
	row := got.Rows[0]
	if row.Status != StatusBinary || row.Added != "-" || row.Deleted != "-" {
		t.Errorf("logo.bin row = %+v; want StatusBinary with \"-\" counts", row)
	}
}

// TestRun_FreeFloatingCommentIsUnanchorable pins docs/ANCHORS.md's example
// at the unit lane: a comment separated from every declaration by a blank
// line on both sides belongs to no symbol, so editing only it must surface
// as (unanchorable) and neither neighbouring function may show any change
// -- the sum-of-hunks invariant. The e2e lane exercises this path too;
// this unit case drives a Go free-floating comment through attributeSymbols/Run
// rather than setext attribution and hand-built rows.
func TestRun_FreeFloatingCommentIsUnanchorable(t *testing.T) {
	t.Parallel()
	const before = `package notes

func A() int {
	return 1
}

// free-floating note

func B() int {
	return 2
}
`
	const after = `package notes

func A() int {
	return 1
}

// free-floating note, edited

func B() int {
	return 2
}
`
	dir, repo := gittest.RepoWithFile(t, "notes.go", before, "chore: fixture")
	gittest.Write(t, dir, "notes.go", after)

	rows := rowsFor(t, dir, repo, "notes.go")

	var found bool
	for _, r := range rows {
		if r.Status == StatusUnanchorable {
			found = true
			if r.Added != "1" || r.Deleted != "1" {
				t.Errorf("UNANCHORABLE row = %+v; want Added=1 Deleted=1", r)
			}
		}
		if r.Symbol != "" {
			t.Errorf("neither A nor B changed; the comment edit must not be attributed to a symbol: %+v", r)
		}
	}
	if !found {
		t.Fatalf("comment-only change between two functions must surface as UNANCHORABLE: %+v", rows)
	}
}

// TestRun_UntrackedFileThatVanishesIsWarnedNotFatal pins the race
// buildUntrackedReport shares with synth's preview counts: ls-files
// --others names a path, and by the time the report reads it the file is
// gone. Returning that error failed the whole report -- every other file
// with it -- for one file that stopped existing. A dangling symlink is the
// deterministic stand-in: git lists it as untracked, and reading it gets
// the same ENOENT the real race produces.
func TestRun_UntrackedFileThatVanishesIsWarnedNotFatal(t *testing.T) {
	t.Parallel()
	dir, repo := newDiffTestRepo(t)
	gittest.Write(t, dir, "real.go", "package p\n\nfunc Kept() int { return 1 }\n")
	if err := os.Symlink(filepath.Join(dir, "gone.go"), filepath.Join(dir, "dangling.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	report, err := Run(context.Background(), repo, dir, Options{})
	if err != nil {
		t.Fatalf("Run: %v; want the unreadable path skipped, not a failed report", err)
	}
	if _, ok := findFile(report.Files, "real.go"); !ok {
		t.Errorf("Files = %+v; want the readable untracked file still reported", report.Files)
	}
	if _, ok := findFile(report.Files, "dangling.go"); ok {
		t.Errorf("Files = %+v; want the unreadable path absent", report.Files)
	}
	var warned bool
	for _, w := range report.Warnings {
		if strings.Contains(w, "dangling.go") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("Warnings = %v; want one naming dangling.go, so the skip is not silent", report.Warnings)
	}
}

// TestRun_StagedRenameAcrossDirectoryDepth pins the brace-shorthand form
// where one side is empty ("a/{ => lib/server}/f.ts"): the empty side's
// path has exactly one separator between prefix and suffix, so HEAD content
// resolves in both directions.
func TestRun_StagedRenameAcrossDirectoryDepth(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, from, to string }{
		{"into new subdirectory", "a/f.ts", "a/lib/server/f.ts"},
		{"out of subdirectory", "a/lib/f.ts", "a/f.ts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, repo := gittest.RepoWithFile(t, tc.from, "export const x = 1;\n", "chore: initial")
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(tc.to)), 0o755); err != nil {
				t.Fatal(err)
			}
			gittest.Git(t, dir, "mv", tc.from, tc.to)

			report, err := Run(context.Background(), repo, dir, Options{Staged: true})
			if err != nil {
				t.Fatalf("Run --staged after %s -> %s: %v", tc.from, tc.to, err)
			}
			if _, ok := findFile(report.Files, tc.to); !ok {
				t.Errorf("report.Files = %+v; want an entry for %s", report.Files, tc.to)
			}
		})
	}
}
