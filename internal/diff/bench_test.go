package diff

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// buildLargeGoClassFixture generates a Go file with a 200-method struct,
// changing every method's return value on the "new" side -- the shape the
// held-parse gain was measured against (0.78s re-parsing per declaration
// vs 0.02s held open, ~39x).
func buildLargeGoClassFixture(members int) (oldSrc, newSrc []byte) {
	var oldBuf, newBuf strings.Builder
	oldBuf.WriteString("package p\n\ntype Big struct{}\n\n")
	newBuf.WriteString("package p\n\ntype Big struct{}\n\n")
	for i := range members {
		fmt.Fprintf(&oldBuf, "func (b Big) M%d() int { return %d }\n\n", i, i)
		fmt.Fprintf(&newBuf, "func (b Big) M%d() int { return %d }\n\n", i, i+1)
	}
	return []byte(oldBuf.String()), []byte(newBuf.String())
}

// BenchmarkAttribution_200MemberClass is the regression gate for the
// held-parse gain buildLargeGoClassFixture measures: attributeSymbols
// (and everything it calls -- resolveRegions, exclusiveText, isolatedDiff)
// must keep doing one parse per side, not one per declaration. There is no
// re-parsing code path left to compare against directly -- attributeSymbolsOpen
// always holds its caller's parse open -- so this benchmark exists to catch
// a future regression, not to reproduce the historical comparison. Compare
// ns/op against a prior run's own baseline (`go test -bench` output, or
// `benchstat` across two runs); cgo + tree-sitter makes absolute time
// machine-noisy, so a ratio is the only stable signal (CONTRIBUTING.md §
// Tests § Benchmarks).
func BenchmarkAttribution_200MemberClass(b *testing.B) {
	lang, ok := resolve.ForExtension(".go")
	if !ok {
		b.Fatal("resolve: no adapter registered for .go")
	}
	oldSrc, newSrc := buildLargeGoClassFixture(200)

	for b.Loop() {
		if _, _, err := attributeSymbols(lang, oldSrc, newSrc, 200, 200); err != nil {
			b.Fatal(err)
		}
	}
}

// buildCrossCheckFixture commits n Python and n TypeScript files (2n total,
// no daemon for either language -- pyright-langserver and vtsls are both
// one-shot-per-query stdio servers), then edits every one of them, so
// Run's own cross-check has 2n
// independent LSP round trips to make.
func buildCrossCheckFixture(b *testing.B, n int) (dir string, repo *gitx.Repo) {
	b.Helper()
	dir, repo = gittest.New(b.Context(), b)
	for i := range n {
		gittest.Write(b, dir, fmt.Sprintf("m%d.py", i), fmt.Sprintf("def f%d():\n    return %d\n", i, i))
		gittest.Write(b, dir, fmt.Sprintf("m%d.ts", i), fmt.Sprintf("export function f%d(): number {\n  return %d;\n}\n", i, i))
	}
	gittest.Commit(b.Context(), b, dir, "chore: initial fixture")
	for i := range n {
		gittest.Write(b, dir, fmt.Sprintf("m%d.py", i), fmt.Sprintf("def f%d():\n    return %d\n", i, i+1))
		gittest.Write(b, dir, fmt.Sprintf("m%d.ts", i), fmt.Sprintf("export function f%d(): number {\n  return %d;\n}\n", i, i+1))
	}
	return dir, repo
}

// BenchmarkRun_CrossCheckManyFiles is the regression gate for
// parallelFileReports (run.go): a commit touching many files across two
// non-daemon-server languages must not regress to one LSP round trip at a
// time. Measured directly against a forced maxConcurrentFileReports=1
// (change the constant, `go test -bench` both ways, revert) on this
// machine: 24 files (12 Python + 12 TypeScript) against real
// pyright-langserver and vtsls processes averaged 471ms/op at the default
// cap of 8 versus 828ms/op serialized at 1 -- a real ~1.8x reduction, well
// short of the 8x the cap alone might suggest. Two things the fan-out does
// not touch cap it: tree-sitter parsing for a given language is already
// serialized behind one shared *ts.Parser regardless of caller concurrency
// (internal/resolve/resolver.go's parserCacheMu, held for the whole
// parse), and pyright/vtsls are each one server process handling every
// query this benchmark sends it -- concurrent requests from this client
// only shorten wall-clock time to the extent each server's own internals
// overlap them, which this benchmark does not measure or assume. Compare
// ns/op against a prior run's own number the same way
// BenchmarkAttribution_200MemberClass above does -- cgo and subprocess
// spawn jitter make absolute time noisy across machines.
func BenchmarkRun_CrossCheckManyFiles(b *testing.B) {
	if testing.Short() {
		b.Skip("live language-server dial skipped under -short")
	}
	for _, bin := range []string{"pyright-langserver", "vtsls"} {
		if _, err := exec.LookPath(bin); err != nil {
			b.Skipf("%s not on PATH", bin)
		}
	}

	dir, repo := buildCrossCheckFixture(b, 12)
	ctx := context.Background()

	for b.Loop() {
		if _, err := Run(ctx, repo, dir, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
