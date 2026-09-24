// End-to-end coverage for rgit, per CONTRIBUTING.md's three-file test
// budget: argument precedence, usage errors, diff rendering, and commit's
// real execution through git, hooks, and the invariants AGENTS.md pins.
//
// Every case execs the actual built binary against a real temporary git
// repository — no gitx mocking — so a regression in pflag's interspersed
// parsing, cli.ClassifyArgs's rule order, or internal/synth's staging
// shows up exactly as a user would see it.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsptest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// rgitBin is the built binary every end-to-end case execs. It is empty
// under -short, which is what requireBinary keys off to skip them: this
// file is the slow lane, and building a binary is the slowest thing the
// suite does. The unit tests beside it in this package carry the
// regression coverage and run either way.
var rgitBin string

func TestMain(m *testing.M) {
	// testing.Short() reads a flag, so the flags have to be parsed before
	// it can be consulted -- m.Run() would otherwise be the first thing to
	// do it, which is already too late to decide whether to build.
	flag.Parse()
	if testing.Short() {
		// Only the full lane's live-gopls case wants a managed daemon.
		os.Exit(lsptest.RunWithoutManagedDaemon(m))
	}

	bin, cleanup, err := buildRgit()
	if err != nil {
		fmt.Fprintln(os.Stderr, "build rgit for e2e tests:", err)
		os.Exit(1)
	}
	rgitBin = bin

	// buildRgit links coverage instrumentation (-cover) into rgitBin, and an
	// instrumented binary run with no GOCOVERDIR warns on its own stderr --
	// noise every case in this file would otherwise have to filter out of
	// its own assertions. Setting a real one here, once, for the whole test
	// process, means every exec.Command in this file inherits it through
	// os.Environ() with no per-call-site change, and the coverage data each
	// invocation writes is real (many processes writing into the same
	// directory is exactly what GOCOVERDIR is designed to accumulate).
	//
	// It lives beside the built binary rather than in its own temp root:
	// under `go test -coverpkg=./...` a child's coverage flush was seen
	// failing with "rename ... no such file or directory" because the
	// directory had gone while cases were still running. A path under the
	// build dir shares that directory's lifetime and its single cleanup,
	// so nothing else on the machine shares a temp root with it.
	coverDir := filepath.Join(filepath.Dir(bin), "cover")
	if err := os.MkdirAll(coverDir, 0o750); err != nil {
		fmt.Fprintln(os.Stderr, "create GOCOVERDIR for e2e tests:", err)
		cleanup()
		os.Exit(1)
	}
	if err := os.Setenv("GOCOVERDIR", coverDir); err != nil {
		fmt.Fprintln(os.Stderr, "set GOCOVERDIR for e2e tests:", err)
		cleanup()
		os.Exit(1)
	}

	code := m.Run()
	cleanup()
	os.Exit(code)
}

// requireBinary skips a case that drives the built binary when there is no
// binary to drive. Every route to one goes through here, including the few
// cases that exec rgitBin directly rather than through runRgit, so adding
// an end-to-end case cannot accidentally opt out of the gate.
func requireBinary(t *testing.T) {
	t.Helper()
	if rgitBin == "" {
		t.Skip("end-to-end binary cases skipped under -short")
	}
}

func buildRgit() (bin string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "rgit-e2e-bin-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	bin = filepath.Join(dir, "rgit")
	// Not -race: race-instrumenting the cgo tree-sitter parse path costs
	// 24x per invocation (1.05s against 0.044s), which across this file's
	// invocations is minutes rather than seconds -- and it buys almost
	// nothing, since a single rgit invocation resolves in sequence
	// (lsp.Session: "not safe for concurrent use"). The concurrency worth
	// checking is the jsonrpc2 read goroutine and the daemon spawn lock,
	// both in-process: `go test -race ./...` reaches them and this does not.
	cmd := exec.CommandContext(context.Background(), "go", "build", "-cover", "-o", bin, ".") //nolint:gosec // e2e builds this checkout with fixed arguments and a test-temp output
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", cleanup, fmt.Errorf("go build: %w: %s", err, stderr.String())
	}
	return bin, cleanup, nil
}

type rgitResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func runRgit(t *testing.T, repoDir string, args ...string) rgitResult {
	t.Helper()
	requireBinary(t)
	cmd := exec.CommandContext(t.Context(), rgitBin, args...) //nolint:gosec // e2e invokes the test-built binary directly with fixture CLI arguments
	cmd.Dir = repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("running rgit %v: %v", args, err)
		}
	}
	return rgitResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}
}

func TestCommit_DoubleDashForcesPathspec(t *testing.T) {
	t.Parallel()
	// Rule 1: everything after "--" is a pathspec, unconditionally — even
	// a token shaped like FILE:NAME for a file that does not exist. If
	// rule 5 got a chance at it instead, it would fail as an unresolvable
	// anchor (exit 3); forced as a pathspec, `git add` itself refuses it
	// (exit 128, "did not match any files") -- proof the whole string
	// reached git as one literal path, never split at its colon.
	// -m must come before "--", since pflag stops flag parsing there too.
	repo, _ := gittest.New(t.Context(), t)

	got := runRgit(t, repo, "commit", "-m", "chore: force pathspec", "--", "missing.go:NotASymbol")

	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.GitFailure)))
	qt.Assert(t, qt.StringContains(got.Stderr, "did not match any files"))
	qt.Assert(t, qt.Not(qt.StringContains(got.Stderr, "cannot classify")))
}

func TestContradictoryPathAndAnchor(t *testing.T) {
	t.Parallel()
	// This must be caught regardless of spelling -- flag or positional --
	// not merely on flag values: apply() runs `git add greet.go` and then
	// overwrites that same index entry with a blob synthesized from HEAD
	// plus one extent, silently dropping every other worktree change in
	// the file the caller asked for by path, while the listing still
	// reports the whole path's line counts.
	//
	// docs/CODES.md § Exit codes assigns 5 to naming a path both ways; the
	// spelling must not change the answer.
	setup := func(t *testing.T) string {
		t.Helper()
		repo, _ := gittest.New(t.Context(), t)
		gittest.Write(t, repo, "greet.go", "package main\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
		return repo
	}

	// "commit, both positional" is deliberately absent: it is the exact
	// shape app_test.go's TestRun_CommitExitCodes already pins at the unit
	// level ("one path named both ways is exit 5").
	contradictory := []struct {
		name string
		args []string
	}{
		{"both flags", []string{"commit", "-m", "chore: x", "--file", "greet.go", "--sym", "greet.go:A"}},
		{"positional path and --sym", []string{"commit", "-m", "chore: x", "greet.go", "--sym", "greet.go:A"}},
		{"diff, both positional", []string{"diff", "greet.go", "greet.go:A"}},
	}
	for _, tc := range contradictory {
		t.Run(tc.name, func(t *testing.T) {
			got := runRgit(t, setup(t), tc.args...)
			qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.ContradictoryAnchors)))
			qt.Assert(t, qt.StringContains(got.Stderr, "named both as a path and as a symbol anchor"))
		})
	}

	t.Run("different files are not a contradiction", func(t *testing.T) {
		// The rule is per path, not "a path and an anchor were both given".
		repo := setup(t)
		gittest.Write(t, repo, "other.go", "package main\n\nfunc C() int {\n\treturn 3\n}\n")

		got := runRgit(t, repo, "commit", "-m", "chore: mixed targets", "other.go", "greet.go:A")

		qt.Assert(t, qt.Equals(got.ExitCode, 0))
	})
}

