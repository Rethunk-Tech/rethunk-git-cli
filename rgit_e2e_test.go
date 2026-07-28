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
		os.Exit(m.Run())
	}

	bin, cleanup, err := buildRgit()
	if err != nil {
		fmt.Fprintln(os.Stderr, "build rgit for e2e tests:", err)
		os.Exit(1)
	}
	rgitBin = bin
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
	// Not -race. Measured: race-instrumenting the cgo tree-sitter parse path
	// costs 24x per invocation (1.05s against 0.044s), which across this
	// file's invocations is minutes rather than seconds -- and it buys
	// almost nothing, since a single rgit invocation resolves in sequence
	// (lsp.Session: "not safe for concurrent use"). The concurrency worth
	// checking is the jsonrpc2 read goroutine and the daemon spawn lock,
	// both in-process: `go test -race ./...` reaches them and this does not.
	cmd := exec.Command("go", "build", "-cover", "-o", bin, ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", cleanup, fmt.Errorf("go build: %w: %s", err, stderr.String())
	}
	return bin, cleanup, nil
}

// newTempRepo creates an empty git repository with no commits. Rule 4/5
// path checks fall back to worktree existence alone on an unborn branch,
// so none of this file's cases need an initial commit.
func newTempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return dir
}

func writeFile(t *testing.T, repo, relPath, content string) {
	t.Helper()
	full := filepath.Join(repo, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type rgitResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func runRgit(t *testing.T, repoDir string, args ...string) rgitResult {
	t.Helper()
	requireBinary(t)
	cmd := exec.Command(rgitBin, args...)
	cmd.Dir = repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("running rgit %v: %v", args, err)
		}
	}
	return rgitResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}
}

func TestCommit_InterspersedFlagAfterPositional(t *testing.T) {
	t.Parallel()
	// Pinned defect: stdlib flag and ff/ffcli stop parsing at the first
	// positional, so this exact argv shape would silently yield zero
	// messages and two targets ("auth.go:Foo", "msg"), failing with
	// exit 129 ("commit requires a message"). pflag's interspersed
	// parsing must read one message and one target instead, letting the
	// commit actually succeed.
	repo := newTempRepo(t)
	writeFile(t, repo, "auth.go", "package main\n\nfunc Foo() {}\n")

	got := runRgit(t, repo, "commit", "auth.go:Foo", "-m", "msg")

	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.Not(qt.StringContains(got.Stderr, "requires a message")))
}

func TestCommit_ColonInFilenameIsPathspec(t *testing.T) {
	t.Parallel()
	// "src/notes:draft.md" is a legal tracked path (design.md measured
	// git accepting it). Rule 4's existing-path check must claim it
	// whole, before rule 5 gets a chance to split it into a bogus
	// FILE:NAME anchor at the interior colon.
	repo := newTempRepo(t)
	writeFile(t, repo, "src/notes:draft.md", "draft\n")

	got := runRgit(t, repo, "commit", "src/notes:draft.md", "-m", "chore: add draft notes")

	qt.Assert(t, qt.Equals(got.ExitCode, 0))
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
	repo := newTempRepo(t)

	got := runRgit(t, repo, "commit", "-m", "chore: force pathspec", "--", "missing.go:NotASymbol")

	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.GitFailure)))
	qt.Assert(t, qt.StringContains(got.Stderr, "did not match any files"))
	qt.Assert(t, qt.Not(qt.StringContains(got.Stderr, "cannot classify")))
}

func TestCommit_LeadingColonPathspecMagicPassesThrough(t *testing.T) {
	t.Parallel()
	// Rule 2: all git pathspec magic is leading-colon, so this is claimed
	// immediately, with no existence check at all -- paired here with a
	// real target so the commit has something to actually stage.
	repo := newTempRepo(t)
	writeFile(t, repo, "keep.go", "package main\n\nfunc Keep() {}\n")

	got := runRgit(t, repo, "commit", "-m", "chore: exclude docs", "keep.go", ":(exclude)docs/*")

	qt.Assert(t, qt.Equals(got.ExitCode, 0))
}

func TestCommit_UnresolvableArgumentListsTriedInterpretations(t *testing.T) {
	t.Parallel()
	// Rule 6: none of the applicable rules matched. commit passes
	// allowRevisions=false to ClassifyArgs (rule 3 is diff-only), so the
	// error must list pathspec-magic, existing-path, and symbol-anchor —
	// and must not claim a revision interpretation was tried.
	repo := newTempRepo(t)

	got := runRgit(t, repo, "commit", "-m", "chore: bogus target", "totally-bogus-target")

	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.InvalidUsage)))
	qt.Assert(t, qt.StringContains(got.Stderr, `cannot classify "totally-bogus-target"`))
	qt.Assert(t, qt.StringContains(got.Stderr, "pathspec magic"))
	qt.Assert(t, qt.StringContains(got.Stderr, "existing path"))
	qt.Assert(t, qt.StringContains(got.Stderr, "symbol anchor"))
	qt.Assert(t, qt.Not(qt.StringContains(got.Stderr, "revision")))
}

func TestInvalidFlagCombinations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		args       []string
		wantSubstr string
	}{
		{
			name:       "dry-run and push",
			args:       []string{"commit", "-m", "chore: x", "--dry-run", "--push"},
			wantSubstr: "--dry-run and --push",
		},
		{
			name:       "message and message-file",
			args:       []string{"commit", "-m", "chore: x", "-F", "msg.txt"},
			wantSubstr: "-m and -F",
		},
		{
			name:       "staged and range",
			args:       []string{"diff", "--staged", "--range", "HEAD"},
			wantSubstr: "--staged and --range",
		},
		{
			name:       "staged and unstaged",
			args:       []string{"diff", "--staged", "--unstaged"},
			wantSubstr: "--staged and --unstaged",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTempRepo(t)
			got := runRgit(t, repo, tc.args...)
			qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.InvalidUsage)))
			qt.Assert(t, qt.StringContains(got.Stderr, tc.wantSubstr))
		})
	}
}

