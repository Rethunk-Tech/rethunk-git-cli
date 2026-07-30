package synth

import (
	"context"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// stagedBlob reads path's staged (index) content, the package-local twin of
// cmd/rgit/index_test.go's own indexBlob -- kept here too since this file's
// cases exercise classify/escalateToContainer directly through Stage rather
// than the built binary, and duplicating one three-line helper is cheaper
// than reaching across the module for it.
func stagedBlob(t *testing.T, repo *gitx.Repo, path string) string {
	t.Helper()
	content, exists, err := repo.CatFile(context.Background(), "", path)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(exists))
	return string(content)
}

// TestClassify_DefersCrossCheckToOneBatchPerFile guards m20's own fix:
// classify used to dial a live language server once per anchor
// (fp.crossCheck, since deleted), so a commit naming N symbols in one file
// paid N documentSymbol round trips where internal/diff's own crossCheckFile
// already batches identically-shaped work into one. classify no longer
// dials at all -- it only queues each worktree resolution onto
// fp.pendingCrossCheck (deferCrossCheck) -- so two anchors in the same file
// must land in the identical, shared queue, in resolution order, ready for
// crossCheckPending to drain in a single CrossCheckExtents call. No live or
// mock server is needed to prove this: the batching is decided entirely by
// what classify queues, before any dial ever happens.
func TestClassify_DefersCrossCheckToOneBatchPerFile(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	gittest.Write(t, dir, "a.go", "package p\n\nfunc A() int { return 1 }\n\nfunc B() int { return 2 }\n")
	gittest.Commit(t, dir, "chore: fixture")
	gittest.Write(t, dir, "a.go", "package p\n\nfunc A() int { return 11 }\n\nfunc B() int { return 22 }\n")

	fp, err := openFilePlan(context.Background(), repo, dir, "a.go")
	qt.Assert(t, qt.IsNil(err))
	defer fp.close()

	qt.Assert(t, qt.HasLen(fp.pendingCrossCheck, 0))

	_, _, err = fp.classify("A")
	qt.Assert(t, qt.IsNil(err))
	_, _, err = fp.classify("B")
	qt.Assert(t, qt.IsNil(err))

	qt.Assert(t, qt.HasLen(fp.pendingCrossCheck, 2))
	var anchors []string
	for _, res := range fp.pendingCrossCheck {
		anchors = append(anchors, res.Anchor)
	}
	qt.Assert(t, qt.DeepEquals(anchors, []string{"A", "B"}))
}

// TestEscalateToContainer_AmbiguousContainerPropagates pins the fix for a
// silent-fallback bug: escalateToContainer used to treat every
// *resolve.ResolveError alike when resolving a new member's own container,
// including exitcode.AnchorAmbiguous, so a member whose container name
// collides -- two duplicate TOML "[[servers]]" headers is the measured
// shape -- proceeded to an ordinary top-level insertion instead of the
// exit-4 ambiguity a direct "servers" anchor resolve already gives.
// Spiked directly against the pre-fix code first (classify_spike_test.go,
// since removed): it staged "port = 1" as a bare top-level entry between
// the two tables with no error at all, silently producing malformed TOML.
func TestEscalateToContainer_AmbiguousContainerPropagates(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	head := "[[servers]]\nhost = \"a\"\n\n[[servers]]\nhost = \"b\"\n"
	gittest.Write(t, dir, "conf.toml", head)
	gittest.Commit(t, dir, "chore: initial conf.toml")

	// "port" is added only under the first table, so "servers.port" itself
	// resolves uniquely -- it is escalateToContainer's own probe of the
	// container name "servers" that is ambiguous, matching both duplicate
	// headers.
	work := "[[servers]]\nhost = \"a\"\nport = 1\n\n[[servers]]\nhost = \"b\"\n"
	gittest.Write(t, dir, "conf.toml", work)

	err := Stage(context.Background(), repo, dir, []Target{AnchorTarget("conf.toml", "servers.port")})

	var rerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &rerr))
	qt.Assert(t, qt.Equals(rerr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.Equals(rerr.Anchor, "servers"))
}

// TestEscalateToContainer_HTMLNestedInsertIgnoresTagCoincidence pins the
// HTML half of the same fix. lang_html.go sets Declaration.Container to the
// element's OWN tag name (used only to build its "tag#id" qualified anchor),
// not to any real enclosing ancestor, so escalateToContainer's generic
// "resolve Container as a container name" logic can match a completely
// unrelated, coincidentally-tag-named id elsewhere in the document.
//
// Here the brand new "<em id=\"tagline\">" sits inside a brand new outer
// wrapper "<div id=\"em\">" -- chosen so the wrapper's own id text ("em")
// collides with the member's tag name, the coincidence escalateToContainer
// used to key off. Spiked against the pre-fix code first
// (classify_spike_test.go, since removed): it resolved "em" to that
// unrelated wrapper, found the wrapper absent from HEAD too, and staged the
// wrapper's ENTIRE extent -- section#content's original bytes duplicated
// inside it, plus the untouched original div#app appended again after --
// in place of the single named member. The fix skips container escalation
// for HTML outright, so only "em#tagline" itself is spliced in, at its
// ordinary nearest-sibling position after section#content.
func TestEscalateToContainer_HTMLNestedInsertIgnoresTagCoincidence(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	head := "<div id=\"app\">\n  <section id=\"content\">v1</section>\n</div>\n"
	gittest.Write(t, dir, "index.html", head)
	gittest.Commit(t, dir, "chore: initial index.html")

	work := "<div id=\"em\">\n<div id=\"app\">\n  <section id=\"content\">v1</section>\n  <em id=\"tagline\">Hi</em>\n</div>\n</div>\n"
	gittest.Write(t, dir, "index.html", work)

	err := Stage(context.Background(), repo, dir, []Target{AnchorTarget("index.html", "em#tagline")})
	qt.Assert(t, qt.IsNil(err))

	want := "<div id=\"app\">\n  <section id=\"content\">v1</section>\n\n  <em id=\"tagline\">Hi</em>\n\n</div>\n"
	qt.Assert(t, qt.Equals(stagedBlob(t, repo, "index.html"), want))
	qt.Assert(t, qt.Equals(strings.Count(want, "<div id=\"app\">"), 1))
}
