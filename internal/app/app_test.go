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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
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
	}} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runApp(t, tc.args...)
			qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
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

	t.Run("every target unchanged is exit 11", func(t *testing.T) {
		writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
		_, stderr, code := runApp(t, "commit", "-m", "fix(a): x", "a.go:A")
		qt.Assert(t, qt.Equals(code, exitcode.NothingToCommit))
		qt.Assert(t, qt.StringContains(stderr, "has no uncommitted changes"))
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
// So -Ss means the key "s", not "sign plus signoff" -- verified against
// git, and the reason this cannot be a general shorthand-chain expansion.
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
func TestRun_PushFailureReportsUpstreamHint(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "a.go", "package a\n\n// A returns one.\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 2\n}\n")

	stdout, stderr, code := runApp(t, "commit", "--push", "-m", "fix(a): bump", "a.go:A")

	qt.Assert(t, qt.Equals(code, exitcode.PushFailed))
	// AGENTS.md: a push failure never rolls back the commit that preceded
	// it -- it already landed by the time Push is even attempted.
	qt.Assert(t, qt.StringContains(stdout, "a.go:A"))
	qt.Assert(t, qt.StringContains(gitOut(t, dir, "cat-file", "-p", "HEAD:a.go"), "return 111"))
	// The concrete, named fix commit.go's own doc comment promises: branch
	// "main" (gittest.New forces it) and the exact command to run.
	qt.Assert(t, qt.StringContains(stderr, "main has no upstream tracking branch"))
	qt.Assert(t, qt.StringContains(stderr, "git push -u origin main"))
}

// TestRun_PathspecListsEveryFileItStages pins the reported bug: naming a
// directory listed one aggregate row for the pathspec, so a caller could
// see that something under it moved but not what. `rgit diff` already broke
// the same change down per file, and the two are supposed to agree.
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
		// and on both subcommands -- three word lists, so three occurrences
		// of the pair in the order completion offers them.
		qt.Assert(t, qt.Equals(strings.Count(stdout, "-h --help"), 3))
		assertShellParses(t, "bash", stdout)
	})

	t.Run("zsh", func(t *testing.T) {
		stdout, stderr, code := runApp(t, "completion", "zsh")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.StringContains(stdout, "compdef _rgit rgit"))
		qt.Assert(t, qt.StringContains(stdout, "rgit diff --porcelain"))
		qt.Assert(t, qt.Equals(strings.Count(stdout, "-h --help"), 3))
		assertShellParses(t, "zsh", stdout)
	})

	t.Run("--help prints usage and exits 0", func(t *testing.T) {
		stdout, _, code := runApp(t, "completion", "--help")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(stdout, "usage: rgit completion"))
	})

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown shell", []string{"completion", "fish"}},
		{"missing shell", []string{"completion"}},
		{"too many args", []string{"completion", "bash", "zsh"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runApp(t, tc.args...)
			qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
			qt.Assert(t, qt.Equals(stdout, ""))
			qt.Assert(t, qt.Not(qt.Equals(stderr, "")))
		})
	}
}

// TestRun_Languages pins the always-present part of the listing -- the
// nine grammars every build carries regardless of -tags rgit_sql. Whether
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