func TestContradictoryPathAndAnchor(t *testing.T) {
	t.Parallel()
	// Pinned defect: the check ran on flag values only, so the positional
	// spelling fell straight through it. Both targets were then built, and
	// apply() ran `git add greet.go` before overwriting that same index
	// entry with a blob synthesized from HEAD plus one extent -- silently
	// dropping every other worktree change in the file the caller had just
	// asked for by path, while the listing still reported the whole path's
	// line counts.
	//
	// docs/CODES.md § Exit codes assigns 5 to naming a path both ways; the
	// spelling used to say it cannot change the answer.
	setup := func(t *testing.T) string {
		t.Helper()
		repo := newTempRepo(t)
		writeFile(t, repo, "greet.go", "package main\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
		return repo
	}

	contradictory := []struct {
		name string
		args []string
	}{
		{"both positional", []string{"commit", "-m", "chore: x", "greet.go", "greet.go:A"}},
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
		writeFile(t, repo, "other.go", "package main\n\nfunc C() int {\n\treturn 3\n}\n")

		got := runRgit(t, repo, "commit", "-m", "chore: mixed targets", "other.go", "greet.go:A")

		qt.Assert(t, qt.Equals(got.ExitCode, 0))
	})
}

func TestCommit_AnnouncesPreambleAndOrdinalAnchors(t *testing.T) {
	t.Parallel()
	// docs/ANCHORS.md documents both announcements: the new-file preamble is
	// "announced on stderr", and an ordinal is a last resort that "warns and
	// suggests qualification". Both were silent.
	t.Run("new-file preamble is announced", func(t *testing.T) {
		repo := newTempRepo(t)
		writeFile(t, repo, "new.go", "package main\n\nimport \"fmt\"\n\nfunc Hi() { fmt.Println(\"hi\") }\n")

		got := runRgit(t, repo, "commit", "-m", "feat(x): hi", "new.go:Hi")

		qt.Assert(t, qt.Equals(got.ExitCode, 0))
		qt.Assert(t, qt.StringContains(got.Stderr, "new.go is new"))
		qt.Assert(t, qt.StringContains(got.Stderr, "@header"))
	})

	t.Run("ordinal anchors warn", func(t *testing.T) {
		repo := newTempRepo(t)
		writeFile(t, repo, "dup.go", "package main\n\nfunc init() { println(1) }\n\nfunc init() { println(2) }\n")

		got := runRgit(t, repo, "commit", "-m", "feat(x): dup", "dup.go:init#2")

		qt.Assert(t, qt.Equals(got.ExitCode, 0))
		qt.Assert(t, qt.StringContains(got.Stderr, "dup.go:init#2"))
		qt.Assert(t, qt.StringContains(got.Stderr, "positional"))
	})

	t.Run("a uniquely named anchor does not warn", func(t *testing.T) {
		// The warning must key on the ordinal form, not fire on every anchor.
		repo := newTempRepo(t)
		writeFile(t, repo, "one.go", "package main\n\nfunc Only() {}\n")

		got := runRgit(t, repo, "commit", "-m", "feat(x): only", "one.go:Only")

		qt.Assert(t, qt.Equals(got.ExitCode, 0))
		qt.Assert(t, qt.Not(qt.StringContains(got.Stderr, "positional")))
	})
}

func TestDiff_CrossCheckReportsWithoutGating(t *testing.T) {
	t.Parallel()
	// The cross-check ran only on the commit path, so rgit diff could emit
	// an anchor rgit commit then refused with exit 6 -- the closed loop held
	// syntactically and not semantically. Diff reports rather than gates: a
	// disagreement is worth knowing while reading the diff, but a read-only
	// command must not fail on one, and a server that is absent, slow or
	// silent about a symbol stays the normal case.
	repo := newTempRepo(t)
	writeFile(t, repo, "go.mod", "module x\n\ngo 1.21\n")
	writeFile(t, repo, "a.go", "package x\n\n// Doc for A.\nfunc A() int {\n\treturn 1\n}\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "-c", "user.email=t@t.t", "-c", "user.name=T", "commit", "-q", "-m", "init")
	writeFile(t, repo, "a.go", "package x\n\n// Doc for A.\nfunc A() int {\n\treturn 111\n}\n")

	got := runRgit(t, repo, "diff", "--porcelain")

	// Extents agree, so nothing is reported -- the false-positive guard that
	// matters most, since a warning on every symbol would be worse than none.
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.StringContains(got.Stdout, "a.go"))
	qt.Assert(t, qt.Not(qt.StringContains(got.Stderr, "[warning]")))
}

func TestDocumentedPathsWithoutOtherCoverage(t *testing.T) {
	t.Parallel()
	// Each of these is specified in docs/USAGE.md and was reachable only
	// through paths no other case exercised.
	commitOne := func(t *testing.T, repo string) {
		t.Helper()
		gitIn(t, repo, "add", "-A")
		gitIn(t, repo, "-c", "user.email=t@t.t", "-c", "user.name=T", "commit", "-q", "-m", "init")
	}

	t.Run("diff --exit-code reports 1 when committable", func(t *testing.T) {
		repo := newTempRepo(t)
		writeFile(t, repo, "a.go", "package main\n\nfunc A() int { return 1 }\n")
		commitOne(t, repo)
		writeFile(t, repo, "a.go", "package main\n\nfunc A() int { return 2 }\n")

		dirty := runRgit(t, repo, "diff", "--exit-code", "--porcelain")
		qt.Assert(t, qt.Equals(dirty.ExitCode, 1))
		qt.Assert(t, qt.StringContains(dirty.Stdout, "a.go"))

		// git's own --exit-code convention: 0 once there is nothing to report.
		commitOne(t, repo)
		clean := runRgit(t, repo, "diff", "--exit-code", "--porcelain")
		qt.Assert(t, qt.Equals(clean.ExitCode, 0))
	})

	t.Run("diff --quiet implies --exit-code and prints nothing", func(t *testing.T) {
		repo := newTempRepo(t)
		writeFile(t, repo, "a.go", "package main\n\nfunc A() int { return 1 }\n")
		commitOne(t, repo)
		writeFile(t, repo, "a.go", "package main\n\nfunc A() int { return 2 }\n")

		got := runRgit(t, repo, "diff", "--quiet")

		qt.Assert(t, qt.Equals(got.ExitCode, 1))
		qt.Assert(t, qt.Equals(got.Stdout, ""))
	})

	t.Run("a non-conventional message warns but still commits", func(t *testing.T) {
		repo := newTempRepo(t)
		writeFile(t, repo, "a.go", "package main\n\nfunc A() {}\n")

		got := runRgit(t, repo, "commit", "-m", "just some words", "a.go:A")

		qt.Assert(t, qt.Equals(got.ExitCode, 0))
		qt.Assert(t, qt.StringContains(got.Stderr, "type(scope): subject"))
	})

	t.Run("no reachable language server degrades to ts-only", func(t *testing.T) {
		// specs/design.md: degraded resolution is normal, announced once, and
		// never blocks.
		//
		// Both routes to a server have to be closed, or this passes or fails
		// on what the developer's machine happens to be running: stripping
		// PATH stops a spawn, and pointing XDG_RUNTIME_DIR at an empty
		// directory stops the socket probe finding a daemon some earlier
		// invocation left behind.
		requireBinary(t)
		repo := newTempRepo(t)
		writeFile(t, repo, "a.go", "package main\n\nfunc A() int { return 1 }\n")

		cmd := exec.Command(rgitBin, "commit", "-m", "feat(x): a", "a.go:A")
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

	t.Run("push failure is exit 8 and keeps the commit", func(t *testing.T) {
		// docs/USAGE.md § Flags: a push failure does not roll back the commit
		// that preceded it. No remote is configured, so the push cannot work.
		repo := newTempRepo(t)
		writeFile(t, repo, "a.go", "package main\n\nfunc A() {}\n")

		got := runRgit(t, repo, "commit", "--push", "-m", "feat(x): a", "a.go:A")

		qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.PushFailed)))
		// The commit itself landed: HEAD resolves and holds the file.
		qt.Assert(t, qt.StringContains(gitIn(t, repo, "cat-file", "-p", "HEAD:a.go"), "func A()"))
	})
}

