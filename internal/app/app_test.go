// Unit coverage for rgit's command surface. This package exists outside
// main precisely so every entry point is reachable without building and
// executing a binary (see app.go), and these cases are what makes that
// claim pay: rgit_e2e_test.go is the slow lane and skips under -short, so
// anything only it proves is a gap rather than coverage.
//
// Run drives real git against a real temporary repository -- no gitx
// stand-in -- so a case fails here for the same reason it would fail for a
// user. Every case changes directory, since openRepo resolves the
// repository from the working directory, which is also why none of them
// call t.Parallel: t.Chdir forbids it.
package app

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	diffpkg "github.com/Rethunk-Tech/rethunk-git-cli/internal/diff"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsptest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// runApp invokes the command surface exactly as main does and returns what
// a caller would see.
func runApp(t *testing.T, args ...string) (stdout, stderr string, code exitcode.Code) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = Run(context.Background(), "v0.0.0-test", args, &out, &errBuf)
	return out.String(), errBuf.String(), code
}

// chdirTempRepo creates a real repository with one committed Go file and
// makes it the working directory for the duration of the test.
func chdirTempRepo(t *testing.T) string {
	t.Helper()
	dir, _ := gittest.New(t)

	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gittest.Commit(t, dir, "chore: initial")

	t.Chdir(dir)
	return dir
}

// writeAppFile is a thin wrapper over gittest.Write, kept rather than
// calling gittest.Write directly at its ~49 call sites across this package
// for the same reason gitOut wraps gittest.Git below it: every test in
// this package reaches the temp repo through an "App"-local name, so a
// future change to what a package-level fixture call looks like here (a
// second argument, a different return shape) is one signature to edit,
// not every call site in every file in this package.
func writeAppFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	gittest.Write(t, dir, rel, content)
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gittest.Git(t, dir, args...)
}

// TestRun_TopLevelDispatch covers every route that returns before a
// repository is ever opened: the three help spellings, --version, an
// unknown command, and the bare invocation docs/CODES.md pins at 129.
func TestRun_TopLevelDispatch(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, tc := range []struct {
		name       string
		args       []string
		wantCode   exitcode.Code
		wantStdout string
		wantStderr string
	}{
		{name: "--help", args: []string{"--help"}, wantStdout: "usage: rgit"},
		{name: "-h", args: []string{"-h"}, wantStdout: "usage: rgit"},
		{name: "help", args: []string{"help"}, wantStdout: "usage: rgit"},
		{name: "--version", args: []string{"--version"}, wantStdout: "rgit v0.0.0-test"},
		{
			name: "unknown command", args: []string{"frobnicate"},
			wantCode: exitcode.InvalidUsage, wantStderr: `unknown command "frobnicate"`,
		},
		{
			name: "no command at all", args: nil,
			wantCode: exitcode.InvalidUsage, wantStderr: "usage: rgit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runApp(t, tc.args...)
			qt.Assert(t, qt.Equals(code, tc.wantCode))
			if tc.wantStdout != "" {
				qt.Assert(t, qt.StringContains(stdout, tc.wantStdout))
			}
			if tc.wantStderr != "" {
				qt.Assert(t, qt.StringContains(stderr, tc.wantStderr))
			}
		})
	}
}

// TestRun_SubcommandHelp pins that each subcommand's help prints to stdout
// at exit 0 rather than being treated as the usage error every other parse
// failure produces -- pflag reports both as an error from Parse.
func TestRun_SubcommandHelp(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, args := range [][]string{
		{"diff", "--help"}, {"diff", "-h"},
		{"commit", "--help"}, {"commit", "-h"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, _, code := runApp(t, args...)
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			qt.Assert(t, qt.StringContains(stdout, "usage: rgit "+args[0]))
			qt.Assert(t, qt.StringContains(stdout, "--sym"))
		})
	}
}

// TestRun_UsageErrors covers every validation that fires before a
// repository is opened. They share one case list because they share one
// exit code: docs/CODES.md gives 129 to bad flags, missing messages, no
// targets and invalid combinations alike.
func TestRun_UsageErrors(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{{
		name: "commit: -m and -F are mutually exclusive",
		args: []string{"commit", "-m", "feat(x): y", "-F", "msg.txt", "a.go"},
		want: "-m and -F are mutually exclusive",
	}, {
		name: "commit: --dry-run and --push are mutually exclusive",
		args: []string{"commit", "--dry-run", "--push", "-m", "feat(x): y", "a.go"},
		want: "--dry-run and --push are mutually exclusive",
	}, {
		name: "commit: --porcelain and --quiet are mutually exclusive",
		args: []string{"commit", "--porcelain", "--quiet", "-m", "feat(x): y", "a.go"},
		want: "--porcelain and --quiet are mutually exclusive",
	}, {
		name: "commit: a message is required",
		args: []string{"commit", "a.go"},
		want: "commit requires a message",
	}, {
		name: "commit: at least one target is required",
		args: []string{"commit", "-m", "feat(x): y"},
		want: "commit requires at least one target",
	}, {
		name: "commit: an unknown flag is a usage error",
		args: []string{"commit", "--no-such-flag", "-m", "feat(x): y", "a.go"},
		want: "unknown flag",
	}, {
		name: "diff: --staged and --range are mutually exclusive",
		args: []string{"diff", "--staged", "--range", "HEAD~1..HEAD"},
		want: "--staged and --range are mutually exclusive",
	}, {
		name: "diff: --staged and --unstaged are mutually exclusive",
		args: []string{"diff", "--staged", "--unstaged"},
		want: "--staged and --unstaged are mutually exclusive",
	}, {
		// docs/USAGE.md's "Invalid combinations" table lists this pair
		// generically, not scoped to commit: unenforced on diff, `rgit
		// diff --porcelain --quiet` would exit 0 printing nothing,
		// indistinguishable from "nothing to commit".
		name: "diff: --porcelain and --quiet are mutually exclusive",
		args: []string{"diff", "--porcelain", "--quiet"},
		want: "--porcelain and --quiet are mutually exclusive",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runApp(t, tc.args...)
			qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
			qt.Assert(t, qt.StringContains(stderr, tc.want))
		})
	}
}

