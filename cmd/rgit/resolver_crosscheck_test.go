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
			t.Parallel()
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
		t.Parallel()
		found, err := resolve.MatchAndCompare(src, res, []lsp.Symbol{
			{Name: "ValidateToken", StartLine: 3, EndLine: 5},
		})
		qt.Assert(t, qt.IsTrue(found))
		qt.Assert(t, qt.IsNil(err))
	})

	t.Run("disagreement is exit 6 with both ranges attached", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
		found, err := resolve.MatchAndCompare(src, res, nil)
		qt.Assert(t, qt.IsFalse(found))
		qt.Assert(t, qt.IsNil(err))
	})

	t.Run("gopls receiver spelling normalizes for comparison", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
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
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.21\n"), 0o600); err != nil {
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
	if err := os.WriteFile(path, src, 0o600); err != nil {
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
		degraded bool
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
	if err := os.WriteFile(path, src, 0o600); err != nil {
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
	if err := os.WriteFile(classPath, classSrc, 0o600); err != nil {
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
