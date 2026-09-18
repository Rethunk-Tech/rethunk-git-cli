// Resolver coverage, per CONTRIBUTING.md's three-file test budget. Byte
// offsets below reflect real tree-sitter output against each fixture, not
// grammar docs.
package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsptest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

func resolverGoLang(t *testing.T) resolve.Language {
	t.Helper()
	lang, ok := resolve.ForExtension(".go")
	if !ok {
		t.Fatal("resolve: no adapter registered for .go")
	}
	return lang
}

func mustResolve(t *testing.T, src []byte, anchor string) *resolve.Resolution {
	t.Helper()
	res, err := resolve.Resolve(resolverGoLang(t), src, anchor)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", anchor, err)
	}
	return res
}

// mustResolveExt is the cross-language form: it picks the adapter by file
// extension, exercising registration as well as resolution.
func mustResolveExt(t *testing.T, ext string, src []byte, anchor string) string {
	t.Helper()
	lang, ok := resolve.ForExtension(ext)
	if !ok {
		t.Fatalf("resolve: no adapter registered for %s", ext)
	}
	res, err := resolve.Resolve(lang, src, anchor)
	if err != nil {
		t.Fatalf("Resolve(%s, %q): %v", ext, anchor, err)
	}
	return string(src[res.Extent.Start:res.Extent.End])
}

func TestResolve_UnsupportedLanguage(t *testing.T) {
	t.Parallel()
	// A symbol anchor on a file whose language has no grammar is the
	// caller's exit 9 (docs/ANCHORS.md § Language support); the resolver's
	// contribution is just reporting the extension unclaimed.
	_, ok := resolve.ForExtension(".rb")
	qt.Assert(t, qt.IsFalse(ok))

	// internal/diff/run.go's validateSym constructs exactly this shape --
	// Code set, no Candidates -- for the identical failure reached through
	// `rgit diff --sym`. Error() must actually say what docs/CODES.md's
	// exit-9 row promises ("Unsupported / deferred language for a symbol
	// anchor"), not silently fall through to the same "unresolved" label
	// exit 3 (AnchorUnresolvable) uses -- the code is right, the message
	// must not contradict it.
	err := &resolve.ResolveError{Code: exitcode.UnsupportedLanguage, Anchor: "main"}
	qt.Assert(t, qt.Equals(err.Error(), `resolve: "main": unsupported language`))
}

// --- Language-server cross-check -------------------------------------
//
// Coverage here is split by what can prove it: the union-shape decode and
// the anchor-normalization/comparison logic run against an in-process mock
// server, since a mock cannot catch a change in real language-server range
// semantics but can exercise every line of rgit's own decode and compare
// code cheaply and deterministically. The one thing only a real server can
// prove -- that gopls's actual reported ranges still match tree-sitter's
// normalized extent -- gets exactly one live case, skipped cleanly when
// gopls is absent or -short is set (CONTRIBUTING.md § Tests).

func TestLSP_DocumentSymbolsDecodesBothUnionShapes(t *testing.T) {
	t.Parallel()
	// go.lsp.dev/protocol's DocumentSymbolResult is a sealed union over
	// DocumentSymbolSlice (a tree, via Children -- gopls's hierarchical
	// mode) and SymbolInformationSlice (flat, with a Location and an
	// optional containerName). AGENTS.md: a client that assumes one shape
	// decodes the other wrongly, so both are exercised here against a real
	// (if hand-framed) wire exchange, not a stubbed union value.
	cases := []struct {
		name       string
		resultJSON string
		want       []lsp.Symbol
	}{
		{
			name: "DocumentSymbolSlice (tree, hierarchical)",
			resultJSON: `[{"name":"A","kind":6,"range":{"start":{"line":1,"character":0},"end":{"line":5,"character":1}},` +
				`"selectionRange":{"start":{"line":1,"character":6},"end":{"line":1,"character":7}},` +
				`"children":[{"name":"Get","kind":6,"range":{"start":{"line":3,"character":1},"end":{"line":3,"character":20}},` +
				`"selectionRange":{"start":{"line":3,"character":1},"end":{"line":3,"character":4}}}]}]`,
			want: []lsp.Symbol{
				{Name: "A", Container: "", StartLine: 1, EndLine: 5},
				{Name: "Get", Container: "A", StartLine: 3, EndLine: 3},
			},
		},
		{
			name: "SymbolInformationSlice (flat, with containerName)",
			resultJSON: `[{"name":"Get","kind":6,"containerName":"A",` +
				`"location":{"uri":"file:///tmp/a.go","range":{"start":{"line":3,"character":1},"end":{"line":3,"character":20}}}}]`,
			want: []lsp.Symbol{
				{Name: "Get", Container: "A", StartLine: 3, EndLine: 3},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serverConn, clientConn := net.Pipe()
			errCh := make(chan error, 1)
			go func() { errCh <- lsptest.ServeMockLSP(serverConn, tc.resultJSON, lsptest.MockServerHooks{}) }()

			client, err := lsp.NewClient(context.Background(), clientConn, t.TempDir())
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			defer func() { _ = client.Close() }()

			got, err := client.DocumentSymbols(context.Background(), "/tmp/a.go", []byte("package p\n"))
			if err != nil {
				t.Fatalf("DocumentSymbols: %v", err)
			}
			if srvErr := <-errCh; srvErr != nil {
				t.Fatalf("mock server: %v", srvErr)
			}
			qt.Assert(t, qt.DeepEquals(got, tc.want))
		})
	}
}

