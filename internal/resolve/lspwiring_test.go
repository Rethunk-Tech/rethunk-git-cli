package resolve

import (
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
)

// TestServerLanguages_HaveLanguageKindMapping guards the seam between two of
// the three places a language's LSP wiring is encoded (AGENTS.md's delegation
// boundary): an adapter here in internal/resolve, internal/lsp/servers.go's
// servers map (keyed by Language.Name()), and internal/lsp/client.go's
// LanguageKindFor (keyed by file extension). This test asserts every
// servers-map language also has a working didOpen languageId, so a newly
// added server entry with no matching LanguageKindFor case fails here
// instead of degrading silently at runtime (client.go's DocumentSymbols).
//
// This lives in package resolve, not lsp, because it needs Languages()'s own
// name -> extensions table as the source of which extensions to try per
// server-catalog language name, and a hand-written second copy of that table
// here would just be the same drift this guard exists to catch.
// internal/lsp cannot import internal/resolve for this instead (resolve
// already imports lsp, for CrossCheckExtents' own Dial call, so the reverse
// import would cycle) -- resolve is the one package that can see both sides
// of the seam.
func TestServerLanguages_HaveLanguageKindMapping(t *testing.T) {
	t.Parallel()

	extsByName := map[string][]string{}
	for _, info := range Languages() {
		extsByName[info.Name] = info.Extensions
	}

	for _, server := range lsp.Servers() {
		for _, name := range server.Languages {
			exts, ok := extsByName[name]
			if !ok {
				t.Errorf("servers catalog (bin %q) names language %q, which Languages() has no adapter for", server.Bin, name)
				continue
			}

			mapped := false
			for _, ext := range exts {
				if _, ok := lsp.LanguageKindFor("fixture" + ext); ok {
					mapped = true
					break
				}
			}
			if !mapped {
				t.Errorf("language %q (extensions %v, server bin %q) has no LanguageKindFor mapping for any of its extensions", name, exts, server.Bin)
			}
		}
	}
}
