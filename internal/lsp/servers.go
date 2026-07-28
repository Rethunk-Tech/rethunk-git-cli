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
}