// TestRun_ChdirBeforeCommand pins the global -C flag against git's own: the
// repository is resolved from <path> instead of the process working
// directory, on every command that opens one. Each case runs from a
// directory that is not a repository at all, so a subcommand that still
// reached for the working directory fails rather than quietly agreeing --
// which is why one test covers all five: the threading is per-call-site,
// and a call site left passing the working directory compiles fine.
func TestRun_ChdirBeforeCommand(t *testing.T) {
	repo := chdirTempRepo(t)
	writeAppFile(t, repo, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	writeAppFile(t, repo, "pkg/deep/c.go", "package deep\n\nfunc C() int {\n\treturn 1\n}\n")
	gitOut(t, repo, "add", "-A")

	// Still standing in the repository: git documents `-C ""` as a no-op,
	// not as an error and not as "the root".
	t.Run(`-C "" is a no-op`, func(t *testing.T) {
		stdout, _, code := runApp(t, "-C", "", "diff", "--porcelain")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "a.go"))
	})

	outside := t.TempDir()
	t.Chdir(outside)

	t.Run("diff", func(t *testing.T) {
		stdout, _, code := runApp(t, "-C", repo, "diff", "--porcelain")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "a.go\tA"))
	})

	// Two -C options are cumulative, the second read relative to the first,
	// and the pair lands on a subdirectory -- so this also pins that the
	// invocation prefix comes from where -C put us: a bare "c.go" resolves
	// against pkg/deep while output stays root-relative, exactly as it does
	// for a caller who actually stood there (TestRun_DiffFromSubdirectory).
	t.Run("cumulative, second relative to the first", func(t *testing.T) {
		stdout, _, code := runApp(t, "-C", repo, "-C", "pkg/deep", "diff", "--porcelain", "c.go")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "pkg/deep/c.go"))
	})

	t.Run("blame", func(t *testing.T) {
		stdout, _, code := runApp(t, "-C", repo, "blame", "a.go:B")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "func B()"))
	})

	t.Run("log", func(t *testing.T) {
		stdout, _, code := runApp(t, "-C", repo, "log", "a.go:B")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "chore: initial"))
	})

	t.Run("context", func(t *testing.T) {
		stdout, _, code := runApp(t, "-C", repo, "context")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "chore: initial"))
	})

	// Last: this one writes, so every read-only case above sees the same
	// worktree.
	t.Run("commit", func(t *testing.T) {
		stdout, _, code := runApp(t, "-C", repo, "commit", "-m", "fix(a): bump A", "a.go:A")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "a.go:A"))
		qt.Assert(t, qt.StringContains(gitOut(t, repo, "cat-file", "-p", "HEAD:a.go"), "return 111"))
	})
}

// TestRun_ChdirRefusals pins the shapes -C refuses, and the code each
// exits with: git reports a missing directory argument as a usage error
// (129) and a directory it cannot enter as a fatal (128), and rgit's own
// table already spells both. The bad directory is validated whatever
// follows it, including a command that never opens a repository -- git
// chdirs before it dispatches, so `doctor` cannot be the one invocation
// where a broken -C passes silently.
func TestRun_ChdirRefusals(t *testing.T) {
	dir := chdirTempRepo(t)

	for _, tc := range []struct {
		name string
		args []string
		want string
		code exitcode.Code
	}{{
		name: "no directory given",
		args: []string{"-C"},
		want: "no directory given for '-C'",
		code: exitcode.InvalidUsage,
	}, {
		// git rejects the glued spelling as an unknown option; rgit exits
		// the same 129 but names the fix, since -C exists here precisely
		// for callers writing git-shaped commands from memory.
		name: "glued -C<path>",
		args: []string{"-C" + dir, "diff"},
		want: "separate argument",
		code: exitcode.InvalidUsage,
	}, {
		name: "nonexistent directory",
		args: []string{"-C", filepath.Join(dir, "no-such-dir"), "diff"},
		want: "cannot change to",
		code: exitcode.GitFailure,
	}, {
		name: "not a directory",
		args: []string{"-C", filepath.Join(dir, "a.go"), "doctor"},
		want: "not a directory",
		code: exitcode.GitFailure,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runApp(t, tc.args...)
			qt.Assert(t, qt.Equals(code, tc.code))
			qt.Assert(t, qt.StringContains(stderr, tc.want))
		})
	}
}

// TestRun_CommitStagesOneSymbol is the happy path through the whole
// surface: classification, target building, synthesis, the real git commit,
// and the per-target listing git cannot produce itself.
func TestRun_CommitStagesOneSymbol(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 222\n}\n")

	stdout, _, code := runApp(t, "commit", "-m", "fix(a): bump A", "a.go:A")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "a.go:A"))

	// Only A's extent was staged; B's worktree edit stayed behind.
	committed := gitOut(t, dir, "cat-file", "-p", "HEAD:a.go")
	qt.Assert(t, qt.StringContains(committed, "return 111"))
	qt.Assert(t, qt.StringContains(committed, "return 2\n"))
	qt.Assert(t, qt.Not(qt.StringContains(committed, "return 222")))
}

// TestRun_CommitDryRunAndPorcelain pins the two output modes against each
// other on the same targets: --dry-run must write nothing, and --porcelain
// must emit records with no human preamble.
func TestRun_CommitDryRunAndPorcelain(t *testing.T) {
	dir := chdirTempRepo(t)
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	plain, _, code := runApp(t, "commit", "--dry-run", "-m", "fix(a): bump", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(plain, "dry run:"))

	records, _, code := runApp(t, "commit", "--dry-run", "--porcelain", "-m", "fix(a): bump", "a.go:A")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Not(qt.StringContains(records, "dry run:")))
	qt.Assert(t, qt.StringContains(records, "a.go\tA\t"))
	for line := range strings.SplitSeq(strings.TrimRight(records, "\n"), "\n") {
		qt.Assert(t, qt.Equals(len(strings.Split(line, "\t")), 4))
	}

	// Neither wrote anything: HEAD is where it was.
	qt.Assert(t, qt.Equals(strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")), before))
}

// TestRun_CommitQuietSuppressesStdoutOnly separates output from
// diagnostics: -q silences the summary and the listing, while the warning
// for a target with nothing to commit still reaches stderr.
func TestRun_CommitQuietSuppressesStdoutOnly(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	stdout, stderr, code := runApp(t, "commit", "-q", "-m", "fix(a): bump", "a.go:A", "a.go:B")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stdout, ""))
	qt.Assert(t, qt.StringContains(stderr, "has no uncommitted changes"))
	qt.Assert(t, qt.StringContains(gitOut(t, dir, "cat-file", "-p", "HEAD:a.go"), "return 111"))
}