func TestCommit_AnnouncesPreambleAndOrdinalAnchors(t *testing.T) {
	t.Parallel()
	// docs/ANCHORS.md documents both announcements: the new-file preamble
	// must be "announced on stderr", and an ordinal, a last resort, must
	// "warn and suggest qualification".
	t.Run("new-file preamble is announced", func(t *testing.T) {
		repo, _ := gittest.New(t.Context(), t)
		gittest.Write(t, repo, "new.go", "package main\n\nimport \"fmt\"\n\nfunc Hi() { fmt.Println(\"hi\") }\n")

		got := runRgit(t, repo, "commit", "-m", "feat(x): hi", "new.go:Hi")

		qt.Assert(t, qt.Equals(got.ExitCode, 0))
		qt.Assert(t, qt.StringContains(got.Stderr, "new.go is new"))
		qt.Assert(t, qt.StringContains(got.Stderr, "@header"))
	})

	t.Run("ordinal anchors warn", func(t *testing.T) {
		repo, _ := gittest.New(t.Context(), t)
		gittest.Write(t, repo, "dup.go", "package main\n\nfunc init() { println(1) }\n\nfunc init() { println(2) }\n")

		got := runRgit(t, repo, "commit", "-m", "feat(x): dup", "dup.go:init#2")

		qt.Assert(t, qt.Equals(got.ExitCode, 0))
		qt.Assert(t, qt.StringContains(got.Stderr, "dup.go:init#2"))
		qt.Assert(t, qt.StringContains(got.Stderr, "positional"))
	})

	t.Run("a uniquely named anchor does not warn", func(t *testing.T) {
		// The warning must key on the ordinal form, not fire on every anchor.
		repo, _ := gittest.New(t.Context(), t)
		gittest.Write(t, repo, "one.go", "package main\n\nfunc Only() {}\n")

		got := runRgit(t, repo, "commit", "-m", "feat(x): only", "one.go:Only")

		qt.Assert(t, qt.Equals(got.ExitCode, 0))
		qt.Assert(t, qt.Not(qt.StringContains(got.Stderr, "positional")))
	})
}

func TestDiff_CrossCheckReportsWithoutGating(t *testing.T) {
	t.Parallel()
	// The cross-check must run on the diff path too, not only commit's:
	// rgit diff must never emit an anchor rgit commit then refuses with
	// exit 6, or the closed loop holds only syntactically, not
	// semantically. Diff reports rather than gates: a disagreement is
	// worth knowing while reading the diff, but a read-only command must
	// not fail on one, and a server that is absent, slow or silent about a
	// symbol stays the normal case.
	repo, _ := gittest.New(t.Context(), t)
	gittest.Write(t, repo, "go.mod", "module x\n\ngo 1.21\n")
	gittest.Write(t, repo, "a.go", "package x\n\n// Doc for A.\nfunc A() int {\n\treturn 1\n}\n")
	gittest.Git(t.Context(), t, repo, "add", "-A")
	gittest.Git(t.Context(), t, repo, "-c", "user.email=t@t.t", "-c", "user.name=T", "commit", "-q", "-m", "init")
	gittest.Write(t, repo, "a.go", "package x\n\n// Doc for A.\nfunc A() int {\n\treturn 111\n}\n")

	got := runRgit(t, repo, "diff", "--porcelain")

	// Extents agree, so nothing is reported -- the false-positive guard that
	// matters most, since a warning on every symbol would be worse than none.
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.StringContains(got.Stdout, "a.go"))
	qt.Assert(t, qt.Not(qt.StringContains(got.Stderr, "[warning]")))
}

// TestDocumentedPathsWithoutOtherCoverage is specified in docs/USAGE.md; it
// is here because no other test -- e2e or unit -- exercises the path that
// reaches it: stripping the built binary's own PATH and XDG_RUNTIME_DIR is
// process-level enough (a real spawn attempt, a real socket probe against a
// directory with nothing in it) that it does not reduce to an app.Run unit
// case the way the rest of this file's former "documented paths" table did
// (diff --exit-code, diff --quiet, a non-conventional message, and a push
// failure all moved to app_test.go).
func TestDocumentedPathsWithoutOtherCoverage(t *testing.T) {
	t.Parallel()
	t.Run("no reachable language server degrades to ts-only", func(t *testing.T) {
		// Degraded resolution is normal, announced once, and never blocks.
		//
		// Both routes to a server have to be closed, or this passes or fails
		// on what the developer's machine happens to be running: stripping
		// PATH stops a spawn, and pointing XDG_RUNTIME_DIR at an empty
		// directory stops the socket probe finding a daemon some earlier
		// invocation left behind.
		requireBinary(t)
		repo, _ := gittest.New(t.Context(), t)
		gittest.Write(t, repo, "a.go", "package main\n\nfunc A() int { return 1 }\n")

		cmd := exec.CommandContext(context.Background(), rgitBin, "commit", "-m", "feat(x): a", "a.go:A")
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"PATH=/usr/bin:/bin",
			"XDG_RUNTIME_DIR="+t.TempDir(),
			"RGIT_LSP_SOCKET=")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()

		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.StringContains(stderr.String(), "[ts-only]"))
	})
}

// --- rgit diff execution ----------------------------------------------
//
// These cases build real temporary git repositories with real commits, per
// this file's own doc comment: the assertions below are about git's
// behaviour (scope selection, numstat's mode/binary conventions, untracked
// discovery), not about internal/diff's internals in isolation.

// initRepoWithFile creates a repo, writes relPath, and commits it as the
// base state every diff scope in this file's tests compares against.
func initRepoWithFile(t *testing.T, relPath, content string) string {
	t.Helper()
	dir, _ := gittest.RepoWithFile(t.Context(), t, relPath, content, "init")
	return dir
}

// porcelainRow is one parsed --porcelain record.
type porcelainRow struct {
	File, Symbol, Status, Added, Deleted string
}

func parsePorcelain(t *testing.T, output string) []porcelainRow {
	t.Helper()
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return nil
	}
	var rows []porcelainRow
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			t.Fatalf("malformed porcelain line %q: want 5 tab-separated fields, got %d", line, len(fields))
		}
		rows = append(rows, porcelainRow{File: fields[0], Symbol: fields[1], Status: fields[2], Added: fields[3], Deleted: fields[4]})
	}
	return rows
}

func findRow(rows []porcelainRow, file, status string) (porcelainRow, bool) {
	for _, r := range rows {
		if r.File == file && r.Status == status {
			return r, true
		}
	}
	return porcelainRow{}, false
}

const authGoV1 = `package auth

// ValidateToken checks a token.
func ValidateToken(tok string) bool {
	return tok != ""
}

// oldHelper is unused.
func oldHelper() int {
	return 1
}
`

const authGoV2 = `package auth

// ValidateToken checks a token and its length.
func ValidateToken(tok string) bool {
	return len(tok) >= 8
}
`

func TestDiff_DefaultScopePicksUpStagedUnstagedAndUntracked(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "auth.go", authGoV1)

	// Unstaged: modify the committed file.
	gittest.Write(t, repo, "auth.go", authGoV2)
	// Staged: a brand new file, added but not committed.
	gittest.Write(t, repo, "staged.go", "package auth\n\nfunc Staged() int { return 3 }\n")
	gittest.Git(t.Context(), t, repo, "add", "--", "staged.go")
	// Untracked: never added at all.
	gittest.Write(t, repo, "untracked.go", "package auth\n\nfunc Untracked() int { return 4 }\n")

	got := runRgit(t, repo, "diff", "--porcelain")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	rows := parsePorcelain(t, got.Stdout)

	if _, ok := findRow(rows, "auth.go", "MOD"); !ok {
		t.Errorf("default scope missed the unstaged change to auth.go: %+v", rows)
	}
	if _, ok := findRow(rows, "staged.go", "MOD"); !ok {
		t.Errorf("default scope missed the staged-only new file staged.go: %+v", rows)
	}
	// Untracked, like staged, attributes per symbol now -- there is no
	// aggregate UNTRACKED row for a supported language.
	if _, ok := findRow(rows, "untracked.go", "MOD"); !ok {
		t.Errorf("default scope missed the untracked file untracked.go: %+v", rows)
	}
}

func TestDiff_UnbornBranchListsEverythingCommittable(t *testing.T) {
	t.Parallel()
	// A fresh `git init` has no HEAD, so the default scope cannot run
	// `git diff HEAD` -- it compares against the empty tree instead.
	repo, _ := gittest.New(t.Context(), t)
	gittest.Write(t, repo, "staged.go", "package auth\n\nfunc Staged() int { return 3 }\n")
	gittest.Git(t.Context(), t, repo, "add", "--", "staged.go")
	gittest.Write(t, repo, "untracked.go", "package auth\n\nfunc Untracked() int { return 4 }\n")

	got := runRgit(t, repo, "diff", "--porcelain")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	rows := parsePorcelain(t, got.Stdout)

	if _, ok := findRow(rows, "staged.go", "MOD"); !ok {
		t.Errorf("unborn-branch diff missed the staged file: %+v", rows)
	}
	if _, ok := findRow(rows, "untracked.go", "MOD"); !ok {
		t.Errorf("unborn-branch diff missed the untracked file: %+v", rows)
	}
}

