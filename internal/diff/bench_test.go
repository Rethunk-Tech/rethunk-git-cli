package diff

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// buildLargeGoClassFixture generates a Go file with a 200-method struct,
// changing every method's return value on the "new" side -- the same shape
// specs/design.md § Blob synthesis measured the held-parse gain against
// (0.78s re-parsing per declaration vs 0.02s held open, ~39x).
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
// held-parse gain specs/design.md § Blob synthesis measures: attributeSymbols
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
