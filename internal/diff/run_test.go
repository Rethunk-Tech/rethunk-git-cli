package diff

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// newDiffTestRepo is a self-contained temp repo, matching gitx_test.go's own
// style rather than reusing package main's unexported test helpers (this is
// a different package). One committed file, "b.py", holds a real Python
// symbol so an anchor can resolve against it either at HEAD or worktree.
func newDiffTestRepo(t *testing.T) (dir string, repo *gitx.Repo) {
	t.Helper()
	dir = t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("checkout", "-q", "-B", "main")
	run("config", "user.email", "diff-test@example.com")
	run("config", "user.name", "Diff Test")

	writeDiffFile(t, dir, "b.py", "def existing():\n    return 1\n")
	run("add", "b.py")
	run("commit", "-q", "-m", "chore: initial b.py")

	return dir, gitx.New(dir)
}

func writeDiffFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
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

	var rerr *resolve.ResolveError
	if !errors.As(err, &rerr) {
		t.Fatalf("Run error = %v (%T); want *resolve.ResolveError", err, err)
	}
	if rerr.Code != exitcode.AnchorUnresolvable {
		t.Errorf("Code = %v; want %v", rerr.Code, exitcode.AnchorUnresolvable)
	}
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

	var rerr *resolve.ResolveError
	if !errors.As(err, &rerr) {
		t.Fatalf("Run error = %v (%T); want *resolve.ResolveError", err, err)
	}
	if rerr.Code != exitcode.AnchorUnresolvable {
		t.Errorf("Code = %v; want %v", rerr.Code, exitcode.AnchorUnresolvable)
	}
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
