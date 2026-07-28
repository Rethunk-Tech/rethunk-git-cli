// End-to-end coverage for rgit, per CONTRIBUTING.md's three-file test
// budget. Covers argument precedence and usage errors (Phase 1), rgit diff
// rendering (Phase 4), and rgit commit's real execution -- staging through
// git commit, hooks, and the invariants AGENTS.md pins (Phase 5).
//
// Every case execs the actual built binary against a real temporary git
// repository — no gitx mocking — so a regression in pflag's interspersed
// parsing, cli.ClassifyArgs's rule order, or internal/synth's staging
// shows up exactly as a user would see it.
package main

import (
	"bytes"
	"errors"
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

var rgitBin string

func TestMain(m *testing.M) {
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

func buildRgit() (bin string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "rgit-e2e-bin-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { os.RemoveAll(dir) }

	bin = filepath.Join(dir, "rgit")
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
	// Rule 2: all git pathspec magic is leading-colon, so this is claimed
	// immediately, with no existence check at all -- paired here with a
	// real target so the commit has something to actually stage.
	repo := newTempRepo(t)
	writeFile(t, repo, "keep.go", "package main\n\nfunc Keep() {}\n")

	got := runRgit(t, repo, "commit", "-m", "chore: exclude docs", "keep.go", ":(exclude)docs/*")

	qt.Assert(t, qt.Equals(got.ExitCode, 0))
}

func TestCommit_UnresolvableArgumentListsTriedInterpretations(t *testing.T) {
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

// --- Phase 4: rgit diff execution -------------------------------------
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
	for _, line := range strings.Split(output, "\n") {
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
	// A fresh `git init` has no HEAD, so the default scope's `git diff
	// HEAD` failed outright with exit 128 -- on the one repository state
	// where "what can I commit?" gets asked most, and the exact command
	// docs/INSTALL.md § Verify hands a new user. rgit commit already
	// worked here, so diff could not describe a commit rgit would make.
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

func TestDiff_UnstagedScopeExcludesStaged(t *testing.T) {
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

// --- Phase 5: rgit commit execution ------------------------------------

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