func TestCommit_PushAfterSuccessfulCommit(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "auth.go", authGoV1)
	remote := t.TempDir()
	gittest.Git(t.Context(), t, remote, "init", "-q", "--bare")
	gittest.Git(t.Context(), t, repo, "remote", "add", "origin", remote)
	branch := strings.TrimSpace(gittest.Git(t.Context(), t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	gittest.Git(t.Context(), t, repo, "push", "-q", "-u", "origin", branch)

	gittest.Write(t, repo, "auth.go", authGoV2)
	got := runRgit(t, repo, "commit", "--push", "-m", "fix(auth): reject expired", "auth.go:ValidateToken")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))

	local := strings.TrimSpace(gittest.Git(t.Context(), t, repo, "rev-parse", "HEAD"))
	qt.Assert(t, qt.Equals(strings.TrimSpace(gittest.Git(t.Context(), t, remote, "rev-parse", branch)), local))
}

// TestCommit_GoTSPythonSymbolGranularityInOneInvocation covers three
// grammars, not every one of them: the guarantee under test is that
// symbol granularity holds across DIFFERENT languages within a single
// invocation, not any one grammar's own resolution behaviour, which
// resolver_test.go and index_test.go already cover per language. Go, TS and
// Python are representative of that mixing (free functions, no shared
// container shape between them) without re-proving what those other files
// already do for every grammar rgit supports.
func TestCommit_GoTSPythonSymbolGranularityInOneInvocation(t *testing.T) {
	t.Parallel()
	// One commit naming a symbol in each of three languages. The point is
	// that each file's OTHER symbol changed too and must stay uncommitted:
	// symbol granularity has to hold per grammar, in a single invocation.
	repo, _ := gittest.New(t.Context(), t)
	gittest.Write(t, repo, "auth.go", "package auth\n\nfunc GoA() int { return 1 }\n\nfunc GoB() int { return 1 }\n")
	gittest.Write(t, repo, "app.ts", "export function TsA(): number { return 1 }\n\nexport function TsB(): number { return 1 }\n")
	gittest.Write(t, repo, "svc.py", "def py_a():\n    return 1\n\n\ndef py_b():\n    return 1\n")
	gittest.Git(t.Context(), t, repo, "add", "-A")
	gittest.Git(t.Context(), t, repo, "commit", "-q", "-m", "init")

	gittest.Write(t, repo, "auth.go", "package auth\n\nfunc GoA() int { return 2 }\n\nfunc GoB() int { return 2 }\n")
	gittest.Write(t, repo, "app.ts", "export function TsA(): number { return 2 }\n\nexport function TsB(): number { return 2 }\n")
	gittest.Write(t, repo, "svc.py", "def py_a():\n    return 2\n\n\ndef py_b():\n    return 2\n")

	got := runRgit(t, repo, "commit", "-m", "fix: bump the first of each",
		"auth.go:GoA", "app.ts:TsA", "svc.py:py_a")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))

	for _, c := range []struct{ path, committed, withheld string }{
		{"auth.go", "func GoA() int { return 2 }", "func GoB() int { return 1 }"},
		{"app.ts", "export function TsA(): number { return 2 }", "export function TsB(): number { return 1 }"},
		{"svc.py", "def py_a():\n    return 2", "def py_b():\n    return 1"},
	} {
		head := gittest.Git(t.Context(), t, repo, "show", "HEAD:"+c.path)
		qt.Assert(t, qt.StringContains(head, c.committed))
		qt.Assert(t, qt.StringContains(head, c.withheld))
	}
}

func TestCommit_ExtensionlessShebangResolvesShellSymbol(t *testing.T) {
	t.Parallel()
	// A git-hook-style script with no extension at all: resolve.ForPath's
	// shebang fallback is what makes it addressable, and staging one
	// function must leave its sibling uncommitted exactly like any other
	// symbol-granular commit.
	repo, _ := gittest.New(t.Context(), t)
	gittest.Write(t, repo, "pre-commit", "#!/usr/bin/env bash\n\nfoo() {\n  echo v1\n}\n\nbar() {\n  echo bar\n}\n")
	gittest.Git(t.Context(), t, repo, "add", "-A")
	gittest.Git(t.Context(), t, repo, "commit", "-q", "-m", "init")

	gittest.Write(t, repo, "pre-commit", "#!/usr/bin/env bash\n\nfoo() {\n  echo v2\n}\n\nbar() {\n  echo changed too\n}\n")

	got := runRgit(t, repo, "commit", "-m", "fix: bump foo only", "pre-commit:foo")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))

	head := gittest.Git(t.Context(), t, repo, "show", "HEAD:pre-commit")
	qt.Assert(t, qt.StringContains(head, "echo v2"))
	qt.Assert(t, qt.StringContains(head, "echo bar")) // bar's edit stayed uncommitted

	// A zsh shebang is deliberately not routed to the shell grammar
	// (tree-sitter-bash mis-parses zsh-only syntax), so an extensionless
	// zsh script still refuses a symbol anchor -- the same exit 9 an
	// unrecognized extension already gets, not a new failure mode.
	gittest.Write(t, repo, "zsh-script", "#!/bin/zsh\n\nfoo() {\n  echo hi\n}\n")
	gittest.Git(t.Context(), t, repo, "add", "-A")
	gittest.Git(t.Context(), t, repo, "commit", "-q", "-m", "add zsh script")
	got = runRgit(t, repo, "commit", "-m", "chore: touch", "zsh-script:foo")
	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.UnsupportedLanguage)))
}

func TestCommit_FromSubdirectoryResolvesCWDRelativePaths(t *testing.T) {
	t.Parallel()
	// git resolves a pathspec relative to the current directory: `git add
	// a.go` in pkg/deep stages pkg/deep/a.go. Output stays root-relative,
	// as git's own --numstat does.
	repo, _ := gittest.New(t.Context(), t)
	gittest.Write(t, repo, "pkg/deep/a.go", "package deep\n\nfunc Alpha() int { return 1 }\n")
	gittest.Git(t.Context(), t, repo, "add", "-A")
	gittest.Git(t.Context(), t, repo, "commit", "-q", "-m", "init")
	gittest.Write(t, repo, "pkg/deep/a.go", "package deep\n\nfunc Alpha() int { return 42 }\n")

	sub := filepath.Join(repo, "pkg", "deep")

	got := runRgit(t, sub, "diff", "--porcelain", "a.go")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.StringContains(got.Stdout, "pkg/deep/a.go"))

	got = runRgit(t, sub, "commit", "-m", "fix: bump", "a.go:Alpha")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.StringContains(gittest.Git(t.Context(), t, repo, "show", "--stat", "--format=", "HEAD"), "pkg/deep/a.go"))
}

func TestDiff_RevisionRangeScopes(t *testing.T) {
	t.Parallel()
	// Precedence rule 3's three reachable shapes: a bare revision against
	// the worktree, a two-dot range, and a three-dot range (whose old side
	// is the merge base, not the left endpoint).
	repo := initRepoWithFile(t, "auth.go", authGoV1)
	gittest.Write(t, repo, "auth.go", authGoV2)
	gittest.Git(t.Context(), t, repo, "commit", "-q", "-a", "-m", "fix: v2")

	for _, rev := range []string{"HEAD~1", "HEAD~1..HEAD", "HEAD~1...HEAD"} {
		got := runRgit(t, repo, "diff", "--porcelain", rev)
		qt.Assert(t, qt.Equals(got.ExitCode, 0))
		if !strings.Contains(got.Stdout, "auth.go") {
			t.Errorf("scope %q reported no change to auth.go: %q", rev, got.Stdout)
		}
	}
}

func TestDiff_UnstagedScopeExcludesStaged(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "auth.go", authGoV1)

	gittest.Write(t, repo, "auth.go", authGoV2) // unstaged change
	gittest.Write(t, repo, "staged.go", "package auth\n\nfunc Staged() int { return 3 }\n")
	gittest.Git(t.Context(), t, repo, "add", "--", "staged.go") // staged-only change

	def := parsePorcelain(t, runRgit(t, repo, "diff", "--porcelain").Stdout)
	if _, ok := findRow(def, "staged.go", "MOD"); !ok {
		t.Fatalf("default scope should include the staged file: %+v", def)
	}

	unstaged := parsePorcelain(t, runRgit(t, repo, "diff", "--unstaged", "--porcelain").Stdout)
	if _, ok := findRow(unstaged, "auth.go", "MOD"); !ok {
		t.Errorf("--unstaged should still show auth.go's unstaged change: %+v", unstaged)
	}
	if _, ok := findRow(unstaged, "staged.go", "MOD"); ok {
		t.Errorf("--unstaged must not show staged.go, which has no unstaged change: %+v", unstaged)
	}
}