func TestResolve_CrossCheckMatchAndCompare(t *testing.T) {
	t.Parallel()
	src := []byte(`package p

// ValidateToken checks the JWT.
func ValidateToken(t string) error {
	return nil
}
`)
	res := mustResolve(t, src, "ValidateToken")

	t.Run("matching range confirms clean", func(t *testing.T) {
		found, err := resolve.MatchAndCompare(src, res, []lsp.Symbol{
			{Name: "ValidateToken", StartLine: 3, EndLine: 5},
		})
		qt.Assert(t, qt.IsTrue(found))
		qt.Assert(t, qt.IsNil(err))
	})

	t.Run("disagreement is exit 6 with both ranges attached", func(t *testing.T) {
		found, err := resolve.MatchAndCompare(src, res, []lsp.Symbol{
			{Name: "ValidateToken", StartLine: 3, EndLine: 6},
		})
		qt.Assert(t, qt.IsTrue(found))
		qt.Assert(t, qt.IsNotNil(err))
		var rerr *resolve.ResolveError
		qt.Assert(t, qt.ErrorAs(err, &rerr))
		qt.Assert(t, qt.Equals(rerr.Code, exitcode.ExtentMismatch))
		qt.Assert(t, qt.Equals(rerr.TreeSitterRange, "L4..L6"))
		qt.Assert(t, qt.Equals(rerr.LSPRange, "L4..L7"))
	})

	t.Run("server outline not naming the anchor degrades, is not an error", func(t *testing.T) {
		found, err := resolve.MatchAndCompare(src, res, nil)
		qt.Assert(t, qt.IsFalse(found))
		qt.Assert(t, qt.IsNil(err))
	})

	t.Run("gopls receiver spelling normalizes for comparison", func(t *testing.T) {
		methodSrc := []byte(`package p

type A struct{}

func (a *A) Get() int { return 1 }
`)
		getRes := mustResolve(t, methodSrc, "A.Get")
		// gopls reports no containerName for methods (docs/ANCHORS.md); the
		// receiver lives in the name string itself.
		found, err := resolve.MatchAndCompare(methodSrc, getRes, []lsp.Symbol{
			{Name: "(*A).Get", StartLine: 4, EndLine: 4},
		})
		qt.Assert(t, qt.IsTrue(found))
		qt.Assert(t, qt.IsNil(err))
	})

	t.Run("ordinal anchor matches the Nth same-named symbol in order", func(t *testing.T) {
		initSrc := []byte(`package p

func init() { println(1) }

func init() { println(2) }
`)
		second := mustResolve(t, initSrc, "init#2")
		found, err := resolve.MatchAndCompare(initSrc, second, []lsp.Symbol{
			{Name: "init", StartLine: 2, EndLine: 2},
			{Name: "init", StartLine: 4, EndLine: 4},
		})
		qt.Assert(t, qt.IsTrue(found))
		qt.Assert(t, qt.IsNil(err))
	})
}