// --- rgit diff execution ----------------------------------------------
//
// These cases build real temporary git repositories with real commits, per
// this file's own doc comment: the assertions below are about git's
// behaviour (scope selection, numstat's mode/binary conventions, untracked
// discovery), not about internal/diff's internals in isolation.

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// initRepoWithFile creates a repo, writes relPath, and commits it as the
// base state every diff scope in this file's tests compares against.
func initRepoWithFile(t *testing.T, relPath, content string) string {
	t.Helper()
	dir := newTempRepo(t)
	writeFile(t, dir, relPath, content)
	gitIn(t, dir, "add", "--", relPath)
	gitIn(t, dir, "commit", "-q", "-m", "init")
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
	writeFile(t, repo, "auth.go", authGoV2)
	// Staged: a brand new file, added but not committed.
	writeFile(t, repo, "staged.go", "package auth\n\nfunc Staged() int { return 3 }\n")
	gitIn(t, repo, "add", "--", "staged.go")
	// Untracked: never added at all.
	writeFile(t, repo, "untracked.go", "package auth\n\nfunc Untracked() int { return 4 }\n")

	got := runRgit(t, repo, "diff", "--porcelain")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	rows := parsePorcelain(t, got.Stdout)

	if _, ok := findRow(rows, "auth.go", "MOD"); !ok {
		t.Errorf("default scope missed the unstaged change to auth.go: %+v", rows)
	}
	if _, ok := findRow(rows, "staged.go", "MOD"); !ok {
		t.Errorf("default scope missed the staged-only new file staged.go: %+v", rows)
	}
	if _, ok := findRow(rows, "untracked.go", "UNTRACKED"); !ok {
		t.Errorf("default scope missed the untracked file untracked.go: %+v", rows)
	}
}

func TestDiff_UnbornBranchListsEverythingCommittable(t *testing.T) {
	t.Parallel()
	// A fresh `git init` has no HEAD, so the default scope cannot run
	// `git diff HEAD` -- it compares against the empty tree instead.
	repo := newTempRepo(t)
	writeFile(t, repo, "staged.go", "package auth\n\nfunc Staged() int { return 3 }\n")
	gitIn(t, repo, "add", "--", "staged.go")
	writeFile(t, repo, "untracked.go", "package auth\n\nfunc Untracked() int { return 4 }\n")

	got := runRgit(t, repo, "diff", "--porcelain")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	rows := parsePorcelain(t, got.Stdout)

	if _, ok := findRow(rows, "staged.go", "MOD"); !ok {
		t.Errorf("unborn-branch diff missed the staged file: %+v", rows)
	}
	if _, ok := findRow(rows, "untracked.go", "UNTRACKED"); !ok {
		t.Errorf("unborn-branch diff missed the untracked file: %+v", rows)
	}
}

func TestCommit_PushAfterSuccessfulCommit(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "auth.go", authGoV1)
	remote := t.TempDir()
	gitIn(t, remote, "init", "-q", "--bare")
	gitIn(t, repo, "remote", "add", "origin", remote)
	branch := strings.TrimSpace(gitIn(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	gitIn(t, repo, "push", "-q", "-u", "origin", branch)

	writeFile(t, repo, "auth.go", authGoV2)
	got := runRgit(t, repo, "commit", "--push", "-m", "fix(auth): reject expired", "auth.go:ValidateToken")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))

	local := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))
	qt.Assert(t, qt.Equals(strings.TrimSpace(gitIn(t, remote, "rev-parse", branch)), local))
}