// TestDiff_AnchorRoundTrip is the closed-loop guarantee AGENTS.md and
// docs/USAGE.md make load-bearing: every FILE:SYMBOL label rgit diff prints
// must be exactly the string internal/resolve accepts back. A MOD row's
// symbol must resolve against the worktree; a DELETED row's only ever
// existed in HEAD, so it must resolve there instead.
func TestDiff_AnchorRoundTrip(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "auth.go", authGoV1)
	gittest.Write(t, repo, "auth.go", authGoV2) // modifies ValidateToken, deletes oldHelper

	got := runRgit(t, repo, "diff", "--porcelain")
	rows := parsePorcelain(t, got.Stdout)

	lang, ok := resolve.ForExtension(".go")
	if !ok {
		t.Fatal("no .go grammar registered")
	}

	checked := 0
	for _, r := range rows {
		if r.Symbol == "" {
			continue
		}
		var src []byte
		if r.Status == "DELETED" {
			src = []byte(authGoV1)
		} else {
			var err error
			src, err = os.ReadFile(filepath.Join(repo, r.File)) //nolint:gosec // rgit reports fixture paths from the test temp repository
			if err != nil {
				t.Fatalf("reading worktree %s: %v", r.File, err)
			}
		}
		if _, err := resolve.Resolve(lang, src, r.Symbol); err != nil {
			t.Errorf("emitted anchor %s:%s (status %s) does not resolve back: %v", r.File, r.Symbol, r.Status, err)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no symbol rows to round-trip -- test fixture stopped exercising the thing it claims to test")
	}
}

// TestDiff_ModeRowOnChmod is thinned to the human-readable text rendering:
// the porcelain half -- a mode-only change surfacing as MODE at all -- is
// already pinned at the unit level
// (internal/app/lanes_test.go's TestRun_DiffUntrackedFileAndModeChange),
// which covered it via both the default and --staged scopes. What only
// this e2e case still proves is that the *human* output names both the
// old and new mode.
func TestDiff_ModeRowOnChmod(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "script.sh", "#!/bin/sh\necho hi\n")

	if err := os.Chmod(filepath.Join(repo, "script.sh"), 0o755); err != nil { //nolint:gosec // executable mode is the behavior under test
		t.Fatal(err)
	}

	text := runRgit(t, repo, "diff").Stdout
	qt.Assert(t, qt.StringContains(text, "644"))
	qt.Assert(t, qt.StringContains(text, "755"))
}

func TestDiff_BinaryRowUsesDashCounts(t *testing.T) {
	t.Parallel()
	binary := []byte("PNGFAKE\x00\x01binary")
	repo, _ := gittest.New(t.Context(), t)
	if err := os.WriteFile(filepath.Join(repo, "logo.bin"), binary, 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t.Context(), t, repo, "add", "--", "logo.bin")
	gittest.Git(t.Context(), t, repo, "commit", "-q", "-m", "add binary")

	changed := append(append([]byte(nil), binary...), 'X')
	if err := os.WriteFile(filepath.Join(repo, "logo.bin"), changed, 0o600); err != nil {
		t.Fatal(err)
	}

	rows := parsePorcelain(t, runRgit(t, repo, "diff", "--porcelain").Stdout)
	row, ok := findRow(rows, "logo.bin", "BINARY")
	if !ok {
		t.Fatalf("modified binary file must surface as BINARY: %+v", rows)
	}
	qt.Assert(t, qt.Equals(row.Added, "-"))
	qt.Assert(t, qt.Equals(row.Deleted, "-"))
}

// TestDiff_UnanchorableHunk covers docs/ANCHORS.md's own example: a
// free-floating comment separated from every declaration by a blank line on
// both sides belongs to no symbol. Changing only that comment must surface
// as (unanchorable) rather than being attributed to a neighbouring
// function, and — the sum-of-hunks invariant — neither neighbouring
// function may show any change at all.
func TestDiff_UnanchorableHunk(t *testing.T) {
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
	repo := initRepoWithFile(t, "notes.go", before)
	gittest.Write(t, repo, "notes.go", after)

	rows := parsePorcelain(t, runRgit(t, repo, "diff", "--porcelain").Stdout)

	row, ok := findRow(rows, "notes.go", "UNANCHORABLE")
	if !ok {
		t.Fatalf("comment-only change between two functions must surface as UNANCHORABLE: %+v", rows)
	}
	qt.Assert(t, qt.Equals(row.Added, "1"))
	qt.Assert(t, qt.Equals(row.Deleted, "1"))

	for _, r := range rows {
		if r.File == "notes.go" && r.Symbol != "" {
			t.Errorf("neither A nor B changed; the comment edit must not be attributed to a symbol: %+v", r)
		}
	}
}

// --- rgit commit execution ---------------------------------------------

const commitHappyV1 = `package auth

func A() int {
	return 1
}

func B() int {
	return 2
}
`

const commitHappyV2 = `package auth

func A() int {
	return 100
}

func B() int {
	return 200
}
`

// TestCommit_HappyPath is CONTRIBUTING.md's pinned rgit_e2e_test.go happy
// path: init repo -> edit symbol -> rgit diff -> rgit commit -> verify HEAD,
// clean index, hook ran, worktree preserved. Both A and B change in the
// worktree; only A is named. The load-bearing assertion is HEAD carrying
// A's change and NOT B's -- a whole-file commit would also move HEAD, so
// checking that alone would not prove symbol granularity.
func TestCommit_HappyPath(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "auth.go", commitHappyV1)
	marker := filepath.Join(repo, "hook-ran")
	gittest.InstallHook(t.Context(), t, repo, "pre-commit", "#!/bin/sh\ntouch \""+marker+"\"\n")

	gittest.Write(t, repo, "auth.go", commitHappyV2)

	diffGot := runRgit(t, repo, "diff", "--porcelain")
	qt.Assert(t, qt.Equals(diffGot.ExitCode, 0))
	if _, ok := findRow(parsePorcelain(t, diffGot.Stdout), "auth.go", "MOD"); !ok {
		t.Fatalf("rgit diff must show auth.go as modified before commit: %q", diffGot.Stdout)
	}

	got := runRgit(t, repo, "commit", "auth.go:A", "-m", "feat(auth): give A a real value")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))

	head := gittest.Git(t.Context(), t, repo, "show", "HEAD:auth.go")
	qt.Assert(t, qt.StringContains(head, "return 100"))
	qt.Assert(t, qt.Not(qt.StringContains(head, "return 200")))

	// Clean index: nothing left staged after the commit.
	qt.Assert(t, qt.Equals(gittest.Git(t.Context(), t, repo, "diff", "--staged", "--numstat"), ""))

	// B's own edit is still outstanding, unstaged -- staging never touched it.
	qt.Assert(t, qt.StringContains(gittest.Git(t.Context(), t, repo, "diff", "--numstat"), "auth.go"))

	// The worktree file itself is never touched by staging.
	onDisk, err := os.ReadFile(filepath.Join(repo, "auth.go")) //nolint:gosec // path is inside the test temp directory
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(string(onDisk), commitHappyV2))

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("pre-commit hook did not run: %v", err)
	}
}

func TestCommit_HookRejectionRestoresStaging(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "auth.go", commitHappyV1)
	gittest.Write(t, repo, "auth.go", commitHappyV2)
	gittest.InstallHook(t.Context(), t, repo, "pre-commit", "#!/bin/sh\nexit 1\n")

	got := runRgit(t, repo, "commit", "auth.go:A", "-m", "feat(auth): update A")
	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.GitFailure)))

	// Rolled back: the index reads HEAD again, A's synthesized edit unstaged.
	indexed := gittest.Git(t.Context(), t, repo, "show", ":auth.go")
	qt.Assert(t, qt.Not(qt.StringContains(indexed, "return 100")))
	qt.Assert(t, qt.StringContains(indexed, "return 1"))
	// Index matches HEAD while the worktree still differs from it: git's
	// porcelain reports " M", unstaged only.
	status := gittest.Git(t.Context(), t, repo, "status", "--porcelain")
	qt.Assert(t, qt.Equals(status, " M auth.go\n"))

	// The worktree file itself is never touched by the rollback.
	onDisk, err := os.ReadFile(filepath.Join(repo, "auth.go")) //nolint:gosec // path is inside the test temp directory
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(string(onDisk), commitHappyV2))
}

