package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

// newClassifyRepo builds a real repository for the precedence table to be
// tested against, rather than a PathChecker/RevisionResolver stand-in. The
// rules are defined in terms of what git itself answers -- "resolves via
// rev-parse --verify", "exists in the worktree or at HEAD" -- so a double
// here would be asserting this package's own assumptions about git back at
// itself, which is precisely the drift CONTRIBUTING warns about.
//
// Layout, chosen so every rule has something to claim:
//
//	a.go                 committed, exists at HEAD and in the worktree
//	src/notes:draft.md   a legal path that itself contains a colon
//	gone.go              committed, then deleted from the worktree
func newClassifyRepo(t *testing.T) (root string, checker GitPathChecker, revs GitRevisionResolver, ctx context.Context) {
	t.Helper()
	root, repo := gittest.New(t)

	gittest.Write(t, root, "a.go", "package a\n\nfunc A() {}\n")
	gittest.Write(t, root, "src/notes:draft.md", "# draft\n")
	gittest.Write(t, root, "gone.go", "package a\n\nfunc Gone() {}\n")
	gittest.Commit(t, root, "chore: fixtures")
	if err := os.Remove(filepath.Join(root, "gone.go")); err != nil {
		t.Fatal(err)
	}

	return root, GitPathChecker{Root: root, Repo: repo}, GitRevisionResolver{Repo: repo}, context.Background()
}

// TestClassifyArgs_PrecedenceTable walks docs/USAGE.md § Argument shape's
// six rules in one pass. Each case names the rule it pins and why that rule
// has to win over the ones below it -- the ordering is the whole design,
// since several tokens satisfy more than one test.
func TestClassifyArgs_PrecedenceTable(t *testing.T) {
	t.Parallel()
	_, checker, revs, ctx := newClassifyRepo(t)

	for _, tc := range []struct {
		name           string
		args           []string
		allowRevisions bool
		want           []Classification
	}{{
		name: "rule 1: everything after -- is a pathspec, even an anchor shape",
		args: []string{"--", "a.go:A", "nosuch.go"},
		want: []Classification{
			{Kind: KindPathspec, Pathspec: "a.go:A"},
			{Kind: KindPathspec, Pathspec: "nosuch.go"},
		},
	}, {
		name: "rule 2: leading colon is magic, passed through verbatim",
		args: []string{":(exclude)docs/*", ":/src"},
		want: []Classification{
			{Kind: KindPathspec, Pathspec: ":(exclude)docs/*"},
			{Kind: KindPathspec, Pathspec: ":/src"},
		},
	}, {
		name:           "rule 3: a revision resolves before the path test",
		args:           []string{"HEAD"},
		allowRevisions: true,
		want:           []Classification{{Kind: KindRevision, Revision: "HEAD"}},
	}, {
		name:           "rule 3: rev:path becomes a blob reference, not an anchor",
		args:           []string{"HEAD:a.go"},
		allowRevisions: true,
		want:           []Classification{{Kind: KindRevPath, RevPath: RevPath{Rev: "HEAD", Path: "a.go"}}},
	}, {
		name: "rule 4: an existing path beats the colon split",
		args: []string{"src/notes:draft.md"},
		want: []Classification{{Kind: KindPathspec, Pathspec: "src/notes:draft.md"}},
	}, {
		name: "rule 4: a path present only at HEAD still claims the token",
		args: []string{"gone.go"},
		want: []Classification{{Kind: KindPathspec, Pathspec: "gone.go"}},
	}, {
		name: "rule 5: split at the LAST colon into an existing path plus a name",
		args: []string{"a.go:A"},
		want: []Classification{{Kind: KindAnchor, Anchor: Anchor{File: "a.go", Name: "A"}}},
	}, {
		name: "rule 5 reaches a file that exists only at HEAD",
		args: []string{"gone.go:Gone"},
		want: []Classification{{Kind: KindAnchor, Anchor: Anchor{File: "gone.go", Name: "Gone"}}},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ClassifyArgs(ctx, tc.args, tc.allowRevisions, checker, revs)
			qt.Assert(t, qt.IsNil(err))
			qt.Assert(t, qt.DeepEquals(got, tc.want))
		})
	}
}

// TestClassifyArgs_Rule6ListsWhatItTried is the error path: rule 6 exists to
// say why a token was rejected, so the message has to name each rule that
// was actually applied -- and only those, since rule 3 is diff-only.
func TestClassifyArgs_Rule6ListsWhatItTried(t *testing.T) {
	t.Parallel()
	_, checker, revs, ctx := newClassifyRepo(t)

	_, err := ClassifyArgs(ctx, []string{"nosuch.go:Nope"}, true, checker, revs)

	var uerr *UnresolvedArgError
	qt.Assert(t, qt.IsTrue(errors.As(err, &uerr)))
	qt.Assert(t, qt.Equals(uerr.Arg, "nosuch.go:Nope"))
	qt.Assert(t, qt.DeepEquals(uerr.Tried, []string{
		"pathspec magic (leading ':')",
		"revision, rev:path, or range (git rev-parse --verify)",
		"existing path (worktree or HEAD)",
		"symbol anchor (existing path + name after last ':')",
	}))

	// Without revisions the rule-3 line must be absent rather than merely
	// unmatched: a commit invocation never consulted rev-parse at all.
	_, err = ClassifyArgs(ctx, []string{"nosuch.go:Nope"}, false, checker, revs)
	qt.Assert(t, qt.IsTrue(errors.As(err, &uerr)))
	qt.Assert(t, qt.Equals(len(uerr.Tried), 3))

	// The same token, classified both ways, is what makes rule 3 diff-only
	// observable: `rgit diff HEAD:a.go` is a blob reference, while
	// `rgit commit HEAD:a.go` cannot be one -- rule 5 splits it at the last
	// colon and finds no path named "HEAD" to anchor against, so it is
	// refused rather than quietly staging something.
	_, err = ClassifyArgs(ctx, []string{"HEAD:a.go"}, false, checker, revs)
	qt.Assert(t, qt.IsTrue(errors.As(err, &uerr)))
	qt.Assert(t, qt.Equals(uerr.Arg, "HEAD:a.go"))
}

// TestPrefixPath covers the subdirectory rule docs/USAGE.md calls out as
// git's own: a path is relative to where you stand, except for the magic
// git already defines as root-relative.
func TestPrefixPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ prefix, path, want string }{
		{"", "a.go", "a.go"},
		{"pkg/deep/", "a.go", "pkg/deep/a.go"},
		{"pkg/deep/", ":/src", ":/src"},
		{"pkg/deep/", ":(top)a.go", ":(top)a.go"},
	} {
		if got := PrefixPath(tc.prefix, tc.path); got != tc.want {
			t.Errorf("PrefixPath(%q, %q) = %q; want %q", tc.prefix, tc.path, got, tc.want)
		}
	}
}
