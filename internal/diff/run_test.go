package diff

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// newDiffTestRepo is a temp repo with one committed file, "b.py", holding
// a real Python symbol so an anchor can resolve against it either at HEAD
// or in the worktree. The scaffolding itself lives in internal/gittest,
// which four packages were each carrying their own copy of.
func newDiffTestRepo(t *testing.T) (dir string, repo *gitx.Repo) {
	t.Helper()
	dir, repo = gittest.New(t)
	writeDiffFile(t, dir, "b.py", "def existing():\n    return 1\n")
	gittest.Git(t, dir, "add", "b.py")
	gittest.Git(t, dir, "commit", "-q", "-m", "chore: initial b.py")
	return dir, repo
}

func writeDiffFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	gittest.Write(t, dir, relPath, content)
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

// TestRun_UnresolvableSymReturnsResolveError pins the fix for the reported
// hazard: `rgit diff --sym b.py:doesNotExist` used to exit 0 with empty
// output, indistinguishable from "that symbol is clean" -- a typo silently
// inverting the check-before-commit workflow rgit diff exists to serve.
// `rgit commit --dry-run` already refused the identical value with a typed
// *resolve.ResolveError; Run must now produce the same error, unfiltered by
// whether the named file happens to have any uncommitted changes at all.
func TestRun_UnresolvableSymReturnsResolveError(t *testing.T) {
	dir, repo := newDiffTestRepo(t)
	// b.py has an uncommitted change -- the exact reproduction from the bug
	// report, where the old behaviour's silence was most misleading.
	writeDiffFile(t, dir, "b.py", "def existing():\n    return 2\n")

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
	dir, repo := newDiffTestRepo(t)

	_, err := Run(context.Background(), repo, dir, Options{
		Syms: []SymRef{{File: "nosuch.py", Name: "foo"}},
	})

	assertUnresolvable(t, err)
}