// TestRun_CommitExitCodes covers the mapped failures that need a real
// repository to reach: a contradiction between the two spellings of one
// path, an unresolvable anchor, and every named target already clean.
func TestRun_CommitExitCodes(t *testing.T) {
	dir := chdirTempRepo(t)

	t.Run("one path named both ways is exit 5", func(t *testing.T) {
		writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 3\n}\n\nfunc B() int {\n\treturn 2\n}\n")
		_, stderr, code := runApp(t, "commit", "-m", "fix(a): x", "a.go", "a.go:A")
		qt.Assert(t, qt.Equals(code, exitcode.ContradictoryAnchors))
		qt.Assert(t, qt.StringContains(stderr, "named both as a path and as a symbol anchor"))
	})

	t.Run("an unresolvable anchor is exit 3", func(t *testing.T) {
		_, stderr, code := runApp(t, "commit", "-m", "fix(a): x", "a.go:NoSuchSymbol")
		qt.Assert(t, qt.Equals(code, exitcode.AnchorUnresolvable))
		qt.Assert(t, qt.StringContains(stderr, "NoSuchSymbol"))
	})

	// "NoSuchSymbol" above is beyond suggest's maxDistance=3 from either
	// A or B, so it never produces a candidate at all -- it cannot catch a
	// regression that only drops did-you-mean candidates while leaving the
	// bare exit-3 unresolved case intact. "DoesExit" is one insertion away
	// from "DoesExist" (edit distance 1, well inside maxDistance), so
	// synth's classify actually has a candidate to lose here (fix(synth):
	// keep did-you-mean candidates on the commit path).
	t.Run("an unresolvable anchor close to a real one suggests it", func(t *testing.T) {
		writeAppFile(t, dir, "b.go", "package a\n\nfunc DoesExist() int {\n\treturn 1\n}\n")
		_, stderr, code := runApp(t, "commit", "-m", "fix(a): x", "b.go:DoesExit")
		qt.Assert(t, qt.Equals(code, exitcode.AnchorUnresolvable))
		qt.Assert(t, qt.StringContains(stderr, "did you mean: DoesExist?"))
	})

	t.Run("every target unchanged is exit 11 and names each skipped target", func(t *testing.T) {
		writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
		_, stderr, code := runApp(t, "commit", "-m", "fix(a): x", "a.go:A", "a.go:B")
		qt.Assert(t, qt.Equals(code, exitcode.NothingToCommit))
		qt.Assert(t, qt.StringContains(stderr, "target 'a.go:A' has no uncommitted changes"))
		qt.Assert(t, qt.StringContains(stderr, "target 'a.go:B' has no uncommitted changes"))
	})

	t.Run("--allow-empty suppresses exit 11", func(t *testing.T) {
		_, _, code := runApp(t, "commit", "--allow-empty", "-m", "chore: empty", "a.go:A")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
	})
}

// TestRun_CommitWarnsOnNonConventionalMessage pins the advisory: the shape
// check writes to stderr and must never change the outcome.
func TestRun_CommitWarnsOnNonConventionalMessage(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 9\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	_, stderr, code := runApp(t, "commit", "-m", "just some words", "a.go:A")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stderr, "does not look like"))
}

// TestRun_PathEscapeIsRefused covers the one piece of path safety rgit owns
// rather than delegating to git, on both subcommands and in both spellings.
//
// The escape check runs on a path that classified successfully, so a
// nonexistent "../outside.go" never reaches it -- rule 6 rejects it first,
// for a different reason and with a different message. --file skips
// classification entirely and goes straight to it; the positional case
// needs a file that genuinely exists above the root to get that far.
func TestRun_PathEscapeIsRefused(t *testing.T) {
	dir := chdirTempRepo(t)
	outside := filepath.Join(filepath.Dir(dir), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"commit", "-m", "feat(x): y", "--file", "../outside.go"},
		{"diff", "--file", "../outside.go"},
		{"commit", "-m", "feat(x): y", "--sym", "../outside.go:Thing"},
		{"commit", "-m", "feat(x): y", "../outside.go"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, stderr, code := runApp(t, args...)
			qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
			qt.Assert(t, qt.StringContains(stderr, "escapes the repository root"))
		})
	}
}

// TestRun_MalformedSymIsRefused pins that a --sym value with no name is
// rejected on both commands rather than silently ignored, which would
// leave a caller reading an unfiltered diff believing it was filtered.
func TestRun_MalformedSymIsRefused(t *testing.T) {
	chdirTempRepo(t)

	for _, args := range [][]string{
		{"commit", "-m", "feat(x): y", "--sym", "a.go"},
		{"diff", "--sym", "a.go"},
	} {
		_, stderr, code := runApp(t, args...)
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "malformed --sym value"))
	}
}

// TestRun_DiffScopesAndOutput walks the diff surface: the default scope,
// --porcelain's records, --sym filtering, and the --quiet/--exit-code pair
// that makes it scriptable.
func TestRun_DiffScopesAndOutput(t *testing.T) {
	dir := chdirTempRepo(t)

	t.Run("clean tree prints nothing and exits 0", func(t *testing.T) {
		stdout, _, code := runApp(t, "diff")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stdout, ""))
	})

	t.Run("--quiet on a clean tree exits 0", func(t *testing.T) {
		stdout, _, code := runApp(t, "diff", "--quiet")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stdout, ""))
	})

	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	t.Run("default scope attributes the hunk to its symbol", func(t *testing.T) {
		stdout, _, code := runApp(t, "diff")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "a.go"))
		qt.Assert(t, qt.StringContains(stdout, "A"))
	})

	t.Run("--porcelain emits five tab-separated fields", func(t *testing.T) {
		stdout, _, code := runApp(t, "diff", "--porcelain")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		for line := range strings.SplitSeq(strings.TrimRight(stdout, "\n"), "\n") {
			qt.Assert(t, qt.Equals(len(strings.Split(line, "\t")), 5))
		}
		qt.Assert(t, qt.StringContains(stdout, "a.go\tA\tMOD\t"))
	})

	t.Run("--sym filters to one anchor", func(t *testing.T) {
		stdout, _, code := runApp(t, "diff", "--porcelain", "--sym", "a.go:A")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "a.go\tA\t"))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "\tB\t")))
	})

	t.Run("--exit-code reports a dirty tree as 1", func(t *testing.T) {
		_, _, code := runApp(t, "diff", "--exit-code")
		qt.Assert(t, qt.Equals(code, exitcode.Code(1)))
	})

	t.Run("--quiet implies --exit-code and prints nothing", func(t *testing.T) {
		stdout, _, code := runApp(t, "diff", "--quiet")
		qt.Assert(t, qt.Equals(code, exitcode.Code(1)))
		qt.Assert(t, qt.Equals(stdout, ""))
	})

	t.Run("an unresolvable --sym is refused, not reported clean", func(t *testing.T) {
		_, stderr, code := runApp(t, "diff", "--sym", "a.go:NoSuchSymbol")
		qt.Assert(t, qt.Equals(code, exitcode.AnchorUnresolvable))
		qt.Assert(t, qt.StringContains(stderr, "NoSuchSymbol"))
	})

	// docs/ANCHORS.md's ordinal-anchor advisory, mirrored from commit onto
	// diff's own --sym form: an anchor resolved by position warns on
	// stderr, but only when it is actually ordinal-shaped.
	writeAppFile(t, dir, "dup.go", "package a\n\nfunc init() { println(1) }\n\nfunc init() { println(2) }\n")

	t.Run("an ordinal --sym anchor warns", func(t *testing.T) {
		_, stderr, code := runApp(t, "diff", "--sym", "dup.go:init#2")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stderr, "dup.go:init#2"))
		qt.Assert(t, qt.StringContains(stderr, "positional"))
	})

	t.Run("a uniquely named --sym anchor does not warn", func(t *testing.T) {
		_, stderr, code := runApp(t, "diff", "--sym", "a.go:A")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Not(qt.StringContains(stderr, "positional")))
	})
}