func TestResolve_CrossCheckLiveGopls(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("live language-server cross-check skipped under -short")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := []byte(`package p

// ValidateToken checks the JWT.
// Returns ErrExpired if stale.
func ValidateToken(t string) error {
	return nil
}
`)
	path := filepath.Join(dir, "auth.go")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}

	res := mustResolve(t, src, "ValidateToken")
	lang := resolverGoLang(t)
	ctx := context.Background()

	// A fresh session per attempt, deliberately: a session remembers a
	// language that degraded and will not redial it, so reusing one here
	// would pin the first cold result forever. Separate sessions model
	// what this actually simulates -- successive rgit invocations, the
	// first spawning a daemon without waiting for it, since rgit never
	// blocks on a cold server.
	attempt := func(r *resolve.Resolution) (bool, error) {
		sess := lsp.NewSession()
		defer sess.Close()
		return crossCheckOne(ctx, sess, lang, dir, path, src, r)
	}

	var (
		degraded = true
		err      error
	)
	for deadline := time.Now().Add(3 * time.Second); ; {
		degraded, err = attempt(res)
		if !degraded || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if degraded {
		t.Skip("gopls daemon did not come up within the test's budget -- degraded, not a failure")
	}
	qt.Assert(t, qt.IsNil(err))

	// Corrupting DeclOnly.End forces a genuine disagreement, proving exit 6
	// fires against a real server's range, not only the mock-driven table
	// test above.
	mismatched := *res
	mismatched.DeclOnly.End -= 5
	_, mismatchErr := attempt(&mismatched)
	qt.Assert(t, qt.IsNotNil(mismatchErr))
	var rerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(mismatchErr, &rerr))
	qt.Assert(t, qt.Equals(rerr.Code, exitcode.ExtentMismatch))
}

// TestResolve_CrossCheckLiveHTML proves the wiring end to end against the
// real installed vscode-html-language-server, the same "real dependency
// over a double" bar TestResolve_CrossCheckLiveGopls holds gopls to
// (CONTRIBUTING.md § Tests). Unlike gopls, HTML has no daemon to warm up --
// every non-Go server here is spawned fresh per query and live on its first
// invocation (docs/INSTALL.md § Language servers) -- so this needs no
// polling loop, one attempt is the real behaviour.
func TestResolve_CrossCheckLiveHTML(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("live language-server cross-check skipped under -short")
	}
	if _, err := exec.LookPath("vscode-html-language-server"); err != nil {
		t.Skip("vscode-html-language-server not on PATH")
	}

	dir := t.TempDir()
	src := []byte(`<!DOCTYPE html>
<html>
<body>
<div id="app">
<input id="name" type="text">   trailing text with no sibling to stop it
<p id="after">next</p>
</div>
</body>
</html>
`)
	path := filepath.Join(dir, "fixture.html")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}

	lang, ok := resolve.ForExtension(".html")
	qt.Assert(t, qt.IsTrue(ok))
	ctx := context.Background()

	attempt := func(anchor string) (degraded bool, err error) {
		res, rerr := resolve.Resolve(lang, src, anchor)
		qt.Assert(t, qt.IsNil(rerr))
		sess := lsp.NewSession()
		defer sess.Close()
		return crossCheckOne(ctx, sess, lang, dir, path, src, res)
	}

	// An id-bearing element, including the void-element case declOnlyExtent's
	// own trim seam exists for, cross-checks clean against the real server --
	// not merely a mock's idea of one.
	for _, anchor := range []string{"div#app", "input#name", "p#after"} {
		degraded, err := attempt(anchor)
		qt.Assert(t, qt.IsFalse(degraded), qt.Commentf("anchor %q", anchor))
		qt.Assert(t, qt.IsNil(err), qt.Commentf("anchor %q", anchor))
	}

	// Class-bearing elements cross-check once the server's ".class…" suffix is
	// stripped from its Name; staged anchors stay tag#id.
	classSrc := []byte(`<div id="widget" class="foo bar">x</div>` + "\n")
	classPath := filepath.Join(dir, "class.html")
	if err := os.WriteFile(classPath, classSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	classRes, err := resolve.Resolve(lang, classSrc, "div#widget")
	qt.Assert(t, qt.IsNil(err))
	sess := lsp.NewSession()
	defer sess.Close()
	degraded, cerr := crossCheckOne(ctx, sess, lang, dir, classPath, classSrc, classRes)
	qt.Assert(t, qt.IsFalse(degraded))
	qt.Assert(t, qt.IsNil(cerr))
}