func TestCommit_MultiLanguageSymbolGranularity(t *testing.T) {
	t.Parallel()
	// One commit naming a symbol in each v1 grammar. The point is that
	// each file's OTHER symbol changed too and must stay uncommitted:
	// symbol granularity has to hold per grammar, in a single invocation.
	repo := newTempRepo(t)
	writeFile(t, repo, "auth.go", "package auth\n\nfunc GoA() int { return 1 }\n\nfunc GoB() int { return 1 }\n")
	writeFile(t, repo, "app.ts", "export function TsA(): number { return 1 }\n\nexport function TsB(): number { return 1 }\n")
	writeFile(t, repo, "svc.py", "def py_a():\n    return 1\n\n\ndef py_b():\n    return 1\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "init")

	writeFile(t, repo, "auth.go", "package auth\n\nfunc GoA() int { return 2 }\n\nfunc GoB() int { return 2 }\n")
	writeFile(t, repo, "app.ts", "export function TsA(): number { return 2 }\n\nexport function TsB(): number { return 2 }\n")
	writeFile(t, repo, "svc.py", "def py_a():\n    return 2\n\n\ndef py_b():\n    return 2\n")

	got := runRgit(t, repo, "commit", "-m", "fix: bump the first of each",
		"auth.go:GoA", "app.ts:TsA", "svc.py:py_a")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))

	for _, c := range []struct{ path, committed, withheld string }{
		{"auth.go", "func GoA() int { return 2 }", "func GoB() int { return 1 }"},
		{"app.ts", "export function TsA(): number { return 2 }", "export function TsB(): number { return 1 }"},
		{"svc.py", "def py_a():\n    return 2", "def py_b():\n    return 1"},
	} {
		head := gitIn(t, repo, "show", "HEAD:"+c.path)
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
	repo := newTempRepo(t)
	writeFile(t, repo, "pre-commit", "#!/usr/bin/env bash\n\nfoo() {\n  echo v1\n}\n\nbar() {\n  echo bar\n}\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "init")

	writeFile(t, repo, "pre-commit", "#!/usr/bin/env bash\n\nfoo() {\n  echo v2\n}\n\nbar() {\n  echo changed too\n}\n")

	got := runRgit(t, repo, "commit", "-m", "fix: bump foo only", "pre-commit:foo")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))

	head := gitIn(t, repo, "show", "HEAD:pre-commit")
	qt.Assert(t, qt.StringContains(head, "echo v2"))
	qt.Assert(t, qt.StringContains(head, "echo bar")) // bar's edit stayed uncommitted

	// A zsh shebang is deliberately not routed to the shell grammar
	// (tree-sitter-bash mis-parses zsh-only syntax), so an extensionless
	// zsh script still refuses a symbol anchor -- the same exit 9 an
	// unrecognized extension already gets, not a new failure mode.
	writeFile(t, repo, "zsh-script", "#!/bin/zsh\n\nfoo() {\n  echo hi\n}\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "add zsh script")
	got = runRgit(t, repo, "commit", "-m", "chore: touch", "zsh-script:foo")
	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.UnsupportedLanguage)))
}

func TestCommit_AllTargetsUnchangedExits11(t *testing.T) {
	t.Parallel()
	// docs/USAGE.md: an unchanged target warns and is skipped; exit is 11
	// only when EVERY named target turned out unchanged.
	repo := initRepoWithFile(t, "auth.go", authGoV1)

	got := runRgit(t, repo, "commit", "-m", "chore: noop", "auth.go:ValidateToken")
	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.NothingToCommit)))
	qt.Assert(t, qt.StringContains(got.Stderr, "no uncommitted changes"))
}

func TestCommit_FromSubdirectoryResolvesCWDRelativePaths(t *testing.T) {
	t.Parallel()
	// git resolves a pathspec relative to the current directory: `git add
	// a.go` in pkg/deep stages pkg/deep/a.go. Output stays root-relative,
	// as git's own --numstat does.
	repo := newTempRepo(t)
	writeFile(t, repo, "pkg/deep/a.go", "package deep\n\nfunc Alpha() int { return 1 }\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "init")
	writeFile(t, repo, "pkg/deep/a.go", "package deep\n\nfunc Alpha() int { return 42 }\n")

	sub := filepath.Join(repo, "pkg", "deep")

	got := runRgit(t, sub, "diff", "--porcelain", "a.go")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.StringContains(got.Stdout, "pkg/deep/a.go"))

	got = runRgit(t, sub, "commit", "-m", "fix: bump", "a.go:Alpha")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.StringContains(gitIn(t, repo, "show", "--stat", "--format=", "HEAD"), "pkg/deep/a.go"))
}

func TestDiff_RevisionRangeScopes(t *testing.T) {
	t.Parallel()
	// Precedence rule 3's three reachable shapes: a bare revision against
	// the worktree, a two-dot range, and a three-dot range (whose old side
	// is the merge base, not the left endpoint).
	repo := initRepoWithFile(t, "auth.go", authGoV1)
	writeFile(t, repo, "auth.go", authGoV2)
	gitIn(t, repo, "commit", "-q", "-a", "-m", "fix: v2")

	for _, rev := range []string{"HEAD~1", "HEAD~1..HEAD", "HEAD~1...HEAD"} {
		got := runRgit(t, repo, "diff", "--porcelain", rev)
		qt.Assert(t, qt.Equals(got.ExitCode, 0))
		if !strings.Contains(got.Stdout, "auth.go") {
			t.Errorf("scope %q reported no change to auth.go: %q", rev, got.Stdout)
		}
	}
}

func TestDiff_MalformedSymAndPathEscapeRejected(t *testing.T) {
	t.Parallel()
	// Both subcommands reject these identically: docs/USAGE.md's exit
	// table qualifies neither to one of them. Silently dropping a
	// malformed --sym would leave the caller reading an unfiltered diff
	// believing it was filtered.
	repo := initRepoWithFile(t, "auth.go", authGoV1)

	got := runRgit(t, repo, "diff", "--sym", "malformed")
	qt.Assert(t, qt.Equals(got.ExitCode, 129))

	got = runRgit(t, repo, "diff", "--file", "../outside.txt")
	qt.Assert(t, qt.Equals(got.ExitCode, 129))
}

func TestDiff_UnstagedScopeExcludesStaged(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "auth.go", authGoV1)

	writeFile(t, repo, "auth.go", authGoV2) // unstaged change
	writeFile(t, repo, "staged.go", "package auth\n\nfunc Staged() int { return 3 }\n")
	gitIn(t, repo, "add", "--", "staged.go") // staged-only change

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
	writeFile(t, repo, "auth.go", authGoV2) // modifies ValidateToken, deletes oldHelper

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
			src, err = os.ReadFile(filepath.Join(repo, r.File))
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

