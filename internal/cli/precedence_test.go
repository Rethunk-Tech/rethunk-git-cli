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
func newClassifyRepo(t *testing.T) (root string, checker *GitPathChecker, revs GitRevisionResolver, ctx context.Context) {
	t.Helper()
	root, repo := gittest.New(t.Context(), t)

	gittest.Write(t, root, "a.go", "package a\n\nfunc A() {}\n")
	gittest.Write(t, root, "src/notes:draft.md", "# draft\n")
	gittest.Write(t, root, "gone.go", "package a\n\nfunc Gone() {}\n")
	gittest.Commit(t.Context(), t, root, "chore: fixtures")
	if err := os.Remove(filepath.Join(root, "gone.go")); err != nil {
		t.Fatal(err)
	}

	return root, &GitPathChecker{Root: root, Repo: repo}, GitRevisionResolver{Repo: repo}, context.Background()
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

// TestClassifyArgs_Rule5NameMayContainColons pins both halves of rule 5's
// scan. Starting at the last colon is what keeps a path that itself carries
// one splitting as it always did; continuing leftwards is what lets a name
// carry one, which CSS anchors do constantly ("a:hover", "*::before",
// "@media (max-width: 600px)"). Before the scan, rgit refused anchors its own
// `symbols` had printed -- 462 of 3054 across 36 of 59 surveyed stylesheets.
func TestClassifyArgs_Rule5NameMayContainColons(t *testing.T) {
	t.Parallel()
	_, checker, revs, ctx := newClassifyRepo(t)

	for _, tc := range []struct {
		name, arg, file, sym string
	}{
		{"name carries colons", "a.go:a:hover", "a.go", "a:hover"},
		{"name carries a doubled colon", "a.go:*::before", "a.go", "*::before"},
		{"name carries a colon and spaces", "a.go:@media (max-width: 600px)", "a.go", "@media (max-width: 600px)"},
		// The path itself contains a colon, and the last-colon split is
		// still tried first, so this resolves exactly as it did before the
		// scan existed rather than being cut at the path's own colon.
		{"path carries a colon", "src/notes:draft.md:Heading", "src/notes:draft.md", "Heading"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ClassifyArgs(ctx, []string{tc.arg}, true, checker, revs)
			qt.Assert(t, qt.IsNil(err))
			qt.Assert(t, qt.DeepEquals(got, []Classification{{
				Kind:   KindAnchor,
				Anchor: Anchor{File: tc.file, Name: tc.sym},
			}}))
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
		// M5: "or range" dropped -- a positional A..B/A...B range is peeled
		// out by diff.ExtractRangeToken before ClassifyArgs ever sees it
		// (internal/diff/scope.go), so rule 3 never actually tries one via
		// rev-parse --verify; saying it did was false.
		"revision or rev:path (git rev-parse --verify)",
		"existing path (worktree, index, or HEAD)",
		"symbol anchor (existing path + name after a ':')",
	}))
	// Error() is what a caller actually reads (internal/app relays it
	// verbatim behind "rgit: "), so its exact wording is pinned here too --
	// "rules considered", not "tried", since rule 2 above is ruled out by a
	// prefix check rather than a real attempt at resolving the token.
	qt.Assert(t, qt.Equals(uerr.Error(),
		`cannot classify "nosuch.go:Nope": rules considered: pathspec magic (leading ':'); `+
			`revision or rev:path (git rev-parse --verify); existing path (worktree, index, or HEAD); `+
			`symbol anchor (existing path + name after a ':')`))

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

func TestGitPathChecker_IndexOnlyPathExists(t *testing.T) {
	t.Parallel()
	root, repo := gittest.New(t.Context(), t)
	gittest.Write(t, root, "new.go", "package p\n\nfunc New() {}\n")
	gittest.Git(t.Context(), t, root, "add", "new.go")
	if err := os.Remove(filepath.Join(root, "new.go")); err != nil {
		t.Fatal(err)
	}

	checker := &GitPathChecker{Root: root, Repo: repo}
	exists, err := checker.ExistsInWorktreeOrHEAD(context.Background(), "new.go")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(exists))
}

func TestClassifyArgs_IndexOnlyPathIsAnAnchor(t *testing.T) {
	t.Parallel()
	root, repo := gittest.New(t.Context(), t)
	gittest.Write(t, root, "new.go", "package p\n\nfunc New() {}\n")
	gittest.Git(t.Context(), t, root, "add", "new.go")
	if err := os.Remove(filepath.Join(root, "new.go")); err != nil {
		t.Fatal(err)
	}

	checker := &GitPathChecker{Root: root, Repo: repo}
	revs := GitRevisionResolver{Repo: repo}
	got, err := ClassifyArgs(
		context.Background(),
		[]string{"new.go:New"},
		false,
		checker,
		revs,
	)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(got, []Classification{
		{Kind: KindAnchor, Anchor: Anchor{File: "new.go", Name: "New"}},
	}))
}

func TestGitPathChecker_IntentToAddIgnoredPathExists(t *testing.T) {
	t.Parallel()
	root, repo := gittest.New(t.Context(), t)
	gittest.Write(t, root, ".gitignore", "skip-me.go\n")
	gittest.Write(t, root, "skip-me.go", "package p\n")
	gittest.Git(t.Context(), t, root, "add", ".gitignore")
	gittest.Commit(t.Context(), t, root, "chore: add ignore rule")
	gittest.Git(t.Context(), t, root, "add", "-f", "-N", "skip-me.go")

	checker := &GitPathChecker{Root: root, Repo: repo}
	exists, err := checker.ExistsInWorktreeOrHEAD(context.Background(), "skip-me.go")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(exists))
}

func TestGitPathChecker_IgnoreCaseIndexOnlyPathExists(t *testing.T) {
	t.Parallel()
	root, repo := gittest.New(t.Context(), t)
	gittest.Git(t.Context(), t, root, "config", "core.ignorecase", "true")
	gittest.Write(t, root, "Foo.go", "package p\n\nfunc Foo() {}\n")
	gittest.Git(t.Context(), t, root, "add", "Foo.go")
	if err := os.Remove(filepath.Join(root, "Foo.go")); err != nil {
		t.Fatal(err)
	}

	checker := &GitPathChecker{Root: root, Repo: repo}
	exists, err := checker.ExistsInWorktreeOrHEAD(context.Background(), "foo.go")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(exists))
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
