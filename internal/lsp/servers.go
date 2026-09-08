package lsp

import (
	"slices"
	"strings"
)

// transportKind is how Dial reaches a language's server. Only "go" has a
// real listen-mode daemon (specs/design.md § Symbol resolution): gopls's
// `-listen=unix;<path>` binds a unix socket other processes can dial into.
// vtsls's and pyright's own `--socket=<port>` flag instead dials OUT, as a
// TCP client, to a listener the caller must already have bound. Neither
// offers anything rgit can probe for at a fixed path the way it does for
// gopls.
type transportKind int

const (
	// transportSocket servers accept a unix-domain-socket daemon: probe
	// first, spawn on demand if nothing answers.
	transportSocket transportKind = iota
	// transportStdio servers are spawned fresh per query over stdio; there
	// is no persistent daemon to probe for.
	transportStdio
)

// serverSpec is everything Dial needs to reach one language's server.
type serverSpec struct {
	// name identifies the server in the socket filename
	// (rgit-<uid>/rgit-<name>.sock, built by defaultSocketPath from the
	// private directory privateSocketDir vouches for — dial.go) and in
	// [ts-only] diagnostics.
	name string
	// bin is the binary Dial looks up on PATH.
	bin       string
	transport transportKind
	// daemonArgs builds the argv for spawning bin as a socket daemon
	// listening at sockPath. Only set for transportSocket servers.
	daemonArgs func(sockPath string) []string
	// stdioArgs is the fixed argv for spawning bin as a one-shot stdio
	// subprocess. Only set for transportStdio servers.
	stdioArgs []string
}

// vtslsSpec is shared by the "typescript" and "tsx" catalog entries below:
// one TypeScript server handles both grammars; only the didOpen languageId
// differs (client.go).
var vtslsSpec = serverSpec{
	name:      "vtsls",
	bin:       "vtsls",
	transport: transportStdio,
	stdioArgs: []string{"--stdio"},
}

// servers maps a resolve.Language's Name() to the server that cross-checks
// it. "tsx" shares vtsls with "typescript" — one TypeScript server handles
// both grammars; only the didOpen languageId differs (client.go).
var servers = map[string]serverSpec{
	"go": {
		name:      "gopls",
		bin:       "gopls",
		transport: transportSocket,
		daemonArgs: func(sockPath string) []string {
			// listen.timeout shuts the daemon down after it sits idle with
			// no connected client, so a spawned-and-forgotten gopls does
			// not outlive every rgit invocation that will ever ask for it.
			return []string{"-listen=unix;" + sockPath, "-listen.timeout=10m"}
		},
	},
	// "typescript" and "tsx" both dial vtsls -- one server, two grammars
	// (tsFamily's own doc comment in internal/resolve/lang_typescript.go) --
	// so both catalog entries share the identical serverSpec literal.
	"typescript": vtslsSpec,
	"tsx":        vtslsSpec,
	"python": {
		name:      "pyright",
		bin:       "pyright-langserver",
		transport: transportStdio,
		stdioArgs: []string{"--stdio"},
	},
	"shell": {
		name:      "bash-language-server",
		bin:       "bash-language-server",
		transport: transportStdio,
		// Unlike vtsls and pyright-langserver, bash-language-server's stdio
		// mode needs no separate flag: "start" is stdio, full stop -- its
		// own docs show no dedicated flag for it.
		stdioArgs: []string{"start"},
	},
	// yaml, json, css, and markdown's servers hold to the same bar as the
	// four above: their documentSymbol ranges match this resolver's own
	// declOnlyExtent byte-for-byte on real fixtures (specs/design.md §
	// Cross-check coverage), including the doc-comment-exclusion case (a
	// leading comment with no blank line before the symbol) -- not merely
	// "it has a --stdio flag".
	"yaml": {
		name:      "yaml-language-server",
		bin:       "yaml-language-server",
		transport: transportStdio,
		stdioArgs: []string{"--stdio"},
	},
	"json": {
		name:      "vscode-json-language-server",
		bin:       "vscode-json-language-server",
		transport: transportStdio,
		stdioArgs: []string{"--stdio"},
	},
	"css": {
		name:      "vscode-css-language-server",
		bin:       "vscode-css-language-server",
		transport: transportStdio,
		stdioArgs: []string{"--stdio"},
	},
	// marksman, not vscode-markdown-language-server: the latter crashes on
	// startup on this machine (specs/design.md), an ESM/CJS interop defect
	// in its own bundled dependency, not a transport choice. marksman's own
	// daemon-shaped "server" subcommand is not used; it is dialled the same
	// one-shot stdio way as the other non-gopls servers here, matching the
	// transport every LSP client already spawns it with (per marksman's own
	// docs, "server" runs the LSP on stdio -- there is no separate
	// socket-daemon mode).
	"markdown": {
		name:      "marksman",
		bin:       "marksman",
		transport: transportStdio,
		stdioArgs: []string{"server"},
	},
	// vscode-html-language-server's documentSymbol ranges match this
	// resolver's own declOnly extent for a bare id-bearing element
	// ("div#app") once void-element trailing absorption is trimmed
	// (internal/resolve/lang_html.go's declEndTrimmer) -- measured against
	// the installed binary, not assumed (specs/design.md § Grammar scope).
	"html": {
		name:      "vscode-html-language-server",
		bin:       "vscode-html-language-server",
		transport: transportStdio,
		stdioArgs: []string{"--stdio"},
	},
}

// ServerInfo is one server rgit's cross-check can dial, plus which
// grammars resolve to it. serverSpec itself stays unexported (Dial owns
// transport internals no caller outside this package needs); this is the
// read-only subset a caller like `rgit doctor` needs to report every
// wired server without a second, hand-maintained list.
type ServerInfo struct {
	// Bin is the binary Dial looks up on PATH.
	Bin string
	// Languages is every resolve.Language.Name() this server cross-checks,
	// sorted. More than one language can share a server -- "typescript"
	// and "tsx" both dial vtsls.
	Languages []string
}

// Servers returns one ServerInfo per distinct binary in the package-level
// servers map, sorted by Bin, so a caller iterating the result gets a
// stable order without sorting it itself.
func Servers() []ServerInfo {
	byBin := map[string][]string{}
	specByBin := map[string]serverSpec{}
	for lang, spec := range servers {
		byBin[spec.bin] = append(byBin[spec.bin], lang)
		specByBin[spec.bin] = spec
	}
	out := make([]ServerInfo, 0, len(byBin))
	for bin, langs := range byBin {
		slices.Sort(langs)
		out = append(out, ServerInfo{Bin: bin, Languages: langs})
	}
	slices.SortFunc(out, func(a, b ServerInfo) int { return strings.Compare(a.Bin, b.Bin) })
	return out
}
