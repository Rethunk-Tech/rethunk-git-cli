package lsp_test

import (
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// neverWired names every resolve.Languages() entry that is deliberately
// permanent [ts-only], with the reason a missing lsp.Servers() entry would
// otherwise wrongly flag as drift (docs/LIMITATIONS.md § Language-server
// coverage carries the fuller version of each): taplo's own ranges disagree
// with tree-sitter-toml on nested tables; no maintained tool speaks
// documentSymbol for SQL at all. HTML is wired (servers.go) -- id-only and
// class-bearing elements both cross-check once the server's ".class…"
// suffix is stripped from its Name for matching. Add a language here only
// with a measured reason, matching LIMITATIONS.md -- everything else in
// Languages() is expected to have a wired server.
var neverWired = map[string]string{
	"toml": "taplo completes the handshake but its ranges genuinely disagree with tree-sitter-toml on nested tables",
	"sql":  "no maintained tool speaks documentSymbol for SQL",
}

// TestServersMap_CoversEveryResolveLanguage is the reverse of
// internal/resolve/lspwiring_test.go's TestServerLanguages_HaveLanguageKindMapping:
// that guard catches an lsp.Servers() entry naming a language Languages()
// does not know about; nothing previously caught the opposite drift -- a
// newly registered resolve grammar landing with no server wired for it,
// which would silently stay [ts-only] forever with no test ever failing for
// it. neverWired above is the allowlist for the languages that are
// [ts-only] on purpose; everything else must be wired.
//
// This is package lsp_test (an external test), not package lsp, and reads
// the wired-language set through the exported lsp.Servers() rather than the
// unexported servers map directly: an internal (package lsp) test file
// importing internal/resolve does cycle, because resolve already imports
// the production lsp package that the "lsp [lsp.test]" variant this file
// would compile into is required to link against -- verified with `go vet`
// while drafting this test, not merely assumed. Living in
// internal/resolve instead (which can see both sides directly, the way
// lspwiring_test.go does) was ruled out: that file belongs to another
// agent's fence for this change.
func TestServersMap_CoversEveryResolveLanguage(t *testing.T) {
	t.Parallel()

	wired := map[string]bool{}
	for _, s := range lsp.Servers() {
		for _, name := range s.Languages {
			wired[name] = true
		}
	}

	for _, info := range resolve.Languages() {
		if reason, exempt := neverWired[info.Name]; exempt {
			t.Logf("%q exempt from server wiring: %s", info.Name, reason)
			continue
		}
		if !wired[info.Name] {
			t.Errorf("resolve.Languages() has %q with no wired internal/lsp server -- wire one or add it to neverWired with a reason", info.Name)
		}
	}
}