func TestCommit_PositionalPathspecParityWithFileFlag(t *testing.T) {
	t.Parallel()
	repoPositional, _ := gittest.New(t.Context(), t)
	gittest.Write(t, repoPositional, "notes.txt", "hello\n")
	gotPositional := runRgit(t, repoPositional, "commit", "notes.txt", "-m", "chore: add notes")
	qt.Assert(t, qt.Equals(gotPositional.ExitCode, 0))

	repoFlag, _ := gittest.New(t.Context(), t)
	gittest.Write(t, repoFlag, "notes.txt", "hello\n")
	gotFlag := runRgit(t, repoFlag, "commit", "--file", "notes.txt", "-m", "chore: add notes")
	qt.Assert(t, qt.Equals(gotFlag.ExitCode, 0))

	qt.Assert(t, qt.Equals(gittest.Git(t.Context(), t, repoPositional, "show", "HEAD:notes.txt"), gittest.Git(t.Context(), t, repoFlag, "show", "HEAD:notes.txt")))
}

func TestCommit_ReportsWhatItCommitted(t *testing.T) {
	t.Parallel()
	// A commit that prints nothing forces the caller to run `git show` or
	// `git status` afterwards just to learn what landed -- which is the
	// context cost rgit exists to remove. git's own summary carries the
	// branch, the new SHA, and the changed/insertion/deletion counts, so
	// relaying it verbatim is both the cheapest fix and the one that
	// matches git (AGENTS.md's governing principle).
	repo := initRepoWithFile(t, "auth.go", commitHappyV1)
	gittest.Write(t, repo, "auth.go", commitHappyV2)

	got := runRgit(t, repo, "commit", "auth.go:A", "-m", "feat(auth): give A a real value")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.StringContains(got.Stdout, "feat(auth): give A a real value"))
	qt.Assert(t, qt.StringContains(got.Stdout, "1 file changed"))
	qt.Assert(t, qt.StringContains(got.Stdout, "insertion"))

	// And the part git cannot report: which symbol went in, and by how
	// much -- the same listing --dry-run prints, so the two are comparable.
	qt.Assert(t, qt.StringContains(got.Stdout, "auth.go:A"))
	qt.Assert(t, qt.Not(qt.StringContains(got.Stdout, "auth.go:B")))
}

// TestCommit_DryRunPreviewsAndStagesNothing is thinned to what only a real,
// separately exec'd binary can show: internal/app/app_test.go's
// TestRun_CommitDryRunAndPorcelain already pins --dry-run's record shape
// and non-writing in-process (a whole-path preview's own counts are
// likewise already covered there, via a --porcelain path-target case). Kept
// here is the one cross-process invariant no in-process call can prove the
// same way -- that the preview's own reported magnitude agrees with what a
// second, independently invoked `rgit diff --porcelain` reports for the
// identical change.
func TestCommit_DryRunPreviewsAndStagesNothing(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "auth.go", commitHappyV1)
	gittest.Write(t, repo, "auth.go", commitHappyV2)

	got := runRgit(t, repo, "commit", "--dry-run", "auth.go:A", "-m", "feat(auth): preview only")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))

	diffGot := runRgit(t, repo, "diff", "--porcelain")
	row, ok := findRow(parsePorcelain(t, diffGot.Stdout), "auth.go", "MOD")
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.StringContains(got.Stdout, "+"+row.Added+"/-"+row.Deleted))
}

func TestCommit_PathAlreadyStagedAsDeleted(t *testing.T) {
	t.Parallel()
	// After `git rm`, the path matches nothing in the worktree and nothing
	// in the index, so `git add` rejects it as a bad pathspec. Naming
	// something already staged exactly as asked is not an error -- the
	// commit includes it either way -- or `rgit commit <path>` would be
	// unusable after a `git rm`.
	repo := initRepoWithFile(t, "auth.go", commitHappyV1)
	gittest.Write(t, repo, "gone.md", "bye\n")
	gittest.Git(t.Context(), t, repo, "add", "gone.md")
	gittest.Git(t.Context(), t, repo, "commit", "-q", "-m", "chore: add gone.md")
	gittest.Git(t.Context(), t, repo, "rm", "-q", "gone.md")

	got := runRgit(t, repo, "commit", "gone.md", "-m", "chore: drop gone.md")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.StringContains(got.Stdout, "1 deletion"))
	qt.Assert(t, qt.Not(qt.StringContains(gittest.Git(t.Context(), t, repo, "ls-files"), "gone.md")))
}

// TestCommit_OtherPathsSurviveAnAlreadyStagedDeletion is
// TestCommit_PathAlreadyStagedAsDeleted's sibling: the same tolerated
// pathspec named *alongside* other, genuinely dirty paths in one commit.
// `git add` fails its whole invocation on the one pathspec that matches
// nothing, so tolerating it must not cost the other named paths their
// staging -- the commit and the summary reporting it must agree with what
// actually landed.
func TestCommit_OtherPathsSurviveAnAlreadyStagedDeletion(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "keep.txt", "a\n")
	gittest.Write(t, repo, "doomed.txt", "b\n")
	gittest.Write(t, repo, "other.txt", "c\n")
	gittest.Git(t.Context(), t, repo, "add", "doomed.txt", "other.txt")
	gittest.Git(t.Context(), t, repo, "commit", "-q", "-m", "chore: add doomed.txt, other.txt")
	gittest.Git(t.Context(), t, repo, "rm", "-q", "doomed.txt")
	gittest.Write(t, repo, "keep.txt", "a-modified\n")
	gittest.Write(t, repo, "other.txt", "c-modified\n")

	got := runRgit(t, repo, "commit", "-m", "test", "doomed.txt", "keep.txt", "other.txt")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))

	stat := gittest.Git(t.Context(), t, repo, "show", "--stat", "--oneline", "HEAD")
	qt.Assert(t, qt.StringContains(stat, "keep.txt"))
	qt.Assert(t, qt.StringContains(stat, "other.txt"))
	qt.Assert(t, qt.StringContains(stat, "doomed.txt"))
	qt.Assert(t, qt.Equals(gittest.Git(t.Context(), t, repo, "status", "--porcelain"), ""))
}

func TestOutput_OrderedByPathThenPosition(t *testing.T) {
	t.Parallel()
	// Both listings sort alphabetically by path, then ascending by position
	// within each file -- the same contract `git status` offers. Discovery
	// order would put @imports last despite it being the first thing in
	// the file, and echoing whatever order the caller happened to type
	// would be neither greppable nor stable between runs on an unchanged
	// tree.
	src := "package p\n\nimport \"fmt\"\n\nfunc Zebra() int { return 1 }\n\nfunc Apple() int { return 2 }\n\nfunc Mango() int { return 3 }\n"
	repo := initRepoWithFile(t, "b.go", src)
	gittest.Write(t, repo, "a.go", src)
	gittest.Write(t, repo, "zsub/c.go", src)
	gittest.Git(t.Context(), t, repo, "add", "a.go", "zsub/c.go")
	gittest.Git(t.Context(), t, repo, "commit", "-q", "-m", "chore: siblings")

	edited := strings.NewReplacer(
		"return 1", "return 11",
		"return 2", "return 22",
		"return 3", "return 33",
	).Replace(src)
	for _, p := range []string{"a.go", "b.go", "zsub/c.go"} {
		gittest.Write(t, repo, p, edited)
	}

	got := runRgit(t, repo, "diff", "--porcelain")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))

	var pairs []string
	for _, r := range parsePorcelain(t, got.Stdout) {
		pairs = append(pairs, r.File+":"+r.Symbol)
	}
	// Files alphabetical; within each, source order (Zebra at line 5 before
	// Apple at 7 before Mango at 9) -- deliberately not alphabetical by
	// symbol, which would reorder the file's own structure.
	qt.Assert(t, qt.DeepEquals(pairs, []string{
		"a.go:Zebra", "a.go:Apple", "a.go:Mango",
		"b.go:Zebra", "b.go:Apple", "b.go:Mango",
		"zsub/c.go:Zebra", "zsub/c.go:Apple", "zsub/c.go:Mango",
	}))

	// Identical between runs on an unchanged tree.
	again := runRgit(t, repo, "diff", "--porcelain")
	qt.Assert(t, qt.Equals(again.Stdout, got.Stdout))

	// The same order regardless of the order targets were named.
	dry := runRgit(t, repo, "commit", "--dry-run", "-m", "fix(p): scrambled",
		"zsub/c.go:Mango", "a.go:Zebra", "b.go:Apple", "a.go:Apple")
	qt.Assert(t, qt.Equals(dry.ExitCode, 0))
	iZebra := strings.Index(dry.Stdout, "a.go:Zebra")
	iApple := strings.Index(dry.Stdout, "a.go:Apple")
	iB := strings.Index(dry.Stdout, "b.go:Apple")
	iC := strings.Index(dry.Stdout, "zsub/c.go:Mango")
	qt.Assert(t, qt.IsTrue(iZebra >= 0 && iZebra < iApple && iApple < iB && iB < iC))
}

