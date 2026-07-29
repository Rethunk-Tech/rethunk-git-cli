package lsp

// transportKind is how Dial reaches a language's server. Only "go" turned
// out to have a real listen-mode daemon in the transport survey recorded in
// specs/design.md § Symbol resolution: gopls's `-listen=unix;<path>` binds
// a unix socket other processes can dial into. vtsls's and pyright's own
// `--socket=<port>` flag instead dials OUT, as a TCP client, to a listener
// the caller must already have bound — measured directly (see design.md),
// not assumed from either tool's docs. Neither offers anything rgit can
// probe for at a fixed path the way it does for gopls.
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
	// ($XDG_RUNTIME_DIR/rgit-<name>.sock) and in [ts-only] diagnostics.
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
	"typescript": {
		name:      "vtsls",
		bin:       "vtsls",
		transport: transportStdio,
		stdioArgs: []string{"--stdio"},
	},
	"tsx": {
		name:      "vtsls",
		bin:       "vtsls",
		transport: transportStdio,
		stdioArgs: []string{"--stdio"},
	},
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
		// own docs show no dedicated flag for it. Verified end to end
		// against a live server (5.6.0): a changed shell function
		// cross-checks clean with it on PATH and reports [ts-only] with it
		// removed, the same two directions gopls is checked in.
		stdioArgs: []string{"start"},
	},
	// yaml, json, css, and markdown were added only after their servers'
	// documentSymbol ranges were measured byte-for-byte against this
	// resolver's own declOnlyExtent on real fixtures (specs/design.md §
	// Cross-check survey) -- not on the strength of "it has a --stdio
	// flag" the way the four above already were. All four matched exactly,
	// including the doc-comment-exclusion case (a leading comment with no
	// blank line before the symbol) each of the original four servers was
	// already held to.
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
	// startup on this machine (measured -- specs/design.md), an ESM/CJS
	// interop defect in its own bundled dependency, not a transport
	// choice. marksman's own daemon-shaped "server" subcommand is not
	// used; it is dialled the same one-shot stdio way as the other
	// non-gopls servers here, matching the transport every LSP client
	// already spawns it with (per marksman's own docs, "server" runs the
	// LSP on stdio -- there is no separate socket-daemon mode).
	"markdown": {
		name:      "marksman",
		bin:       "marksman",
		transport: transportStdio,
		stdioArgs: []string{"server"},
	},
}