// TestResolve_HTML pins the deliberately narrow HTML anchor syntax:
// element + id only ("div#app"), nested to whatever depth the worktree
// actually nests it, with a doc comment attributed the same way every
// other language's is, and @header/@imports/@toplevel each given a
// considered answer -- including @imports, which does not apply and says
// so by degrading to unresolved rather than silently matching nothing
// useful.
func TestResolve_HTML(t *testing.T) {
	t.Parallel()
	src := []byte(`<!DOCTYPE html>
<html>
<head><title>T</title></head>
<body>
<!-- mount point -->
<div id="app" class="widget">
  <section id="content">
    <p>hi</p>
  </section>
  <input id="field" type="text">
</div>
</body>
</html>
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".html", src, "@header"), "<!DOCTYPE html>"))

	// tag#id resolves regardless of nesting depth, container-qualified by
	// its own tag name (Declaration.Sep "#" is what produces this exact
	// spelling, not a second join rule).
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".html", src, "section#content"),
		"<section id=\"content\">\n    <p>hi</p>\n  </section>"))

	// Naming the outer element claims everything nested inside it,
	// including the leading comment attributed to it as a doc comment (no
	// blank line separates them) -- the same attribution rule every other
	// language's fullExtent already applies.
	div := mustResolveExt(t, ".html", src, "div#app")
	qt.Assert(t, qt.StringContains(div, "<!-- mount point -->"))
	qt.Assert(t, qt.StringContains(div, "<section id=\"content\">"))
	qt.Assert(t, qt.StringContains(div, "<input id=\"field\""))

	// A void element resolves to its own tag alone, not the rest of the
	// file -- proof that tree-sitter-html's own void-element trailing-
	// content quirk never crosses into a sibling's bytes, only ever
	// absorbs harmless trailing whitespace.
	field := mustResolveExt(t, ".html", src, "input#field")
	qt.Assert(t, qt.StringContains(field, "<input id=\"field\" type=\"text\">"))
	qt.Assert(t, qt.Not(qt.StringContains(field, "</div>")))
	qt.Assert(t, qt.Not(qt.StringContains(field, "</section>")))

	// An element with no id is not addressable at all -- not even the sole
	// <title> or <body> in the document -- element+id was deliberately kept
	// this narrow rather than falling back to a bare tag name.
	lang, ok := resolve.ForExtension(".html")
	qt.Assert(t, qt.IsTrue(ok))
	for _, anchor := range []string{"title", "body", "p", "div"} {
		_, err := resolve.Resolve(lang, src, anchor)
		var unresolvable *resolve.ResolveError
		qt.Assert(t, qt.ErrorAs(err, &unresolvable), qt.Commentf("anchor %q", anchor))
		qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))
	}

	// @imports does not apply: no node kind in this grammar plays the role
	// of an import statement, so it degrades to unresolved rather than
	// matching <link>/<script src> guesswork (docs/ANCHORS.md).
	_, err := resolve.Resolve(lang, src, "@imports")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// @toplevel spans every addressable element -- here, the whole <html>
	// element, since div#app (the outermost declaration) sits inside it.
	toplevel := mustResolveExt(t, ".html", src, "@toplevel")
	qt.Assert(t, qt.StringContains(toplevel, "<html>"))
	qt.Assert(t, qt.StringContains(toplevel, "</html>"))
	qt.Assert(t, qt.Not(qt.StringContains(toplevel, "<!DOCTYPE")))

	// DeclOrder lists every id-bearing element in source order, and only
	// those -- confirming the id-less <html>/<head>/<title>/<body>/<p>
	// wrappers were walked through, not staged as declarations of their own.
	order, err := resolve.DeclOrder(lang, src)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.DeepEquals(order, []string{"div#app", "section#content", "input#field"}))
}

// TestResolve_HTMLDuplicateIDIsAmbiguous pins the second HTML decision: an
// id is unique per document by spec but routinely is not in practice, and
// index.go's existing byBare/byQualified/ordinal machinery already
// reports that as exit 4 (AnchorAmbiguous) with no HTML-specific code at
// all -- the same mechanism two identical CSS selectors or two same-named
// Go functions already share.
func TestResolve_HTMLDuplicateIDIsAmbiguous(t *testing.T) {
	t.Parallel()
	src := []byte(`<div id="app"></div>
<span id="app"></span>
<div id="hero"></div>
<div id="hero"></div>
`)
	lang, ok := resolve.ForExtension(".html")
	qt.Assert(t, qt.IsTrue(ok))

	// Two different tags sharing one id: the bare id alone is ambiguous,
	// each tag-qualified spelling resolves unambiguously on its own.
	_, err := resolve.Resolve(lang, src, "app")
	var ambiguous *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &ambiguous))
	qt.Assert(t, qt.Equals(ambiguous.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(ambiguous.Candidates, []string{"div#app", "span#app"}))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".html", src, "div#app"), `<div id="app"></div>`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".html", src, "span#app"), `<span id="app"></span>`))

	// The same tag and id repeated: even the tag-qualified spelling
	// collides, and the existing ordinal fallback disambiguates it exactly
	// the way two same-named Go package-level functions already do.
	_, err = resolve.Resolve(lang, src, "hero")
	qt.Assert(t, qt.ErrorAs(err, &ambiguous))
	qt.Assert(t, qt.Equals(ambiguous.Code, exitcode.AnchorAmbiguous))
	_, err = resolve.Resolve(lang, src, "div#hero")
	qt.Assert(t, qt.ErrorAs(err, &ambiguous))
	qt.Assert(t, qt.Equals(ambiguous.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(ambiguous.Candidates, []string{"div#hero#1", "div#hero#2"}))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".html", src, "div#hero#1"), `<div id="hero"></div>`))
}

func TestResolve_HTMLDuplicateTagIDOrdinal(t *testing.T) {
	t.Parallel()
	src := []byte(`<div id="app">first</div>
<div id="app">second</div>
`)

	qt.Assert(t, qt.Equals(
		mustResolveExt(t, ".html", src, "div#app#2"),
		`<div id="app">second</div>`,
	))
}

// TestResolve_HTMLVoidElementDeclOnlyTrimmed pins the seam declOnlyExtent
// consults for HTML alone: a void element followed by inline text with no
// enclosing tag or sibling element to stop it (unlike TestResolve_HTML's own
// "<input ...>\n</div>" fixture, where the enclosing tag's own close already
// bounds it) absorbs that text into its own node's EndByte() -- left alone
// in the extent that gets staged (Extent, TestResolve_HTML's own coverage),
// but trimmed back to the start_tag's own end in DeclOnly, the extent the
// later LSP cross-check compares against a server that never reports that
// absorbed text as part of the element either.
func TestResolve_HTMLVoidElementDeclOnlyTrimmed(t *testing.T) {
	t.Parallel()
	src := []byte(`<div id="app">
<input id="name" type="text">   trailing text with no sibling to stop it
<p id="after">next</p>
</div>
`)
	lang, ok := resolve.ForExtension(".html")
	qt.Assert(t, qt.IsTrue(ok))

	res, err := resolve.Resolve(lang, src, "input#name")
	qt.Assert(t, qt.IsNil(err))

	declOnly := string(src[res.DeclOnly.Start:res.DeclOnly.End])
	qt.Assert(t, qt.Equals(declOnly, `<input id="name" type="text">`))

	full := string(src[res.Extent.Start:res.Extent.End])
	qt.Assert(t, qt.StringContains(full, "trailing text with no sibling to stop it"),
		qt.Commentf("Extent (what gets staged) keeps the grammar's own absorption -- only DeclOnly (cross-check) is trimmed"))

	// An element with a real end_tag is unaffected: DeclOnly and Extent
	// agree, nothing to trim.
	pRes, err := resolve.Resolve(lang, src, "p#after")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(pRes.DeclOnly, pRes.Extent))
}

// TestResolve_Rust covers the shapes the grammar actually has to get right:
// items named by their own field, an impl block that has no name field at
// all, members qualified with Rust's own "::" path separator, and the outer
// attributes that are siblings of the item they annotate rather than part of
// it.
func TestResolve_Rust(t *testing.T) {
	t.Parallel()
	src := []byte(`use std::fmt;

pub const LIMIT: usize = 10;

/// Doc comment.
#[inline]
pub fn classify(input: &str) -> bool {
    !input.is_empty()
}

pub struct Config {
    pub name: String,
}

pub enum Kind {
    A,
    B(u8),
}

pub trait Render {
    fn render(&self) -> String;
}

impl Render for Config {
    fn render(&self) -> String {
        self.name.clone()
    }
}

impl Config {
    pub fn new(name: String) -> Self {
        Self { name }
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn alpha() {
        assert!(true);
    }
}
`)

	// An item is named by its own name field, and "::" joins a member to
	// its container -- what a Rust caller would type, not the "." every
	// other adapter's convention produces.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "LIMIT"), "pub const LIMIT: usize = 10;"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "Config::name"), "pub name: String,"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "Kind::B"), "B(u8),"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "Render::render"), "fn render(&self) -> String;"))

	// An impl block has no name field. It is named the way Rust reads it,
	// so it cannot collide with the struct of the same name, and its
	// members qualify by the type rather than by that longer display name.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "impl Config"),
		"impl Config {\n    pub fn new(name: String) -> Self {\n        Self { name }\n    }\n}"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "Config::new"),
		"pub fn new(name: String) -> Self {\n        Self { name }\n    }"))

	lang, ok := resolve.ForExtension(".rs")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "impl Render for Config")
	qt.Assert(t, qt.IsNil(err))

	// A doc comment and an outer attribute both belong to the item. The
	// attribute is a sibling in this grammar, not a wrapper the way
	// Python's decorated_definition is, so without prefixAttacher deleting
	// an item left its attribute orphaned.
	classify := mustResolveExt(t, ".rs", src, "classify")
	qt.Assert(t, qt.Equals(classify,
		"/// Doc comment.\n#[inline]\npub fn classify(input: &str) -> bool {\n    !input.is_empty()\n}"))

	// A module is descended into -- that is where a Rust crate's tests
	// live -- while a function body is not.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "tests::alpha"),
		"#[test]\n    fn alpha() {\n        assert!(true);\n    }"))

	// @imports spans the use declarations, never an item.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "@imports"), "use std::fmt;"))
}

func TestResolve_ForPathShebangFallback(t *testing.T) {
	t.Parallel()
	// A recognized extension is authoritative and never even looks at
	// content: passing shebang-shaped bytes that would map to a different
	// language must not steer a ".go" file anywhere else.
	lang, ok := resolve.ForPathFolding("main.go", []byte("#!/usr/bin/env python3\n"), false)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(lang.Name(), "go"))

	// Both forms of both shells named in the spec resolve to "shell", for an
	// extensionless path.
	for _, shebang := range []string{
		"#!/bin/bash\n", "#!/usr/bin/env bash\n",
		"#!/bin/sh\n", "#!/usr/bin/env sh\n",
	} {
		lang, ok := resolve.ForPathFolding("hooks/pre-commit", []byte(shebang+"foo() {\n  echo hi\n}\n"), false)
		qt.Assert(t, qt.IsTrue(ok), qt.Commentf("shebang %q", shebang))
		qt.Assert(t, qt.Equals(lang.Name(), "shell"))
	}

	// python3 falls out cleanly too: the same adapter Python's own
	// extension resolves to, with no special-casing for how it was reached.
	lang, ok = resolve.ForPathFolding("bin/tool", []byte("#!/usr/bin/env python3\n\ndef foo():\n    return 1\n"), false)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(lang.Name(), "python"))

	// zsh is a real interpreter, not a typo, and is deliberately refused:
	// tree-sitter-bash would mis-parse zsh-only syntax rather than honestly
	// fail. An unrecognized interpreter (perl) and a leading line that is
	// an ordinary comment, not a shebang, both refuse the same way, as does
	// an extensionless path with no content at all to sniff.
	for _, c := range []struct {
		path    string
		content string
	}{
		{"hooks/pre-commit", "#!/bin/zsh\nfoo() {}\n"},
		{"hooks/pre-commit", "#!/usr/bin/env perl\n"},
		{"hooks/pre-commit", "# just a comment, not a shebang\nfoo() {}\n"},
		{"hooks/pre-commit", ""},
	} {
		_, ok := resolve.ForPathFolding(c.path, []byte(c.content), false)
		qt.Assert(t, qt.IsFalse(ok), qt.Commentf("content %q", c.content))
	}
}

// crossCheckOne drives resolve.CrossCheckExtents for a single resolution.
// The tests below assert per-anchor outcomes against a live server; the
// package itself only offers the batch form, since production never wants
// one round trip per anchor.
func crossCheckOne(ctx context.Context, sess *lsp.Session, lang resolve.Language, repoRoot, absPath string, src []byte, res *resolve.Resolution) (bool, error) {
	degraded, mismatches := resolve.CrossCheckExtents(ctx, sess, lang, repoRoot, absPath, src, []*resolve.Resolution{res})
	if len(mismatches) > 0 {
		return degraded, mismatches[0]
	}
	return degraded, nil
}