// --- help --------------------------------------------------------------

func TestHelp_TopLevelExitsZeroOnEverySpelling(t *testing.T) {
	t.Parallel()
	// All three spellings -- bare "--help", "-h", and "help" -- must print
	// the same top-level help to stdout and exit 0, not fall into the
	// unknown-command branch (exit 129).
	repo, _ := gittest.New(t.Context(), t)
	for _, spelling := range []string{"--help", "-h", "help"} {
		t.Run(spelling, func(t *testing.T) {
			got := runRgit(t, repo, spelling)
			qt.Assert(t, qt.Equals(got.ExitCode, 0))
			// TestMain sets GOCOVERDIR for the whole process, so the
			// -cover-instrumented binary (buildRgit) never has its own
			// coverage warning to filter out here: stderr is exactly what
			// this command itself writes, which for a help spelling is
			// nothing.
			qt.Assert(t, qt.Equals(got.Stderr, ""))
			qt.Assert(t, qt.StringContains(got.Stdout, "diff"))
			qt.Assert(t, qt.StringContains(got.Stdout, "commit"))
			qt.Assert(t, qt.StringContains(got.Stdout, "--version"))
		})
	}
}

func TestHelp_BareInvocationStillExitsInvalidUsage(t *testing.T) {
	t.Parallel()
	// Bare `git` prints its own full help to stdout at exit 1 -- but rgit's
	// eight subcommands (diff, commit, blame, log, context, languages,
	// doctor, completion) include no useful no-op mode, and every other
	// usage error in its table (missing message, no target, ...) is already
	// pinned to exit 129. Naming no command is the same kind of usage
	// error, so it keeps rgit's own convention rather than adopting git's
	// top-level dispatcher quirk.
	repo, _ := gittest.New(t.Context(), t)
	got := runRgit(t, repo)
	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.InvalidUsage)))
	qt.Assert(t, qt.Equals(got.Stdout, ""))
	qt.Assert(t, qt.StringContains(got.Stderr, "usage: rgit"))
}

func TestHelp_SubcommandExitsZeroAndDoesNotLeakPflag(t *testing.T) {
	t.Parallel()
	// -h/--help must route to the subcommand's own help on stdout at exit
	// 0: pflag's ContinueOnError returns pflag.ErrHelp from Parse, and that
	// must be caught via errors.Is(err, pflag.ErrHelp) rather than falling
	// into the generic parse-failure branch, which would print pflag's own
	// internal error string ("rgit: pflag: help requested") to stderr at
	// exit 129.
	//
	// Both subcommands are covered here rather than in two near-identical
	// tests: each subcommand wires its own help text separately, so
	// nothing structural stops one from missing this wiring while the
	// other has it.
	repo, _ := gittest.New(t.Context(), t)
	// wantFlag is a flag unique to that subcommand, proving the help came
	// from its own FlagSet rather than the other's.
	for sub, wantFlag := range map[string]string{"commit": "--amend", "diff": "--porcelain"} {
		for _, spelling := range []string{"--help", "-h"} {
			t.Run(sub+" "+spelling, func(t *testing.T) {
				got := runRgit(t, repo, sub, spelling)
				qt.Assert(t, qt.Equals(got.ExitCode, 0))
				qt.Assert(t, qt.Not(qt.StringContains(got.Stdout, "pflag")))
				qt.Assert(t, qt.Not(qt.StringContains(got.Stderr, "pflag")))
				qt.Assert(t, qt.StringContains(got.Stdout, wantFlag))
				qt.Assert(t, qt.StringContains(got.Stdout, "FILE:NAME"))
			})
		}
	}
}

// --- commit --amend ------------------------------------------------------

// --- Forwarded git flags: --fixup/--squash, --author/--date/--reset-author,
// --gpg-sign/--no-gpg-sign, the --porcelain and -q output modes, and a clearer
// --push-with-no-upstream message. Each is specified in docs/USAGE.md § Flags;
// what earns a test here is a flag rgit does more with than hand to git.
// --amend, --fixup/--squash's own autosquash-message generation, plain
// --author/--date forwarding, and --reset-author are all pinned at the unit
// level instead (internal/app/lanes_test.go), since app.Run shells out to
// the same real git these e2e cases would.

func TestCommit_FixupWithMessageAppendsRatherThanConflicts(t *testing.T) {
	t.Parallel()
	// --fixup plus -m is not the "-m and -F are mutually exclusive" shape
	// of conflict: git appends -m's text as an extra body paragraph below
	// the generated "fixup! ..." subject.
	repo := initRepoWithFile(t, "g.go", "package main\n\nfunc G() int { return 1 }\n")
	target := strings.TrimSpace(gittest.Git(t.Context(), t, repo, "rev-parse", "HEAD"))

	gittest.Write(t, repo, "g.go", "package main\n\nfunc G() int { return 2 }\n")
	got := runRgit(t, repo, "commit", "--fixup="+target, "-m", "UNIQUE_BODY_MARKER", "g.go")

	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	body := gittest.Git(t.Context(), t, repo, "log", "-1", "--format=%B")
	qt.Assert(t, qt.StringContains(body, "fixup! init"))
	qt.Assert(t, qt.StringContains(body, "UNIQUE_BODY_MARKER"))
}

func TestCommit_GPGSignFlagsForwarded(t *testing.T) {
	t.Parallel()
	// gpg.program pointed at a binary that always fails turns any signing
	// attempt into a deterministic, fast failure -- proof --gpg-sign (bare
	// or with a key id) reached git and triggered signing, with no real
	// GPG setup needed. --no-gpg-sign is checked the other way: it must
	// override commit.gpgsign=true and still succeed.
	repo, _ := gittest.New(t.Context(), t)
	gittest.Git(t.Context(), t, repo, "config", "gpg.program", "/bin/false")

	gittest.Write(t, repo, "a.go", "package main\n\nfunc A() {}\n")
	unsigned := runRgit(t, repo, "commit", "-m", "feat(a): add A", "a.go")
	qt.Assert(t, qt.Equals(unsigned.ExitCode, 0))

	gittest.Write(t, repo, "b.go", "package main\n\nfunc B() {}\n")
	bare := runRgit(t, repo, "commit", "--gpg-sign", "-m", "feat(b): add B", "b.go")
	qt.Assert(t, qt.Equals(bare.ExitCode, int(exitcode.GitFailure)))
	qt.Assert(t, qt.StringContains(bare.Stderr, "sign"))

	keyed := runRgit(t, repo, "commit", "--gpg-sign=DEADBEEF", "-m", "feat(c): add C", "a.go")
	qt.Assert(t, qt.Equals(keyed.ExitCode, int(exitcode.GitFailure)))

	gittest.Git(t.Context(), t, repo, "config", "commit.gpgsign", "true")
	gittest.Write(t, repo, "d.go", "package main\n\nfunc D() {}\n")
	noSign := runRgit(t, repo, "commit", "--no-gpg-sign", "-m", "feat(d): add D", "d.go")
	qt.Assert(t, qt.Equals(noSign.ExitCode, 0))
}

func TestCommit_PushWithNoUpstreamNamesTheFix(t *testing.T) {
	t.Parallel()
	// docs/USAGE.md / AGENTS.md's one invariant: rgit does not invent an
	// implicit `-u` (a push.default=current caller already gets a
	// successful push with no upstream at all, and pre-empting on that
	// basis would silently break them). What it adds on top of git's own
	// failure is a named, concrete fix.
	//
	// Also pinned at the unit level
	// (internal/app/app_test.go's TestRun_PushFailureReportsUpstreamHint),
	// deliberately -- see that test's own comment for why this is not
	// plain duplication: it triggers push failure via no remote configured
	// at all, this one via a real remote with no upstream tracking, git's
	// own distinct "no upstream branch" refusal, which only a real remote
	// can produce.
	repo := initRepoWithFile(t, "auth.go", authGoV1)
	remote := t.TempDir()
	gittest.Git(t.Context(), t, remote, "init", "-q", "--bare")
	gittest.Git(t.Context(), t, repo, "remote", "add", "origin", remote)
	branch := strings.TrimSpace(gittest.Git(t.Context(), t, repo, "rev-parse", "--abbrev-ref", "HEAD"))

	gittest.Write(t, repo, "auth.go", authGoV2)
	got := runRgit(t, repo, "commit", "--push", "-m", "fix(auth): reject expired", "auth.go:ValidateToken")

	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.PushFailed)))
	qt.Assert(t, qt.StringContains(got.Stderr, "git push -u origin "+branch))
	// The commit itself still landed even though the push failed.
	qt.Assert(t, qt.StringContains(gittest.Git(t.Context(), t, repo, "cat-file", "-p", "HEAD:auth.go"), "len(tok)"))
}