// TestRun_DiffPatchFlag covers -p/--patch on rgit diff: it reaches git and
// carries real content, is mutually exclusive with --porcelain the same way
// --quiet already is, and -- the requirement the design settled on --
// leaves the non--patch default output completely untouched. That last
// case is proven structurally rather than by a hand-copied expected
// string: runDiff's own render block is `RenderText(report)` plus, only
// when -p was given, the patch appended after it -- so calling
// diffpkg.Run/RenderText directly with the identical Options (Patch left
// false) and comparing byte-for-byte against the CLI's own default output
// is what would catch an accidental extra byte on the non-patch path,
// without this test also being the thing that goes stale the next time
// RenderText's own format changes.
func TestRun_DiffPatchFlag(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	t.Run("default output is unchanged by the patch feature's existence", func(t *testing.T) {
		stdout, _, code := runApp(t, "diff")
		qt.Assert(t, qt.Equals(code, exitcode.Success))

		repo := gitx.New(dir)
		report, err := diffpkg.Run(context.Background(), repo, dir, diffpkg.Options{})
		if err != nil {
			t.Fatalf("diffpkg.Run: %v", err)
		}
		qt.Assert(t, qt.Equals(stdout, diffpkg.RenderText(report)))
	})

	t.Run("-p/--patch includes the real patch body after the report", func(t *testing.T) {
		for _, flag := range []string{"-p", "--patch"} {
			t.Run(flag, func(t *testing.T) {
				stdout, _, code := runApp(t, "diff", flag)
				qt.Assert(t, qt.Equals(code, exitcode.Success))
				qt.Assert(t, qt.StringContains(stdout, "a.go"))
				qt.Assert(t, qt.StringContains(stdout, "diff --git"))
				qt.Assert(t, qt.StringContains(stdout, "return 111"))
			})
		}
	})

	t.Run("--porcelain and --patch are mutually exclusive", func(t *testing.T) {
		_, stderr, code := runApp(t, "diff", "--porcelain", "--patch")
		qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
		qt.Assert(t, qt.StringContains(stderr, "mutually exclusive"))
	})

	t.Run("--quiet suppresses the patch body too", func(t *testing.T) {
		stdout, _, code := runApp(t, "diff", "--patch", "--quiet")
		qt.Assert(t, qt.Equals(code, exitcode.Code(1)))
		qt.Assert(t, qt.Equals(stdout, ""))
	})
}

// TestRun_DiffFromSubdirectory pins git's own rule: a path is relative to
// where you stand, while output stays root-relative.
func TestRun_DiffFromSubdirectory(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "pkg/deep/c.go", "package deep\n\nfunc C() int {\n\treturn 1\n}\n")
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}

	t.Chdir(filepath.Join(dir, "pkg", "deep"))
	stdout, _, code := runApp(t, "diff", "--porcelain", "c.go")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	// Named as c.go from pkg/deep, reported as pkg/deep/c.go.
	qt.Assert(t, qt.StringContains(stdout, "pkg/deep/c.go"))
}

// TestExpandGPGSignShorthand pins the argv rewrite that makes git's own -S
// spelling work. pflag resolves an optional-value shorthand's NoOptDefVal
// before it looks for an attached value, so registering -S directly makes
// git's idiomatic -Skeyid parse as a chain of nonexistent single-letter
// flags. Rewriting the token before Parse sidesteps that entirely.
//
// The rule is getopt's, which is git's: for a short option taking an
// optional argument, whatever follows in the same token IS the argument.
// So -Ss means the key "s", not "sign plus signoff" -- the reason this
// cannot be a general shorthand-chain expansion.
// TestExtForFailedSym pins the lookup directly against
// *resolve.ResolveError's own Path field, now that validateSym
// (internal/diff/run.go) populates it: two ResolveErrors naming the
// identical bare anchor but different Paths must resolve to their own
// file's extension, the exact collision the prior allSyms-name-matching
// implementation could not tell apart.
func TestExtForFailedSym(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		rerr    *resolve.ResolveError
		wantExt string
		wantOK  bool
	}{
		{
			name:    "path present",
			rerr:    &resolve.ResolveError{Code: exitcode.UnsupportedLanguage, Anchor: "Shared", Path: "a.sql"},
			wantExt: ".sql",
			wantOK:  true,
		},
		{
			name:    "a different file sharing the same bare anchor name",
			rerr:    &resolve.ResolveError{Code: exitcode.UnsupportedLanguage, Anchor: "Shared", Path: "b.sql"},
			wantExt: ".sql",
			wantOK:  true,
		},
		{
			name:   "no path set",
			rerr:   &resolve.ResolveError{Code: exitcode.UnsupportedLanguage, Anchor: "Shared"},
			wantOK: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext, ok := extForFailedSym(tc.rerr)
			qt.Assert(t, qt.Equals(ok, tc.wantOK))
			qt.Assert(t, qt.Equals(ext, tc.wantExt))
		})
	}
}