func TestDiff_ModeRowOnChmod(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "script.sh", "#!/bin/sh\necho hi\n")

	if err := os.Chmod(filepath.Join(repo, "script.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	rows := parsePorcelain(t, runRgit(t, repo, "diff", "--porcelain").Stdout)
	row, ok := findRow(rows, "script.sh", "MODE")
	if !ok {
		t.Fatalf("chmod +x with no content change must surface as MODE, not silently clean: %+v", rows)
	}
	qt.Assert(t, qt.Equals(row.Added, "0"))
	qt.Assert(t, qt.Equals(row.Deleted, "0"))

	text := runRgit(t, repo, "diff").Stdout
	qt.Assert(t, qt.StringContains(text, "644"))
	qt.Assert(t, qt.StringContains(text, "755"))
}

func TestDiff_BinaryRowUsesDashCounts(t *testing.T) {
	t.Parallel()
	binary := []byte("PNGFAKE\x00\x01binary")
	repo := newTempRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "logo.bin"), binary, 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "--", "logo.bin")
	gitIn(t, repo, "commit", "-q", "-m", "add binary")

	changed := append(append([]byte(nil), binary...), 'X')
	if err := os.WriteFile(filepath.Join(repo, "logo.bin"), changed, 0o644); err != nil {
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
	writeFile(t, repo, "notes.go", after)

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

// installHook writes an executable git hook, e.g. a pre-commit hook that
// exits non-zero to exercise AGENTS.md's "a rejected commit leaves staging
// in place" rule.
func installHook(t *testing.T, repo, name, script string) {
	t.Helper()
	path := filepath.Join(repo, ".git", "hooks", name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

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
	installHook(t, repo, "pre-commit", "#!/bin/sh\ntouch \""+marker+"\"\n")

	writeFile(t, repo, "auth.go", commitHappyV2)

	diffGot := runRgit(t, repo, "diff", "--porcelain")
	qt.Assert(t, qt.Equals(diffGot.ExitCode, 0))
	if _, ok := findRow(parsePorcelain(t, diffGot.Stdout), "auth.go", "MOD"); !ok {
		t.Fatalf("rgit diff must show auth.go as modified before commit: %q", diffGot.Stdout)
	}

	got := runRgit(t, repo, "commit", "auth.go:A", "-m", "feat(auth): give A a real value")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))

	head := gitIn(t, repo, "show", "HEAD:auth.go")
	qt.Assert(t, qt.StringContains(head, "return 100"))
	qt.Assert(t, qt.Not(qt.StringContains(head, "return 200")))

	// Clean index: nothing left staged after the commit.
	qt.Assert(t, qt.Equals(gitIn(t, repo, "diff", "--staged", "--numstat"), ""))

	// B's own edit is still outstanding, unstaged -- staging never touched it.
	qt.Assert(t, qt.StringContains(gitIn(t, repo, "diff", "--numstat"), "auth.go"))

	// The worktree file itself is never touched by staging.
	onDisk, err := os.ReadFile(filepath.Join(repo, "auth.go"))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(string(onDisk), commitHappyV2))

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("pre-commit hook did not run: %v", err)
	}
}

func TestCommit_PreStagedSiblingFileComesAlong(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "auth.go", commitHappyV1)
	writeFile(t, repo, "auth.go", commitHappyV2)
	writeFile(t, repo, "sibling.txt", "never named to rgit\n")
	gitIn(t, repo, "add", "--", "sibling.txt")

	got := runRgit(t, repo, "commit", "auth.go:A", "-m", "feat(auth): update A")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))

	show := gitIn(t, repo, "show", "--stat", "HEAD")
	qt.Assert(t, qt.StringContains(show, "sibling.txt"))
}

func TestCommit_HookRejectionLeavesStagingIntact(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "auth.go", commitHappyV1)
	writeFile(t, repo, "auth.go", commitHappyV2)
	installHook(t, repo, "pre-commit", "#!/bin/sh\nexit 1\n")

	got := runRgit(t, repo, "commit", "auth.go:A", "-m", "feat(auth): update A")
	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.GitFailure)))

	// Nothing rolled back: A's synthesized edit is still staged.
	indexed := gitIn(t, repo, "show", ":auth.go")
	qt.Assert(t, qt.StringContains(indexed, "return 100"))
	qt.Assert(t, qt.Not(qt.StringContains(indexed, "return 200")))
	// Staged (index differs from HEAD) AND unstaged (B's edit, worktree
	// differs from index) both hold: git's porcelain reports "MM".
	status := gitIn(t, repo, "status", "--porcelain")
	qt.Assert(t, qt.StringContains(status, "MM auth.go"))
}

func TestCommit_ResolveAllBeforeStageLeavesIndexUntouched(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "auth.go", commitHappyV1)
	writeFile(t, repo, "auth.go", commitHappyV2)

	// "Bogus" resolves nowhere -- the whole batch must fail before A (which
	// resolves cleanly) is ever staged.
	got := runRgit(t, repo, "commit", "auth.go:A", "auth.go:Bogus", "-m", "feat(auth): update A")
	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.AnchorUnresolvable)))

	status := gitIn(t, repo, "status", "--porcelain")
	qt.Assert(t, qt.Equals(status, " M auth.go\n"))
}

func TestCommit_PositionalPathspecParityWithFileFlag(t *testing.T) {
	t.Parallel()
	repoPositional := newTempRepo(t)
	writeFile(t, repoPositional, "notes.txt", "hello\n")
	gotPositional := runRgit(t, repoPositional, "commit", "notes.txt", "-m", "chore: add notes")
	qt.Assert(t, qt.Equals(gotPositional.ExitCode, 0))

	repoFlag := newTempRepo(t)
	writeFile(t, repoFlag, "notes.txt", "hello\n")
	gotFlag := runRgit(t, repoFlag, "commit", "--file", "notes.txt", "-m", "chore: add notes")
	qt.Assert(t, qt.Equals(gotFlag.ExitCode, 0))

	qt.Assert(t, qt.Equals(gitIn(t, repoPositional, "show", "HEAD:notes.txt"), gitIn(t, repoFlag, "show", "HEAD:notes.txt")))
}

func TestCommit_PathEscapeRejected(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q", ".")

	// A real file just outside the repo root: rule 4's existence check
	// succeeds, so the token reaches rgit's own target construction --
	// proving the escape is caught there, not merely that classification
	// found no interpretation for it at all.
	if err := os.WriteFile(filepath.Join(parent, "outside.txt"), []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := runRgit(t, repo, "commit", "-m", "chore: escape", "../outside.txt")
	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.InvalidUsage)))
	qt.Assert(t, qt.StringContains(got.Stderr, "escapes the repository root"))
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
	writeFile(t, repo, "auth.go", commitHappyV2)

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

