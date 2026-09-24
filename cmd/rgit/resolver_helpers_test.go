// Resolver coverage, per CONTRIBUTING.md's three-file test budget. Byte
// offsets below reflect real tree-sitter output against each fixture, not
// grammar docs.

// Grammar coverage is split per language (resolver_<lang>_test.go,
// resolver_sql_test.go); this file holds the helpers they share.
package main

import (
	"context"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

func resolverGoLang(t *testing.T) resolve.Language { //nolint:ireturn // resolve.ForExtension's own concrete type is unexported; this forwards its interface value to resolve.Resolve
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