func TestExpandGPGSignShorthand(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"bare -S", []string{"-S", "a.go"}, []string{"--gpg-sign", "a.go"}},
		{"attached key id", []string{"-SDEADBEEF"}, []string{"--gpg-sign=DEADBEEF"}},
		{"single-letter key id", []string{"-Ss"}, []string{"--gpg-sign=s"}},
		{"long form untouched", []string{"--gpg-sign=X"}, []string{"--gpg-sign=X"}},
		{"other shorthands untouched", []string{"-s", "-m", "x"}, []string{"-s", "-m", "x"}},
		{"nothing after -- is rewritten", []string{"--", "-Sfile"}, []string{"--", "-Sfile"}},
		{"a lone dash is not a flag", []string{"-"}, []string{"-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := expandGPGSignShorthand(tc.in)
			qt.Assert(t, qt.DeepEquals(got, tc.want))
		})
	}
}

// TestRestoreDoubleDash_ReconstructsThroughRealPflag proves the actual
// wiring cli.ClassifyArgs's rule 1 depends on. fs.Args() alone loses the
// fact that "--" was ever present; only a real pflag.FlagSet's own
// ArgsLenAtDash says where it was, and internal/cli's own precedence_test.go
// covers rule 1 with a hand-built slice that already contains "--" --
// never touching this function at all. A regression here (say, pflag
// changing what ArgsLenAtDash reports, or an off-by-one in the splice)
// would still pass `go test -short ./...` without this.
func TestRestoreDoubleDash_ReconstructsThroughRealPflag(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"-- present after a flag value", []string{"--sym", "a.go:A", "--", "b.go:B", "weird"}, []string{"--", "b.go:B", "weird"}},
		{"-- absent leaves positionals untouched", []string{"--sym", "a.go:A", "pathspec.go"}, []string{"pathspec.go"}},
		{"-- with nothing after it", []string{"a.go", "--"}, []string{"a.go", "--"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var syms, files []string
			fs := newTargetFlagSet("test", &syms, &files)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("Parse(%v): %v", tc.args, err)
			}
			qt.Assert(t, qt.DeepEquals(restoreDoubleDash(fs), tc.want))
		})
	}
}

// TestRun_GPGSignShorthandReachesGit closes the loop through the real
// command surface: -S must trigger signing exactly as --gpg-sign does.
// gpg.program pointed at a binary that always fails turns any signing
// attempt into a deterministic failure, which is proof the flag arrived.
func TestRun_GPGSignShorthandReachesGit(t *testing.T) {
	dir := chdirTempRepo(t)
	gittest.Git(t, dir, "config", "gpg.program", "/bin/false")
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 5\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	_, stderr, code := runApp(t, "commit", "-SDEADBEEF", "-m", "feat(a): signed", "a.go:A")

	qt.Assert(t, qt.Equals(code, exitcode.GitFailure))
	qt.Assert(t, qt.StringContains(stderr, "sign"))
}

// TestRun_PushFailureReportsUpstreamHint covers the --push branch the
// --dry-run cases above never reach: --dry-run returns before repo.Push is
// ever called, so it is the only shape of --push this file's other tests
// exercise, leaving repo.Push, repo.HasUpstream, repo.CurrentBranch, and
// exitcode.PushFailed's own message at 0% under -short even though the
// full lane covers all four -- a regression in any of them would pass
// `go test -short ./...` clean. A repo gittest builds has no remote
// configured at all, so `git push` fails for the plainest possible
// reason and HasUpstream's negative answer is real, not assumed.
//
// m26: this is deliberately pinned again in
// cmd/rgit/rgit_e2e_test.go's TestCommit_PushWithNoUpstreamNamesTheFix, the
// same dual-pin CONTRIBUTING.md sanctions for hook rejection
// (lanes_test.go's own TestRun_HookRejectionLeavesStagingIntact) -- but for
// a different reason than duplication would suggest. The two cases trigger
// genuinely different git failures that both leave HasUpstream negative:
// no remote configured at all here, versus a real remote with no upstream
// tracking there (git's own literal "no upstream branch" refusal). Losing
// this lane would still catch a regression in the message-construction
// code, but only the e2e twin proves the hint fires for the specific
// failure its own text names.
func TestRun_PushFailureReportsUpstreamHint(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	stdout, stderr, code := runApp(t, "commit", "--push", "-m", "fix(a): bump", "a.go:A")

	qt.Assert(t, qt.Equals(code, exitcode.PushFailed))
	// AGENTS.md: a push failure never rolls back the commit that preceded
	// it -- it already landed by the time Push is even attempted.
	qt.Assert(t, qt.StringContains(stdout, "a.go:A"))
	qt.Assert(t, qt.StringContains(gitOut(t, dir, "cat-file", "-p", "HEAD:a.go"), "return 111"))
	// The concrete, named suggestion commit.go's own doc comment promises:
	// branch "main" (gittest.New forces it) and the exact command to run.
	qt.Assert(t, qt.StringContains(stderr, "main has no upstream tracking branch"))
	qt.Assert(t, qt.StringContains(stderr, "git push -u origin main"))
}

// TestRun_PathspecListsEveryFileItStages asserts a pathspec naming a
// directory lists one row per file it stages, not one aggregate row for
// the whole pathspec, so a caller can see what moved under it, not merely
// that something did. `rgit diff` already breaks the same change down per
// file, and the two are supposed to agree.
//
// Each row also has to match git's own numstat for that file, since the
// aggregate it replaced did.
func TestRun_PathspecListsEveryFileItStages(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "apps/svc/one.go", "package svc\n\nfunc One() int {\n\treturn 1\n}\n")
	writeAppFile(t, dir, "apps/svc/notes.txt", "x\n")
	gittest.Commit(t, dir, "chore: fixture")

	writeAppFile(t, dir, "apps/svc/one.go", "package svc\n\nfunc One() int {\n\treturn 111\n}\n")
	writeAppFile(t, dir, "apps/svc/notes.txt", "x\ny\nz\n")
	// An untracked file under the same directory is staged by the same
	// pathspec, so it belongs in the listing too.
	writeAppFile(t, dir, "apps/svc/two.go", "package svc\n\nfunc Two() int {\n\treturn 2\n}\n")

	stdout, _, code := runApp(t, "commit", "--dry-run", "--porcelain", "-m", "chore: svc", "apps/svc")
	qt.Assert(t, qt.Equals(code, exitcode.Success))

	got := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimRight(stdout, "\n"), "\n") {
		f := strings.Split(line, "\t")
		qt.Assert(t, qt.Equals(len(f), 4))
		got[f[0]] = f[2] + "/" + f[3]
	}
	want := map[string]string{
		"apps/svc/one.go":    "1/1",
		"apps/svc/notes.txt": "2/0",
		"apps/svc/two.go":    "5/0", // untracked: every line is an addition
	}
	qt.Assert(t, qt.DeepEquals(got, want))
}

