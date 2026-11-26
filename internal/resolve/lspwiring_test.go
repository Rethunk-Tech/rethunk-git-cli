package resolve

import (
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/lsp"
)

// TestServerLanguages_HaveLanguageKindMapping guards the seam between two of
// the three places a language's LSP wiring is encoded (AGENTS.md's delegation
// boundary): an adapter here in internal/resolve, internal/lsp/servers.go's
// servers map (keyed by Language.Name()), and internal/lsp/client.go's
// LanguageKindForName (keyed by file extension, then language name). This test asserts every
// servers-map language also has a working didOpen languageId, so a newly
// added server entry with no matching LanguageKindForName case fails here
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

			for _, ext := range exts {
				if _, ok := lsp.LanguageKindForName(name, "fixture"+ext); !ok {
					t.Errorf("language %q (server bin %q) has no languageId for extension %s", name, server.Bin, ext)
				}
			}
			// An extensionless file (a shebang script) resolves by language alone.
			if _, ok := lsp.LanguageKindForName(name, "fixture"); !ok {
				t.Errorf("language %q (server bin %q) has no languageId for an extensionless file", name, server.Bin)
			}
		}
	}
}