// TestCommit_PorcelainEmitsRecords is thinned the same way: --dry-run
// --porcelain's own record shape (a pathspec target's empty SYMBOL column
// included, per TestRun_PathspecMatchingNothingStillListsItself,
// internal/app/app_test.go) is already pinned in-process. What only two
// separately exec'd binary invocations can show is kept: a real commit's
// target rows are byte-identical to what its dry-run preview showed, the
// real commit additionally leads with an `H<TAB>SHA` record the dry run
// never invents (docs/CODES.md#rgit-commit---porcelain), and the real
// commit's own --porcelain output never lets git's human summary leak into
// it (docs/CODES.md's "no header, no summary" record contract).
func TestCommit_PorcelainEmitsRecords(t *testing.T) {
	t.Parallel()
	repo, _ := gittest.New(t.Context(), t)
	gittest.Write(t, repo, "g.go", "package main\n\nfunc G() int { return 1 }\n")
	gittest.Write(t, repo, "notes.txt", "hello\n")

	dry := runRgit(t, repo, "commit", "--dry-run", "--porcelain",
		"-m", "feat(g): add G", "g.go:G", "notes.txt")
	qt.Assert(t, qt.Equals(dry.ExitCode, 0))
	qt.Assert(t, qt.StringContains(dry.Stdout, "g.go\tG\t"))

	real := runRgit(t, repo, "commit", "--porcelain",
		"-m", "feat(g): add G", "g.go:G", "notes.txt")
	qt.Assert(t, qt.Equals(real.ExitCode, 0))

	sha := strings.TrimSpace(gittest.Git(t.Context(), t, repo, "rev-parse", "HEAD"))
	qt.Assert(t, qt.Equals(real.Stdout, "H\t"+sha+"\n"+dry.Stdout))
	qt.Assert(t, qt.Equals(strings.Contains(real.Stdout, "file changed"), false))
}

// --- shell completion ---------------------------------------------------
//
// internal/app/completion_test.go-equivalent coverage (in app_test.go)
// proves the emitted script parses and contains the right pieces; it
// cannot prove the dynamic half actually works, because that half is shell
// text with no Go behind it once emitted. This is the one thing only a real
// shell process running the real binary can show: that "auth.go:" really
// does complete to the live symbol names `rgit symbols` reports for that
// file -- a gap CONTRIBUTING.md says to measure rather than assume.