// TestRun_PathspecMatchingNothingStillListsItself keeps git's own answer
// reachable: a pathspec that matches no file must not vanish from the
// listing, because `git add` is what reports "did not match any files" and
// it only gets the chance if the target is still staged.
func TestRun_PathspecMatchingNothingStillListsItself(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "kept/keep.txt", "x\n")
	gittest.Commit(t, dir, "chore: fixture")

	stdout, _, code := runApp(t, "commit", "--dry-run", "--porcelain", "-m", "chore: none", "kept")

	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "kept\t\t0\t0"))
}

// assertShellParses feeds script through "<shell> -n", the cheapest
// possible proof an emitted completion script is not garbage -- a script
// that fails to parse is the most obvious way this feature could break,
// and it costs one process fork to rule out. Skips when the shell binary
// is not on PATH, the same way resolver_test.go treats a missing gopls:
// a missing cross-check is never a failure.
func assertShellParses(t *testing.T, shell, script string) {
	t.Helper()
	path, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("%s not on PATH", shell)
	}
	f := filepath.Join(t.TempDir(), "rgit-completion")
	if err := os.WriteFile(f, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(path, "-n", f).CombinedOutput(); err != nil {
		t.Fatalf("%s -n %s: %v: %s", shell, f, err, out)
	}
}

// assertPwshParses asks PowerShell's own parser to validate the emitted script.
// PowerShell has no shellcheck-style -n switch, so this uses the parser API
// without executing the registration or completer.
func assertPwshParses(t *testing.T, script string) {
	t.Helper()
	path, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not on PATH")
	}

	file := filepath.Join(t.TempDir(), "rgit-completion.ps1")
	if err := os.WriteFile(file, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	const parseScript = `
$tokens = $null
$errors = $null
[void][System.Management.Automation.Language.Parser]::ParseFile(
    $env:RGIT_COMPLETION_SCRIPT,
    [ref]$tokens,
    [ref]$errors)
if ($null -ne $errors -and $errors.Count -gt 0) {
    $errors | ForEach-Object { $_.ToString() }
    exit 1
}
`
	cmd := exec.Command(path, "-NoProfile", "-NonInteractive", "-Command", parseScript)
	cmd.Env = append(os.Environ(), "RGIT_COMPLETION_SCRIPT="+file)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pwsh parser: %v: %s", err, output)
	}
}

// TestRun_Completion covers the completion subcommand: a script for each
// supported shell, syntactically valid by its own shell's judgment, and
// the usage error docs/CODES.md gives every other malformed argument for
// anything else.
func TestRun_Completion(t *testing.T) {
	t.Chdir(t.TempDir())

	t.Run("bash", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "completion", "bash")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "complete -F _rgit_completion rgit"))
		qt.Assert(t, qt.StringContains(stdout, "rgit diff --porcelain"))
		// docs/USAGE.md § Help: -h is --help's equivalent at the top level
		// and on every subcommand -- nine word lists (rgitSubcommands, and
		// each of diff/commit/blame/log/context/languages/doctor/completion's
		// own flags), so nine occurrences of the pair in the order
		// completion offers them.
		qt.Assert(t, qt.Equals(strings.Count(stdout, "-h --help"), 9))
		assertShellParses(t, "bash", stdout)
	})

	t.Run("zsh", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "completion", "zsh")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "compdef _rgit rgit"))
		qt.Assert(t, qt.StringContains(stdout, "rgit diff --porcelain"))
		qt.Assert(t, qt.Equals(strings.Count(stdout, "-h --help"), 9))
		assertShellParses(t, "zsh", stdout)
	})

	t.Run("fish", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "completion", "fish")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "complete -c rgit -f -a '(__rgit_complete)'"))
		qt.Assert(t, qt.StringContains(stdout, "rgit diff --porcelain"))
		qt.Assert(t, qt.Equals(strings.Count(stdout, "-h --help"), 9))
		assertShellParses(t, "fish", stdout)
	})

	t.Run("pwsh", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "completion", "pwsh")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "Register-ArgumentCompleter -Native -CommandName rgit"))
		qt.Assert(t, qt.StringContains(stdout, "param($wordToComplete, $commandAst, $cursorPosition)"))
		qt.Assert(t, qt.StringContains(stdout, "[System.Management.Automation.CompletionResult]::new"))
		qt.Assert(t, qt.StringContains(stdout, "--follow-rename"))
		qt.Assert(t, qt.Equals(strings.Count(stdout, "-h --help"), 9))
		assertPwshParses(t, stdout)
	})

	t.Run("--help prints usage and exits 0", func(t *testing.T) {
		stdout, _, code := runApp(t, "completion", "--help")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "usage: rgit completion"))
		qt.Assert(t, qt.StringContains(stdout, "pwsh"))
	})

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown shell", []string{"completion", "csh"}},
		{"missing shell", []string{"completion"}},
		{"too many args", []string{"completion", "bash", "zsh"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runApp(t, tc.args...)
			qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
			qt.Assert(t, qt.Equals(stdout, ""))
			qt.Assert(t, qt.Not(qt.Equals(stderr, "")))
			if tc.name == "unknown shell" {
				qt.Assert(t, qt.StringContains(stderr, "pwsh"))
			}
		})
	}
}

// TestRun_Languages pins the always-present part of the listing -- the
// eleven grammars every build carries regardless of -tags rgit_sql. Whether
// "sql" itself appears is build-specific and covered separately
// (languages_sql_test.go, languages_nosql_test.go), which is exactly why
// this case avoids asserting either way about it.
func TestRun_Languages(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, stderr, code := runApp(t, "languages")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	for _, want := range []string{"go", ".go", "css", ".css", "typescript", ".ts"} {
		qt.Assert(t, qt.StringContains(stdout, want))
	}
}

// TestRun_LanguagesPorcelain pins the record shape docs/CODES.md commits to
// -- four tab-separated fields, no header -- against the "go" entry every
// build carries, so this case holds regardless of -tags rgit_sql. Whether a
// "sql" record appears, and what its GATED field reads, is build-specific
// and covered separately (languages_sql_test.go, languages_nosql_test.go).
func TestRun_LanguagesPorcelain(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, stderr, code := runApp(t, "languages", "--porcelain")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	// No stray human decoration leaking into the machine form.
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "(build-tag gated)")))

	found := false
	for line := range strings.SplitSeq(strings.TrimRight(stdout, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		qt.Assert(t, qt.Equals(len(fields), 4))
		if fields[0] == "go" {
			found = true
			qt.Assert(t, qt.Equals(fields[1], ".go"))
			qt.Assert(t, qt.Equals(fields[2], "0"))
			qt.Assert(t, qt.Equals(fields[3], "wired"))
		}
	}
	qt.Assert(t, qt.IsTrue(found))
}

