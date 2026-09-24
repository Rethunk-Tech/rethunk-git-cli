package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

// TestStage_RoundTripsWorktreeForEveryDeclaration is rgit's own promise,
// tested per declaration across every wired grammar: change exactly one
// symbol, stage that one anchor, and the staged blob must equal the whole
// worktree file byte for byte. Nothing else in the file changed, so anything
// but equality means synthesis rebuilt bytes it was supposed to carry over.
//
// This is the write-side counterpart to `make xcheck`, and it reaches what
// the identity-splice property in internal/synth cannot: HEAD-versus-worktree
// classification, and the separator reproduction (container members sitting
// flush, PEP 8's double blank line) whose byte-identity requirements
// internal/synth's own comments spell out.
//
// The perturbation is a single space inserted before a newline inside the
// declaration -- the one edit that is a real byte change in all ten grammars
// while changing no token, so the anchor still resolves and the file still
// parses. A declaration with no interior newline is skipped, which is why the
// test asserts a floor on how many it actually exercised.
func TestStage_RoundTripsWorktreeForEveryDeclaration(t *testing.T) {
	requireBinary(t)

	dir := filepath.Join("..", "..", "internal", "resolve", "testdata", "xcheck")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}

	staged := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}

		// --porcelain so an anchor carrying anything a line format could
		// not survive still arrives whole.
		listRepo := initRepoWithFile(t, name, string(src))
		list := runRgit(t, listRepo, "symbols", "--for-commit", "--porcelain", name)
		if list.ExitCode != 0 {
			t.Errorf("symbols %s: exit %d: %s", name, list.ExitCode, list.Stderr)
			continue
		}
		anchors := strings.SplitSeq(strings.TrimSuffix(list.Stdout, "\x00"), "\x00")

		for anchor := range anchors {
			if anchor == "" {
				continue
			}
			worktree, ok := perturbDeclaration(t, listRepo, name, src, anchor)
			if !ok {
				continue
			}

			repo := initRepoWithFile(t, name, string(src))
			path := filepath.Join(repo, name)
			if err := os.WriteFile(path, worktree, 0o600); err != nil {
				t.Fatalf("write worktree %s: %v", name, err)
			}

			res := runRgit(t, repo, "commit", "-m", "test: perturb one symbol", name+":"+anchor)
			if res.ExitCode != 0 {
				t.Errorf("%s:%s commit exit %d: %s", name, anchor, res.ExitCode, res.Stderr)
				continue
			}
			got := gittest.Git(t, repo, "show", "HEAD:"+name)
			if got != string(worktree) {
				t.Errorf("%s:%s staged blob is not the worktree file\nwant %d bytes, got %d",
					name, anchor, len(worktree), len(got))
			}
			staged++
		}
	}

	// A floor, not an exact count: the corpus is meant to grow. Zero would
	// mean the perturbation stopped applying and the test silently checked
	// nothing, which is the failure mode a round-trip like this has.
	if staged < 20 {
		t.Fatalf("only %d declarations round-tripped; the corpus or the perturbation stopped exercising this", staged)
	}
	t.Logf("round-tripped %d declarations", staged)
}

// perturbDeclaration inserts one space before a newline inside anchor's own
// extent, returning the modified file. ok is false when the declaration has
// no interior newline to use.
func perturbDeclaration(t *testing.T, repo, name string, src []byte, anchor string) ([]byte, bool) {
	t.Helper()
	res := runRgit(t, repo, "symbols", "--with-lines", "--porcelain", name)
	if res.ExitCode != 0 {
		return nil, false
	}
	var start, end int
	for rec := range strings.SplitSeq(strings.TrimSuffix(res.Stdout, "\x00"), "\x00") {
		lineRange, sym, found := strings.Cut(rec, "\t")
		if !found || sym != anchor {
			continue
		}
		s, e, ok := strings.Cut(lineRange, ",")
		if !ok {
			return nil, false
		}
		start, end = atoi(s), atoi(e)
	}
	if start == 0 || end <= start {
		return nil, false
	}

	// Line `start` is the declaration's first line; inserting before the
	// newline that ends it keeps the edit inside the extent.
	offset := 0
	for line := 1; line < start; line++ {
		nl := bytes.IndexByte(src[offset:], '\n')
		if nl < 0 {
			return nil, false
		}
		offset += nl + 1
	}
	nl := bytes.IndexByte(src[offset:], '\n')
	if nl < 0 {
		return nil, false
	}
	cut := offset + nl

	out := make([]byte, 0, len(src)+1)
	out = append(out, src[:cut]...)
	out = append(out, ' ')
	out = append(out, src[cut:]...)
	return out, true
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