// runBashCompletion sources bashScript, then simulates typing
// "rgit <words...>" with the cursor on the final word and prints one
// candidate per line -- the same shape `complete`'s COMPREPLY protocol
// uses, without needing an interactive terminal to drive it.
func runBashCompletion(t *testing.T, repo, bashScript string, words ...string) []string {
	t.Helper()
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}

	compWords := append([]string{"rgit"}, words...)
	driver := bashScript + "\n" +
		"COMP_WORDS=(" + shellQuoteAll(compWords) + ")\n" +
		"COMP_CWORD=" + fmt.Sprint(len(compWords)-1) + "\n" +
		"_rgit_completion\n" +
		`printf '%s\n' "${COMPREPLY[@]}"` + "\n"

	cmd := exec.CommandContext(t.Context(), bashPath, "-c", driver) //nolint:gosec // completion tests intentionally execute the discovered shell with generated input
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(rgitBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash completion driver: %v: %s", err, out)
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// shellQuoteAll renders words as a bash array literal's contents, single
// quoting each so a word containing "$" or ":" is not reinterpreted by the
// driver script that assigns them into COMP_WORDS.
func shellQuoteAll(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

// TestCompletion_BashCompletesSymbolsFromSymbols is the dynamic half of
// shell completion coverage: completing the token after "FILE:" has to
// name a symbol `rgit commit` will really accept, for a file with more
// than one candidate and a worktree that has not been committed yet.
func TestCompletion_BashCompletesSymbolsFromSymbols(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gittest.Write(t, repo, "a.go", "package a\n\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 222\n}\n")

	script := runRgit(t, repo, "completion", "bash")
	qt.Assert(t, qt.Equals(script.ExitCode, 0))

	got := runBashCompletion(t, repo, script.Stdout, "commit", "a.go:")
	qt.Assert(t, qt.DeepEquals(got, []string{"a.go:A", "a.go:B"}))

	// A prefix after the colon narrows the same way any other compgen -W
	// match does.
	got = runBashCompletion(t, repo, script.Stdout, "commit", "a.go:A")
	qt.Assert(t, qt.DeepEquals(got, []string{"a.go:A"}))
}

// TestCompletion_BashDegradesSilentlyOutsideARepo pins the failure mode
// docs/USAGE.md § Shell completion promises: a cwd with no repository (so
// `rgit symbols` itself exits non-zero) must not put anything on the
// completion prompt, and the driver above would surface a bash error as a
// non-candidate line if the function leaked one.
func TestCompletion_BashDegradesSilentlyOutsideARepo(t *testing.T) {
	t.Parallel()
	notARepo := t.TempDir()

	// completion itself needs no repository; any cwd fetches the script.
	script := runRgit(t, notARepo, "completion", "bash")
	qt.Assert(t, qt.Equals(script.ExitCode, 0))

	got := runBashCompletion(t, notARepo, script.Stdout, "commit", "a.go:")
	qt.Assert(t, qt.Equals(len(got), 0))
}

// zshCompaddStub replaces zsh's real compadd, which only records candidates
// into the surrounding completion widget's state, with one that prints
// them -- there is no interactive completion widget here to record into.
// It reimplements just the two calling conventions _rgit's zsh script
// uses: a bare "compadd -- word..." and a prefixed "compadd -P p -- word...".
// The widget's own automatic filtering of those candidates against what is
// already typed (zsh's usual job, done without an explicit "-- $cur" the
// way bash's compgen needs) is exactly what this stub cannot reproduce
// outside a real completion context, so these tests assert the candidate
// set _rgit_symbols/compadd would offer, not the narrowed-by-what-you-typed
// subset a live Tab press shows -- the bash tests above already cover that
// narrowing, and the two scripts share the identical _rgit_symbols body.
const zshCompaddStub = `compadd() {
  local prefix=""
  local -a args
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -P) prefix="$2"; shift 2 ;;
      --) shift; args+=("$@"); break ;;
      *) args+=("$1"); shift ;;
    esac
  done
  local a
  for a in "${args[@]}"; do
    print -r -- "${prefix}${a}"
  done
}
`

// runZshCompletion is runBashCompletion's zsh counterpart: it sources
// zshScript under zshCompaddStub, sets words/CURRENT the way zsh's own
// completion frontend would, and calls _rgit directly -- -f skips rc files
// so the result depends only on what rgit emitted.
func runZshCompletion(t *testing.T, repo, zshScript string, words ...string) []string {
	t.Helper()
	zshPath, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not on PATH")
	}

	compWords := append([]string{"rgit"}, words...)
	driver := zshCompaddStub + zshScript + "\n" +
		"words=(" + shellQuoteAll(compWords) + ")\n" +
		"CURRENT=" + fmt.Sprint(len(compWords)) + "\n" +
		"_rgit\n"

	cmd := exec.CommandContext(t.Context(), zshPath, "-f", "-c", driver) //nolint:gosec // completion tests intentionally execute the discovered shell with generated input
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(rgitBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zsh completion driver: %v: %s", err, out)
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// TestCompletion_ZshCompletesSymbolsFromSymbols is
// TestCompletion_BashCompletesSymbolsFromSymbols's zsh counterpart: the
// two scripts share the same _rgit_symbols contract, so this pins that the
// zsh half of the emitted pair reads and prefixes the same symbol output
// correctly, not just that it parses.
func TestCompletion_ZshCompletesSymbolsFromSymbols(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gittest.Write(t, repo, "a.go", "package a\n\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 222\n}\n")

	script := runRgit(t, repo, "completion", "zsh")
	qt.Assert(t, qt.Equals(script.ExitCode, 0))

	got := runZshCompletion(t, repo, script.Stdout, "commit", "a.go:")
	qt.Assert(t, qt.DeepEquals(got, []string{"a.go:A", "a.go:B"}))
}

// TestCompletion_ZshDegradesSilentlyOutsideARepo is the zsh half of
// TestCompletion_BashDegradesSilentlyOutsideARepo: `rgit symbols` failing
// outside a repository must still leave compadd with nothing to add, not an
// error on the prompt.
func TestCompletion_ZshDegradesSilentlyOutsideARepo(t *testing.T) {
	t.Parallel()
	notARepo := t.TempDir()

	script := runRgit(t, notARepo, "completion", "zsh")
	qt.Assert(t, qt.Equals(script.ExitCode, 0))

	got := runZshCompletion(t, notARepo, script.Stdout, "commit", "a.go:")
	qt.Assert(t, qt.Equals(len(got), 0))
}

// runFishCompletion is runBashCompletion/runZshCompletion's fish
// counterpart. Fish has no COMPREPLY/compadd equivalent to drive directly
// outside a real line editor -- "commandline -opc"/"-ct", which
// __rgit_complete calls, only work inside fish's own completion machinery
// -- so this uses fish's documented non-interactive completion entry
// point instead: `complete -C"<cmdline>"` runs the identical machinery a
// real Tab press would and prints one candidate per line, filtered by
// whatever prefix the last word already carries (fish, unlike bash's
// compgen, applies that filtering itself).
func runFishCompletion(t *testing.T, repo, fishScript string, words ...string) []string {
	t.Helper()
	fishPath, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish not on PATH")
	}

	cmdline := "rgit " + strings.Join(words, " ")
	driver := fishScript + "\n" +
		`complete -C"` + strings.ReplaceAll(cmdline, `"`, `\"`) + `"` + "\n"

	cmd := exec.CommandContext(t.Context(), fishPath, "--no-config", "-c", driver) //nolint:gosec // completion tests intentionally execute the discovered shell with generated input
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(rgitBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fish completion driver: %v: %s", err, out)
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// TestCompletion_FishCompletesSymbolsFromSymbols is
// TestCompletion_BashCompletesSymbolsFromSymbols's fish counterpart: the
// fish script's own __rgit_symbols body reads the same symbol list the bash
// and zsh ones do, so this pins that fish's dynamic FILE:SYMBOL completion
// resolves against real, live symbol names too.
func TestCompletion_FishCompletesSymbolsFromSymbols(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gittest.Write(t, repo, "a.go", "package a\n\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 222\n}\n")

	script := runRgit(t, repo, "completion", "fish")
	qt.Assert(t, qt.Equals(script.ExitCode, 0))

	got := runFishCompletion(t, repo, script.Stdout, "commit", "a.go:")
	qt.Assert(t, qt.DeepEquals(got, []string{"a.go:A", "a.go:B"}))

	got = runFishCompletion(t, repo, script.Stdout, "commit", "a.go:A")
	qt.Assert(t, qt.DeepEquals(got, []string{"a.go:A"}))
}

// TestCompletion_FishDegradesSilentlyOutsideARepo is the fish half of
// TestCompletion_BashDegradesSilentlyOutsideARepo/
// TestCompletion_ZshDegradesSilentlyOutsideARepo.
func TestCompletion_FishDegradesSilentlyOutsideARepo(t *testing.T) {
	t.Parallel()
	notARepo := t.TempDir()

	script := runRgit(t, notARepo, "completion", "fish")
	qt.Assert(t, qt.Equals(script.ExitCode, 0))

	got := runFishCompletion(t, notARepo, script.Stdout, "commit", "a.go:")
	qt.Assert(t, qt.Equals(len(got), 0))
}

// runPwshCompletion loads the emitted PowerShell script, then asks
// CommandCompletion for the candidates at the end of a command line. This is
// the programmatic path behind TabExpansion2, without needing an interactive
// PowerShell host.
func runPwshCompletion(t *testing.T, repo, pwshScript string, words ...string) []string {
	t.Helper()
	pwshPath, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not on PATH")
	}

	cmdline := "rgit " + strings.Join(words, " ")
	driver := "$script = @'\n" + pwshScript + "\n'@\n" +
		"Invoke-Expression $script\n" +
		"$line = '" + strings.ReplaceAll(cmdline, "'", "''") + "'\n" +
		"$completion = [System.Management.Automation.CommandCompletion]::CompleteInput($line, $line.Length, $null)\n" +
		"$completion.CompletionMatches | ForEach-Object { $_.CompletionText }\n"

	cmd := exec.CommandContext(t.Context(), pwshPath, "-NoProfile", "-NonInteractive", "-Command", driver) //nolint:gosec // completion tests intentionally execute the discovered shell with generated input
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(rgitBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pwsh completion driver: %v: %s", err, out)
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// TestCompletion_PwshCompletesSymbolsFromSymbols is the PowerShell
// counterpart to the bash, zsh, and fish dynamic completion cases.
func TestCompletion_PwshCompletesSymbolsFromSymbols(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	gittest.Write(t, repo, "a.go", "package a\n\nfunc A() int {\n\treturn 111\n}\n\nfunc B() int {\n\treturn 222\n}\n")

	script := runRgit(t, repo, "completion", "pwsh")
	qt.Assert(t, qt.Equals(script.ExitCode, 0))

	got := runPwshCompletion(t, repo, script.Stdout, "commit", "a.go:")
	qt.Assert(t, qt.DeepEquals(got, []string{"a.go:A", "a.go:B"}))

	got = runPwshCompletion(t, repo, script.Stdout, "commit", "a.go:A")
	qt.Assert(t, qt.DeepEquals(got, []string{"a.go:A"}))
}

// TestCompletion_PwshDegradesSilentlyOutsideARepo is the PowerShell half of
// the shell completion tests' no-repository contract.
func TestCompletion_PwshDegradesSilentlyOutsideARepo(t *testing.T) {
	t.Parallel()
	notARepo := t.TempDir()

	script := runRgit(t, notARepo, "completion", "pwsh")
	qt.Assert(t, qt.Equals(script.ExitCode, 0))

	got := runPwshCompletion(t, notARepo, script.Stdout, "commit", "a.go:")
	qt.Assert(t, qt.Equals(len(got), 0))
}

// TestCompletion_FishOffersSubcommandsAndFlags pins the parts of the fish
// script bash/zsh have no equivalent for: the "-C <path>" pair walk uses
// fish's own commandline -opc/-ct split (opc never contains the token the
// cursor is still on, unlike bash's COMP_WORDS or zsh's words), so a
// trailing, not-yet-paired "-C" must resolve to directory completion
// rather than being mistaken for an already-consumed pair.
func TestCompletion_FishOffersSubcommandsAndFlags(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "a.go", "package a\n")

	script := runRgit(t, repo, "completion", "fish")
	qt.Assert(t, qt.Equals(script.ExitCode, 0))

	got := runFishCompletion(t, repo, script.Stdout, "")
	qt.Assert(t, qt.SliceContains(got, "diff"))
	qt.Assert(t, qt.SliceContains(got, "commit"))
	qt.Assert(t, qt.SliceContains(got, "-C"))

	got = runFishCompletion(t, repo, script.Stdout, "diff", "--")
	qt.Assert(t, qt.SliceContains(got, "--porcelain"))
	qt.Assert(t, qt.Not(qt.SliceContains(got, "-p")))

	t.Run("symbols offers --for-commit", func(t *testing.T) {
		got := runFishCompletion(t, repo, script.Stdout, "symbols", "--")
		qt.Assert(t, qt.SliceContains(got, "--for-commit"))
	})

	// A "-C" with no path after it yet completes as a directory, not the
	// subcommand list -- the case the opc/ct split above makes non-obvious.
	// __fish_complete_directories appends its own "\tDirectory" hint to
	// each candidate, unlike this script's own plain printf'd ones, so the
	// match is by prefix rather than exact equality.
	qt.Assert(t, qt.IsNil(os.Mkdir(filepath.Join(repo, "sub"), 0o750)))
	got = runFishCompletion(t, repo, script.Stdout, "-C", "")
	sawSubdir := false
	for _, c := range got {
		if strings.HasPrefix(c, "sub/") {
			sawSubdir = true
		}
	}
	qt.Assert(t, qt.IsTrue(sawSubdir))
	qt.Assert(t, qt.Not(qt.SliceContains(got, "diff")))
}