// TestRun_LanguagesInRepo pins the advisory filter: a repo with only a
// tracked .go file lists "go" and omits "python" (a grammar this binary
// always compiles in, per TestRun_Languages's own always-present list),
// even though the underlying binary still contains every grammar regardless.
func TestRun_LanguagesInRepo(t *testing.T) {
	chdirTempRepo(t) // commits a.go, a Go file, and nothing else

	stdout, stderr, code := runApp(t, "languages", "--in-repo")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.StringContains(stdout, "go"))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "python")))
}

// TestRun_LanguagesInRepoOutsideRepoErrors pins the other half of the
// contract: --in-repo needs a real repository to scan, unlike plain
// "rgit languages" (TestRun_Languages), and fails clearly rather than
// silently listing nothing or every grammar.
func TestRun_LanguagesInRepoOutsideRepoErrors(t *testing.T) {
	t.Chdir(t.TempDir())

	_, stderr, code := runApp(t, "languages", "--in-repo")
	qt.Assert(t, qt.Not(qt.Equals(code, exitcode.Success)))
	qt.Assert(t, qt.Not(qt.Equals(stderr, "")))
}

// TestRun_LanguagesHelpAndUsage covers the two non-listing paths: --help
// prints and exits 0, and an unexpected argument is the usual usage error.
func TestRun_LanguagesHelpAndUsage(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, _, code := runApp(t, "languages", "--help")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "usage: rgit languages"))

	stdout, stderr, code := runApp(t, "languages", "extra")
	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.Equals(stdout, ""))
	qt.Assert(t, qt.Not(qt.Equals(stderr, "")))
}

// TestRun_Doctor covers the happy path: every section prints, and a real
// test environment always has git on PATH, so the essential check passes
// regardless of which optional tools happen to be installed.
func TestRun_Doctor(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, stderr, code := runApp(t, "doctor")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.StringContains(stdout, "[ok]"))
	qt.Assert(t, qt.StringContains(stdout, "git"))
	qt.Assert(t, qt.StringContains(stdout, "Language servers"))
	qt.Assert(t, qt.StringContains(stdout, "Grammars compiled in"))
	qt.Assert(t, qt.StringContains(stdout, "go"))

	// Every resolved row's path starts at the same column, across the
	// Environment and Language servers sections both -- doctor measures one
	// width over the two rather than letting each align only against
	// itself. The byte-exact line shape, including the ok/MISSING status
	// padding, is pinned in internal/prereq's own test; what this adds is
	// that doctor shares a single width across sections.
	//
	// Only found tools are compared: a MISSING row's detail is a prose note,
	// not a path, so " /" would not locate its column. git is always found
	// here, and every language server present on the machine joins it.
	var cols []int
	for line := range strings.SplitSeq(stdout, "\n") {
		if !strings.HasPrefix(line, "  [ok]") {
			continue
		}
		if i := strings.Index(line, " /"); i >= 0 {
			cols = append(cols, i+1)
		}
	}
	qt.Assert(t, qt.IsTrue(len(cols) >= 1))
	for _, c := range cols[1:] {
		qt.Assert(t, qt.Equals(c, cols[0]))
	}
}

// TestRun_DoctorPorcelain pins docs/CODES.md's stable record shape: one
// KIND/NAME/STATUS/DETAIL row per check, no header, and no grammar rows --
// "rgit languages --porcelain" already owns that listing.
func TestRun_DoctorPorcelain(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, stderr, code := runApp(t, "doctor", "--porcelain")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "Environment:")))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "Grammars compiled in")))

	sawGit := false
	for line := range strings.SplitSeq(strings.TrimRight(stdout, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		qt.Assert(t, qt.Equals(len(fields), 4))
		qt.Assert(t, qt.IsTrue(fields[0] == "env" || fields[0] == "server"))
		if fields[0] == "env" && fields[1] == "git" {
			sawGit = true
			qt.Assert(t, qt.Equals(fields[2], "ok"))
		}
	}
	qt.Assert(t, qt.IsTrue(sawGit))
}

// TestRun_DoctorReportsGitVersion pins that doctor checks git's version, not
// only its presence: the real git on this machine (CONTRIBUTING.md's own
// hard requirement) always meets internal/prereq.MinGitVersion, so both
// output forms carry an "ok" git version row alongside the plain git row.
func TestRun_DoctorReportsGitVersion(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, _, code := runApp(t, "doctor")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "git version"))

	stdout, _, code = runApp(t, "doctor", "--porcelain")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "env\tgit version\tok\t"))
}

// TestRun_DoctorPorcelainMissingGitIsFatalWithMissingStatus pins that a
// fatal check still gets its normal MISSING record, on top of the usual
// exit 128 -- the porcelain stream is not suppressed by the failure.
func TestRun_DoctorPorcelainMissingGitIsFatalWithMissingStatus(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", t.TempDir())

	stdout, stderr, code := runApp(t, "doctor", "--porcelain")
	qt.Assert(t, qt.Equals(code, exitcode.GitFailure))
	qt.Assert(t, qt.StringContains(stderr, "git"))
	qt.Assert(t, qt.StringContains(stdout, "env\tgit\tMISSING\t"))
}

// isolatedPATHWithGopls returns a PATH containing only a real git (doctor's
// one fatal check) and a fake "gopls" binary -- isolated rather than
// prepended to the real PATH, so a language server that happens to be
// installed on the machine running this test is never actually dialed or
// spawned for real; gopls's own content is never executed either, since
// --deep reaches it only via $RGIT_LSP_SOCKET, never a real exec.
func isolatedPATHWithGopls(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	bin := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gopls"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestRun_DoctorDeepReportsReachable pins --deep's own success path: a
// managed socket that actually completes the initialize/initialized
// handshake reports "(reachable)" in the server's Detail column, on top of
// the unchanged on-PATH check. lsptest.ServeMockLSP serves exactly that
// handshake and nothing past it (dial.go's own Dial/Close never sends a
// didOpen), so the mock loop returns cleanly the moment Close tears the
// connection down.
func TestRun_DoctorDeepReportsReachable(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", isolatedPATHWithGopls(t))
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // never reach a real, already-running daemon at the managed default

	sockPath := filepath.Join(t.TempDir(), "gopls.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func() { _ = lsptest.ServeMockLSP(conn, "[]", lsptest.MockServerHooks{}) }()
		}
	}()
	t.Setenv("RGIT_LSP_SOCKET", sockPath)

	stdout, stderr, code := runApp(t, "doctor", "--porcelain", "--deep")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.StringContains(stdout, "gopls"))
	qt.Assert(t, qt.StringContains(stdout, "(reachable)"))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "degraded")))
}