func TestCommit_DryRunPreviewsAndStagesNothing(t *testing.T) {
	t.Parallel()
	// A preview that prints nothing and exits 0 is indistinguishable from
	// one that resolved nothing at all, which defeats the point of asking.
	repo := initRepoWithFile(t, "auth.go", commitHappyV1)
	writeFile(t, repo, "auth.go", commitHappyV2)

	before := gitIn(t, repo, "rev-parse", "HEAD")
	got := runRgit(t, repo, "commit", "--dry-run", "auth.go:A", "-m", "feat(auth): preview only")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.StringContains(got.Stdout, "auth.go:A"))

	// The preview reports magnitude, not just names, and its numbers come
	// from the same counter `rgit diff` renders with -- assert they agree,
	// since a preview that contradicts the diff it previews is worse than
	// no preview at all.
	diffGot := runRgit(t, repo, "diff", "--porcelain")
	row, ok := findRow(parsePorcelain(t, diffGot.Stdout), "auth.go", "MOD")
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.StringContains(got.Stdout, "+"+row.Added+"/-"+row.Deleted))

	// A whole-path target reports counts too. Labelling it "(path)" and
	// leaving the numbers out made the preview inconsistent with the diff
	// for exactly the targets a caller is least able to eyeball.
	writeFile(t, repo, "notes.md", "one\ntwo\n")
	pathGot := runRgit(t, repo, "commit", "--dry-run", "notes.md", "-m", "docs: preview a path")
	qt.Assert(t, qt.Equals(pathGot.ExitCode, 0))
	qt.Assert(t, qt.StringContains(pathGot.Stdout, "notes.md"))
	qt.Assert(t, qt.StringContains(pathGot.Stdout, "+2/-0"))

	// "writes no objects, stages nothing" (docs/USAGE.md): HEAD unmoved and
	// the index untouched.
	qt.Assert(t, qt.Equals(gitIn(t, repo, "rev-parse", "HEAD"), before))
	qt.Assert(t, qt.Equals(gitIn(t, repo, "diff", "--staged", "--numstat"), ""))
}

func TestCommit_PathAlreadyStagedAsDeleted(t *testing.T) {
	t.Parallel()
	// After `git rm`, the path matches nothing in the worktree and nothing
	// in the index, so `git add` rejects it as a bad pathspec. Naming
	// something already staged exactly as asked is not an error -- the
	// commit includes it either way -- and failing made `rgit commit <path>`
	// unusable after a `git rm`.
	repo := initRepoWithFile(t, "auth.go", commitHappyV1)
	writeFile(t, repo, "gone.md", "bye\n")
	gitIn(t, repo, "add", "gone.md")
	gitIn(t, repo, "commit", "-q", "-m", "chore: add gone.md")
	gitIn(t, repo, "rm", "-q", "gone.md")

	got := runRgit(t, repo, "commit", "gone.md", "-m", "chore: drop gone.md")
	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.StringContains(got.Stdout, "1 deletion"))
	qt.Assert(t, qt.Not(qt.StringContains(gitIn(t, repo, "ls-files"), "gone.md")))
}

func TestOutput_OrderedByPathThenPosition(t *testing.T) {
	t.Parallel()
	// Both listings sort alphabetically by path, then ascending by position
	// within each file -- the same contract `git status` offers. Output that
	// followed discovery order put @imports last despite it being the first
	// thing in the file, and `rgit commit` echoed whatever order the caller
	// happened to type. Neither is greppable, and neither is stable between
	// runs on an unchanged tree.
	src := "package p\n\nimport \"fmt\"\n\nfunc Zebra() int { return 1 }\n\nfunc Apple() int { return 2 }\n\nfunc Mango() int { return 3 }\n"
	repo := initRepoWithFile(t, "b.go", src)
	writeFile(t, repo, "a.go", src)
	writeFile(t, repo, "zsub/c.go", src)
	gitIn(t, repo, "add", "a.go", "zsub/c.go")
	gitIn(t, repo, "commit", "-q", "-m", "chore: siblings")

	edited := strings.NewReplacer(
		"return 1", "return 11",
		"return 2", "return 22",
		"return 3", "return 33",
	).Replace(src)
	for _, p := range []string{"a.go", "b.go", "zsub/c.go"} {
		writeFile(t, repo, p, edited)
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
	// specs/design.md:231 measured "--help tokens" per flag library as a
	// selection criterion, but nothing ever wired the flag up: bare
	// "--help", "-h", and "help" all fell into the unknown-command branch
	// (exit 129). All three now print the same top-level help to stdout
	// and exit 0.
	repo := newTempRepo(t)
	for _, spelling := range []string{"--help", "-h", "help"} {
		t.Run(spelling, func(t *testing.T) {
			got := runRgit(t, repo, spelling)
			qt.Assert(t, qt.Equals(got.ExitCode, 0))
			// Not an exact-empty check: the e2e binary is built with -cover
			// (buildRgit), which itself warns on stderr when GOCOVERDIR is
			// unset -- noise unrelated to this command's own behaviour.
			qt.Assert(t, qt.Not(qt.StringContains(got.Stderr, "rgit:")))
			qt.Assert(t, qt.StringContains(got.Stdout, "diff"))
			qt.Assert(t, qt.StringContains(got.Stdout, "commit"))
			qt.Assert(t, qt.StringContains(got.Stdout, "--version"))
		})
	}
}

func TestHelp_BareInvocationStillExitsInvalidUsage(t *testing.T) {
	t.Parallel()
	// Bare `git` prints its own full help to stdout at exit 1 -- but rgit
	// has exactly two subcommands and no useful no-op mode, and every other
	// usage error in its table (missing message, no target, ...) is already
	// pinned to exit 129. Naming no command is the same kind of usage
	// error, so it keeps rgit's own convention rather than adopting git's
	// top-level dispatcher quirk.
	got := runRgit(t, newTempRepo(t))
	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.InvalidUsage)))
	qt.Assert(t, qt.Equals(got.Stdout, ""))
	qt.Assert(t, qt.StringContains(got.Stderr, "usage: rgit"))
}

