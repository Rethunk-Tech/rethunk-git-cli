// End-to-end coverage for rgit, per CONTRIBUTING.md's three-file test
// budget. This slice is Phase 1's: argument precedence and usage errors
// only. The commit happy path (init repo -> edit symbol -> rgit diff ->
// rgit commit -> verify HEAD) lands in Phase 5, once staging is real.
//
// Every case execs the actual built binary against a real temporary git
// repository — no gitx mocking — so a regression in pflag's interspersed
// parsing, or in cli.ClassifyArgs's rule order, shows up exactly as a
// user would see it.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
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
	cmd := exec.Command("go", "build", "-o", bin, ".")
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

// wantParsed asserts that classification succeeded and the command
// reached the (unimplemented) execution step — i.e. no usage error, no
// classification error. Phase 1 has no real commit/diff execution yet,
// so "reached execution" is the strongest observable signal that
// argument precedence resolved every target correctly.
func wantParsed(t *testing.T, got rgitResult) {
	t.Helper()
	qt.Assert(t, qt.Equals(got.ExitCode, int(phase1Unimplemented)))
}

func TestCommit_InterspersedFlagAfterPositional(t *testing.T) {
	// Pinned defect: stdlib flag and ff/ffcli stop parsing at the first
	// positional, so this exact argv shape would silently yield zero
	// messages and two targets ("auth.go:Foo", "msg"), failing with
	// exit 129 ("commit requires a message") instead of reaching
	// execution. pflag's interspersed parsing must read one message and
	// one target.
	repo := newTempRepo(t)
	writeFile(t, repo, "auth.go", "package main\n")

	got := runRgit(t, repo, "commit", "auth.go:Foo", "-m", "msg")

	wantParsed(t, got)
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

	wantParsed(t, got)
}

func TestCommit_DoubleDashForcesPathspec(t *testing.T) {
	// Rule 1: everything after "--" is a pathspec, unconditionally — even
	// a token shaped like FILE:NAME for a file that does not exist, which
	// would otherwise fail rule 6 with an unresolvable-argument error.
	// -m must come before "--", since pflag stops flag parsing there too.
	repo := newTempRepo(t)

	got := runRgit(t, repo, "commit", "-m", "chore: force pathspec", "--", "missing.go:NotASymbol")

	wantParsed(t, got)
}

func TestCommit_LeadingColonPathspecMagicPassesThrough(t *testing.T) {
	// Rule 2: all git pathspec magic is leading-colon, so this is claimed
	// immediately, with no existence check at all.
	repo := newTempRepo(t)

	got := runRgit(t, repo, "commit", "-m", "chore: exclude docs", ":(exclude)docs/*")

	wantParsed(t, got)
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