// TestRun_DoctorDeepReportsDegraded is the other half: a socket that
// accepts but never answers the handshake (the shape a stuck or
// incompatible daemon leaves) reports "(degraded ...)" instead --
// "on PATH" alone is not proof of reachability, which is the whole point
// of --deep existing.
func TestRun_DoctorDeepReportsDegraded(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", isolatedPATHWithGopls(t))
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // never reach a real, already-running daemon at the managed default

	sockPath := filepath.Join(t.TempDir(), "gopls.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			_ = conn.Close() // accepts, then hangs up -- never speaks the handshake
		}
	}()
	t.Setenv("RGIT_LSP_SOCKET", sockPath)

	stdout, _, code := runApp(t, "doctor", "--porcelain", "--deep")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "(degraded"))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "(reachable)")))
}

// TestRun_DoctorDeepLeavesMissingServersUnchanged pins the skip guard: a
// server not on PATH at all is never dialed under --deep either -- there is
// nothing to dial, and MISSING already says everything --deep could add.
func TestRun_DoctorDeepLeavesMissingServersUnchanged(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	bin := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin) // git only -- no server binaries at all

	stdout, _, code := runApp(t, "doctor", "--porcelain", "--deep")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	for line := range strings.SplitSeq(strings.TrimRight(stdout, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if fields[0] != "server" {
			continue
		}
		qt.Assert(t, qt.Equals(fields[2], "MISSING"))
		qt.Assert(t, qt.Not(qt.StringContains(fields[3], "reachable")))
		qt.Assert(t, qt.Not(qt.StringContains(fields[3], "degraded")))
	}
}

// TestRun_DoctorMissingGitIsFatal pins the one check doctor treats as fatal:
// git is what rgit shells out to for everything, so its absence is the
// "genuinely cannot function" case docs/CODES.md's exit 128 covers -- unlike
// a missing language server or the tree-sitter CLI, both informational.
func TestRun_DoctorMissingGitIsFatal(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", t.TempDir()) // a PATH with nothing on it, git included

	_, stderr, code := runApp(t, "doctor")
	qt.Assert(t, qt.Equals(code, exitcode.GitFailure))
	qt.Assert(t, qt.StringContains(stderr, "git"))
}

// TestRun_DoctorHelpAndUsage mirrors TestRun_LanguagesHelpAndUsage for the
// same two non-listing paths.
func TestRun_DoctorHelpAndUsage(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, _, code := runApp(t, "doctor", "--help")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "usage: rgit doctor"))

	stdout, stderr, code := runApp(t, "doctor", "extra")
	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.Equals(stdout, ""))
	qt.Assert(t, qt.Not(qt.Equals(stderr, "")))
}

// TestRun_VersionReportsGrammars pins the first line staying
// byte-identical to what scripts and cmd/rgit-install already parse, and a
// second line reporting optional/gated grammars -- present or explicitly
// "none", so a caller can tell a SQL-enabled binary from a plain one
// without a separate `rgit languages` call. Which word that second line
// carries is build-specific (languages_sql_test.go, languages_nosql_test.go).
func TestRun_VersionReportsGrammars(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, _, code := runApp(t, "--version")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	lines := strings.SplitN(stdout, "\n", 2)
	qt.Assert(t, qt.Equals(lines[0], "rgit v0.0.0-test"))
	qt.Assert(t, qt.StringContains(stdout, "optional grammars:"))
}

// TestRun_UnsupportedLanguageGetsNoRebuildHint pins the negative case: a
// language this resolver has never supported (no grammar exists at all,
// gated or otherwise) gets the plain exit-9 refusal with no rebuild
// suggestion -- unlike a genuinely gated miss (languages_sql_test.go,
// languages_nosql_test.go), which does. True regardless of -tags rgit_sql,
// so it belongs in the untagged file rather than either build-specific one.
func TestRun_UnsupportedLanguageGetsNoRebuildHint(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "main.rs", "fn main() {}\n")

	_, stderr, code := runApp(t, "commit", "-m", "feat(x): y", "main.rs:main")

	qt.Assert(t, qt.Equals(code, exitcode.UnsupportedLanguage))
	qt.Assert(t, qt.StringContains(stderr, "no grammar registered for main.rs"))
	qt.Assert(t, qt.StringContains(stderr, `extension ".rs"`))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "rgit_sql")))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "rebuild")))
}

// TestRun_UnclassifiableArgReportsRulesConsidered pins the user-visible
// surface of cli.UnresolvedArgError: commit.go's mapStageError-adjacent
// path (its ClassifyArgs call) prints the error verbatim behind "rgit: "
// at exit 129, so a target rule 6 rejects reads as this and not a bare
// "invalid argument" -- and pins the "rules considered" wording 7de6deb
// gave UnresolvedArgError.Error() over the old "tried" framing.
func TestRun_UnclassifiableArgReportsRulesConsidered(t *testing.T) {
	chdirTempRepo(t)

	_, stderr, code := runApp(t, "commit", "-m", "feat(x): y", "nosuch.go:Nope")

	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.StringContains(stderr, `rgit: cannot classify "nosuch.go:Nope": rules considered:`))
}

// TestRun_HelpIsPlainText guards a defect that only shows up when something
// reads the output rather than a person skimming it: pflag renders a string
// flag's NoOptDefVal into the usage line as [="<value>"], so --gpg-sign's
// bare-vs-absent sentinel lands in `rgit commit --help`. A NUL-prefixed
// sentinel therefore printed a raw control byte, which made grep treat the
// help as a binary file and knocked the column alignment out for every flag
// below it.
func TestRun_HelpIsPlainText(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, args := range [][]string{{"--help"}, {"commit", "--help"}, {"diff", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, _, code := runApp(t, args...)
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			if i := strings.IndexFunc(stdout, func(r rune) bool {
				return r < 0x20 && r != '\n' && r != '\t'
			}); i >= 0 {
				t.Errorf("help contains control byte %q at offset %d", stdout[i], i)
			}
		})
	}
}