func TestHelp_SubcommandExitsZeroAndDoesNotLeakPflag(t *testing.T) {
	t.Parallel()
	// Before: pflag's ContinueOnError returned pflag.ErrHelp from Parse,
	// which fell into the generic parse-failure branch and printed the
	// library's own internal error string -- "rgit: pflag: help requested"
	// -- to stderr at exit 129. errors.Is(err, pflag.ErrHelp) now routes
	// -h/--help to the subcommand's own help on stdout at exit 0 instead.
	//
	// Both subcommands are covered here rather than in two near-identical
	// tests: diff was the one that kept leaking after commit was fixed,
	// because each subcommand wires its own help text separately and
	// nothing structural stops one from being missed again.
	repo := newTempRepo(t)
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

func TestCommit_AmendWithNoMessageReusesHeadSubject(t *testing.T) {
	t.Parallel()
	// rgit never opens an editor (docs/USAGE.md: commit.template is
	// deliberately not honoured), so --amend with neither -m nor -F has
	// exactly one sensible meaning: `git commit --amend --no-edit`.
	repo := initRepoWithFile(t, "g.go", "package main\n\nfunc G() int { return 1 }\n")
	before := gitIn(t, repo, "log", "-1", "--format=%s")

	writeFile(t, repo, "g.go", "package main\n\nfunc G() int { return 2 }\n")
	got := runRgit(t, repo, "commit", "--amend", "g.go:G")

	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	after := gitIn(t, repo, "log", "-1", "--format=%s")
	qt.Assert(t, qt.Equals(after, before))
	qt.Assert(t, qt.StringContains(gitIn(t, repo, "cat-file", "-p", "HEAD:g.go"), "return 2"))
}

func TestCommit_NonAmendWithNoMessageStillRequiresOne(t *testing.T) {
	t.Parallel()
	// The message requirement is suppressed only for --amend; a plain
	// commit with neither -m nor -F is still exit 129.
	repo := newTempRepo(t)
	writeFile(t, repo, "g.go", "package main\n\nfunc G() {}\n")

	got := runRgit(t, repo, "commit", "g.go")

	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.InvalidUsage)))
	qt.Assert(t, qt.StringContains(got.Stderr, "commit requires a message"))
}

// --- Forwarded git flags: --fixup/--squash, --author/--date/--reset-author,
// --gpg-sign/--no-gpg-sign, the --porcelain and -q output modes, and a clearer
// --push-with-no-upstream message. Each is specified in docs/USAGE.md § Flags;
// what earns a test here is a flag rgit does more with than hand to git.

func TestCommit_FixupAndSquashGenerateAutosquashMessages(t *testing.T) {
	t.Parallel()
	repo := initRepoWithFile(t, "g.go", "package main\n\nfunc G() int { return 1 }\n")
	target := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))

	for i, tc := range []struct{ flag, wantPrefix string }{
		{"--fixup", "fixup! "},
		{"--squash", "squash! "},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			// Distinct content each iteration -- otherwise the second
			// subtest's write is a no-op against the first subtest's
			// already-committed content, and there is nothing to commit.
			writeFile(t, repo, "g.go", fmt.Sprintf("package main\n\nfunc G() int { return %d }\n", i+2))
			// Neither -m nor -F: the message requirement must not fire,
			// same as bare --amend -- git generates the subject itself.
			got := runRgit(t, repo, "commit", tc.flag+"="+target, "g.go")
			qt.Assert(t, qt.Equals(got.ExitCode, 0))
			qt.Assert(t, qt.Equals(gitIn(t, repo, "log", "-1", "--format=%s"), tc.wantPrefix+"init\n"))
		})
	}
}

func TestCommit_FixupWithMessageAppendsRatherThanConflicts(t *testing.T) {
	t.Parallel()
	// Verified against real git: --fixup plus -m is not the "-m and -F are
	// mutually exclusive" shape of conflict. git appends -m's text as an
	// extra body paragraph below the generated "fixup! ..." subject.
	repo := initRepoWithFile(t, "g.go", "package main\n\nfunc G() int { return 1 }\n")
	target := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))

	writeFile(t, repo, "g.go", "package main\n\nfunc G() int { return 2 }\n")
	got := runRgit(t, repo, "commit", "--fixup="+target, "-m", "UNIQUE_BODY_MARKER", "g.go")

	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	body := gitIn(t, repo, "log", "-1", "--format=%B")
	qt.Assert(t, qt.StringContains(body, "fixup! init"))
	qt.Assert(t, qt.StringContains(body, "UNIQUE_BODY_MARKER"))
}

func TestCommit_AuthorAndDateForwarded(t *testing.T) {
	t.Parallel()
	repo := newTempRepo(t)
	writeFile(t, repo, "g.go", "package main\n\nfunc G() {}\n")

	got := runRgit(t, repo, "commit",
		"--author", "Ada Lovelace <ada@example.com>",
		"--date", "2005-04-07T22:13:13",
		"-m", "feat(g): add G", "g.go")

	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.Equals(gitIn(t, repo, "log", "-1", "--format=%an <%ae>"), "Ada Lovelace <ada@example.com>\n"))
	qt.Assert(t, qt.Equals(gitIn(t, repo, "log", "-1", "--date=format:%Y-%m-%d", "--format=%ad"), "2005-04-07\n"))
}

func TestCommit_GPGSignFlagsForwarded(t *testing.T) {
	t.Parallel()
	// gpg.program pointed at a binary that always fails turns any signing
	// attempt into a deterministic, fast failure -- proof --gpg-sign (bare
	// or with a key id) reached git and triggered signing, with no real
	// GPG setup needed. --no-gpg-sign is checked the other way: it must
	// override commit.gpgsign=true and still succeed.
	repo := newTempRepo(t)
	gitIn(t, repo, "config", "gpg.program", "/bin/false")

	writeFile(t, repo, "a.go", "package main\n\nfunc A() {}\n")
	unsigned := runRgit(t, repo, "commit", "-m", "feat(a): add A", "a.go")
	qt.Assert(t, qt.Equals(unsigned.ExitCode, 0))

	writeFile(t, repo, "b.go", "package main\n\nfunc B() {}\n")
	bare := runRgit(t, repo, "commit", "--gpg-sign", "-m", "feat(b): add B", "b.go")
	qt.Assert(t, qt.Equals(bare.ExitCode, int(exitcode.GitFailure)))
	qt.Assert(t, qt.StringContains(bare.Stderr, "sign"))

	keyed := runRgit(t, repo, "commit", "--gpg-sign=DEADBEEF", "-m", "feat(c): add C", "a.go")
	qt.Assert(t, qt.Equals(keyed.ExitCode, int(exitcode.GitFailure)))

	gitIn(t, repo, "config", "commit.gpgsign", "true")
	writeFile(t, repo, "d.go", "package main\n\nfunc D() {}\n")
	noSign := runRgit(t, repo, "commit", "--no-gpg-sign", "-m", "feat(d): add D", "d.go")
	qt.Assert(t, qt.Equals(noSign.ExitCode, 0))
}