// TestRun_SymResolvesButUnchangedIsNotAnError pins the distinction the fix
// must preserve: a symbol that genuinely exists but has no uncommitted
// changes is still a clean, exit-0 result with no row for it -- only an
// anchor that does not resolve at all is an error.
func TestRun_SymResolvesButUnchangedIsNotAnError(t *testing.T) {
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

// TestValidateSym_PrefersNewSideThenFallsBackToOld exercises validateSym
// directly against every side-availability combination applyFilters'
// hazard rests on: resolving on the New side when present, falling back to
// Old when the file was deleted from the worktree, and failing when the
// name exists on neither.
func TestValidateSym_PrefersNewSideThenFallsBackToOld(t *testing.T) {
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
	dir, repo := newDiffTestRepo(t)
	writeDiffFile(t, dir, "pre-commit", "#!/usr/bin/env bash\n\nfoo() {\n  echo v1\n}\n\nbar() {\n  echo bar\n}\n")
	gittest.Git(t, dir, "add", "pre-commit")
	gittest.Git(t, dir, "commit", "-q", "-m", "add pre-commit")

	// Edit foo only; bar stays clean and must not appear as a row.
	writeDiffFile(t, dir, "pre-commit", "#!/usr/bin/env bash\n\nfoo() {\n  echo v2\n}\n\nbar() {\n  echo bar\n}\n")

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

	// A deleted extensionless script has no worktree copy left to peek --
	// PeekShebangLine returns ok=false, and this degrades to the same
	// whole-file row any other unsupported extension already gets, not a
	// missed case.
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
	if len(got.Rows) != 1 || got.Rows[0].Symbol != "" || got.Rows[0].Status != StatusNoSymbols {
		t.Errorf("deleted pre-commit rows = %+v; want one whole-file StatusNoSymbols row", got.Rows)
	}
}

// TestRun_SymFilterMatchesAnyAcceptedAliasSpelling pins the fix for a
// silent-empty-diff hazard identical in shape to the one
// TestRun_UnresolvableSymReturnsResolveError already covers: applyFilters
// used to match a --sym request against Row.Symbol using the caller's own
// literal string, but Row.Symbol is always rgit's canonical, emitted
// spelling (resolve.DeclOrder) -- never one of the alternate spellings
// resolve.Resolve accepts on input but never produces (docs/ANCHORS.md):
// gopls's "(*A).Get" receiver form, or a Markdown heading's raw text. Both
// used to resolve cleanly (no error, exit 0) and then filter to zero rows,
// indistinguishable from "that symbol is clean" -- the same false-negative
// this package's validateSyms already exists to prevent for a name that
// does not resolve at all.
//
// One fixture covers every aliasing shape at once rather than one test per
// language: a canonical name (must keep working), gopls's receiver spelling,
// and a Markdown heading's raw text.
func TestRun_SymFilterMatchesAnyAcceptedAliasSpelling(t *testing.T) {
	dir, repo := newDiffTestRepo(t)

	writeDiffFile(t, dir, "a.go", "package p\n\ntype A struct{}\n\nfunc (a *A) Get() int { return 1 }\n")
	writeDiffFile(t, dir, "doc.md", "# Diff Scope\n\nOriginal.\n")
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("add", "a.go", "doc.md")
	runGit("commit", "-q", "-m", "chore: add a.go and doc.md")

	writeDiffFile(t, dir, "a.go", "package p\n\ntype A struct{}\n\nfunc (a *A) Get() int { return 2 }\n")
	writeDiffFile(t, dir, "doc.md", "# Diff Scope\n\nEdited.\n")

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

// TestRun_DegradedCrossCheckSetsTSOnly pins the signal docs/INSTALL.md §
// Verify's recipe greps for. crossCheckFile used to discard the degraded
// bool CrossCheckExtents returns, so `rgit diff` was silent whether or not a
// server was reached -- and the documented way to tell the difference always
// answered "cross-check active". rgit commit reported it all along.
func TestRun_DegradedCrossCheckSetsTSOnly(t *testing.T) {
	dir, repo := newDiffTestRepo(t)
	writeDiffFile(t, dir, "b.py", "def existing():\n    return 2\n")
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
	writeDiffFile(t, dir, "empty.py", "# no declarations here\n")
	cmd := exec.Command("git", "add", "empty.py")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}

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

// TestAttribute_TopLevelSymbolOwnsOneSeparator pins the fix for a row that
// told the reader to do the one thing rgit exists to avoid.
//
// Adding or removing a top-level declaration also moves the blank line
// between it and its neighbour. internal/synth moves exactly one such line
// (joinWithSeparator for an insert, spliceExcise's gap collapse for a
// delete), but attribution counted only the declaration's own extent, so
// that line fell to (unanchorable) -- a row carrying "-> use --file X",
// advising a whole-path stage that was already unnecessary. Measured before
// the fix: staging the anchor alone left the tree clean in Go and
// TypeScript, with the row claiming work that did not exist.
//
// Python is the case that proves the rule is "one separator", not "all
// adjacent blank lines": PEP 8 writes two, synth still inserts one, and the
// second genuinely does remain unowned -- so exactly one (unanchorable)
// line must survive there.
func TestAttribute_TopLevelSymbolOwnsOneSeparator(t *testing.T) {
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
			dir, repo := gittest.New(t)
			gittest.Write(t, dir, tc.path, tc.head)
			gittest.Commit(t, dir, "chore: fixture")
			gittest.Write(t, dir, tc.path, tc.work)

			if got := describeRows(rowsFor(t, dir, repo, tc.path)); got != tc.wantRows {
				t.Errorf("rows = %s; want %s", got, tc.wantRows)
			}
		})
	}
}

// TestRun_ScopeUsageErrorsAreTyped covers the scope contradictions Run
// detects itself. They must arrive as *UsageError, because internal/app
// maps that type — and nothing else — to exit 129 rather than to the
// exit 128 a git-level failure gets; the distinction is what tells a caller
// whether they mistyped the command or whether git broke.
func TestRun_ScopeUsageErrorsAreTyped(t *testing.T) {
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