func TestCommit_PushWithNoUpstreamNamesTheFix(t *testing.T) {
	t.Parallel()
	// docs/USAGE.md / AGENTS.md's one invariant: rgit does not invent an
	// implicit `-u` (a push.default=current caller already gets a
	// successful push with no upstream at all, and pre-empting on that
	// basis would silently break them -- verified against real git). What
	// it adds on top of git's own failure is a named, concrete fix.
	repo := initRepoWithFile(t, "auth.go", authGoV1)
	remote := t.TempDir()
	gitIn(t, remote, "init", "-q", "--bare")
	gitIn(t, repo, "remote", "add", "origin", remote)
	branch := strings.TrimSpace(gitIn(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))

	writeFile(t, repo, "auth.go", authGoV2)
	got := runRgit(t, repo, "commit", "--push", "-m", "fix(auth): reject expired", "auth.go:ValidateToken")

	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.PushFailed)))
	qt.Assert(t, qt.StringContains(got.Stderr, "git push -u origin "+branch))
	// The commit itself still landed even though the push failed.
	qt.Assert(t, qt.StringContains(gitIn(t, repo, "cat-file", "-p", "HEAD:auth.go"), "len(tok)"))
}

func TestCommit_ResetAuthorForwarded(t *testing.T) {
	t.Parallel()
	// --author sets an identity the amend must then discard: with
	// --reset-author, git takes the author from the committer, so the
	// Ada identity written by the first commit must not survive.
	repo := newTempRepo(t)
	writeFile(t, repo, "g.go", "package main\n\nfunc G() int { return 1 }\n")
	first := runRgit(t, repo, "commit",
		"--author", "Ada Lovelace <ada@example.com>",
		"-m", "feat(g): add G", "g.go")
	qt.Assert(t, qt.Equals(first.ExitCode, 0))
	qt.Assert(t, qt.Equals(gitIn(t, repo, "log", "-1", "--format=%an"), "Ada Lovelace\n"))

	writeFile(t, repo, "g.go", "package main\n\nfunc G() int { return 2 }\n")
	got := runRgit(t, repo, "commit", "--amend", "--reset-author", "g.go:G")

	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	// newTempRepo's own committer identity, not Ada's.
	qt.Assert(t, qt.Equals(gitIn(t, repo, "log", "-1", "--format=%an"),
		gitIn(t, repo, "log", "-1", "--format=%cn")))
}

func TestCommit_PorcelainEmitsRecords(t *testing.T) {
	t.Parallel()
	// The machine-readable counterpart to the aligned listing, on both the
	// preview and the commit it previews -- and the records must agree,
	// which is the whole reason --dry-run's listing exists.
	repo := newTempRepo(t)
	writeFile(t, repo, "g.go", "package main\n\nfunc G() int { return 1 }\n")
	writeFile(t, repo, "notes.txt", "hello\n")

	dry := runRgit(t, repo, "commit", "--dry-run", "--porcelain",
		"-m", "feat(g): add G", "g.go:G", "notes.txt")
	qt.Assert(t, qt.Equals(dry.ExitCode, 0))
	// No human preamble: records are the entire stdout stream.
	qt.Assert(t, qt.Equals(strings.Contains(dry.Stdout, "dry run:"), false))
	// A pathspec target leaves the SYMBOL column empty; an anchor fills it.
	qt.Assert(t, qt.StringContains(dry.Stdout, "g.go\tG\t"))
	qt.Assert(t, qt.StringContains(dry.Stdout, "notes.txt\t\t"))
	for line := range strings.SplitSeq(strings.TrimRight(dry.Stdout, "\n"), "\n") {
		if got := len(strings.Split(line, "\t")); got != 4 {
			t.Errorf("record %q has %d fields; want 4", line, got)
		}
	}

	real := runRgit(t, repo, "commit", "--porcelain",
		"-m", "feat(g): add G", "g.go:G", "notes.txt")
	qt.Assert(t, qt.Equals(real.ExitCode, 0))
	qt.Assert(t, qt.Equals(real.Stdout, dry.Stdout))
	// git's own summary is replaced, not merely appended to, exactly as
	// git commit --porcelain replaces it.
	qt.Assert(t, qt.Equals(strings.Contains(real.Stdout, "file changed"), false))
}

func TestCommit_QuietSuppressesStdoutOnly(t *testing.T) {
	t.Parallel()
	repo := newTempRepo(t)
	writeFile(t, repo, "g.go", "package main\n\nfunc G() int { return 1 }\n")
	// An unchanged second target still has to warn on stderr: -q is git's
	// own "suppress the summary", not "suppress the diagnostics".
	runRgit(t, repo, "commit", "-m", "feat(h): add H", "g.go")
	writeFile(t, repo, "g.go", "package main\n\nfunc G() int { return 2 }\n")
	writeFile(t, repo, "h.go", "package main\n\nfunc H() {}\n")
	runRgit(t, repo, "commit", "-m", "feat(h): add H", "h.go")

	got := runRgit(t, repo, "commit", "-q", "-m", "fix(g): bump", "g.go:G", "h.go:H")

	qt.Assert(t, qt.Equals(got.ExitCode, 0))
	qt.Assert(t, qt.Equals(got.Stdout, ""))
	qt.Assert(t, qt.StringContains(got.Stderr, "has no uncommitted changes"))
	qt.Assert(t, qt.StringContains(gitIn(t, repo, "cat-file", "-p", "HEAD:g.go"), "return 2"))
}

func TestCommit_PorcelainAndQuietConflict(t *testing.T) {
	t.Parallel()
	repo := newTempRepo(t)
	writeFile(t, repo, "g.go", "package main\n\nfunc G() {}\n")

	got := runRgit(t, repo, "commit", "--porcelain", "-q", "-m", "feat(g): add G", "g.go")

	qt.Assert(t, qt.Equals(got.ExitCode, int(exitcode.InvalidUsage)))
	qt.Assert(t, qt.StringContains(got.Stderr, "mutually exclusive"))
}
