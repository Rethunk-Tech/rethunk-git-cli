package lsp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// configClient answers workspace/configuration, the one server-initiated
// request rgit implements rather than leaving unanswered: taplo (TOML)
// silently reports zero symbols for a document it has just been told to
// open -- via a "this document has been excluded" diagnostic, not an error
// -- whenever that request fails, which is what
// protocol.UnimplementedClient's own Configuration does by design (returns
// errNotImplemented, turned into a JSON-RPC error response). gopls, vtsls,
// pyright, and bash-language-server all cross-check correctly without ever
// sending this request, so answering it with an empty settings object per
// requested item is a safe default: nothing changes for a server that
// never asks, and the one that requires an answer stops treating an opened
// document as excluded.
//
// taplo is not in the servers map -- deliberately, not merely not-yet-wired:
// taplo's own ranges were measured genuinely disagreeing with
// tree-sitter-toml on nested tables, a real false-positive risk, not a
// normalization gap this package could paper over. This has no
// live caller as a result, but stays: the workspace/configuration behavior
// documented above is specific to taplo's own measured protocol quirk, not
// speculative, and answering it safely costs nothing for every server that
// is wired and never asks.
//
// Dead on every wired path today is therefore deliberate, not debt: this is
// protocol reserve for taplo specifically, worth revisiting only if taplo is
// wired into the servers map for real. internal/lsptest's mockserver has no
// equivalent handler -- see its own doc comment for what that gap in the
// double does and does not cover.
type configClient struct {
	protocol.UnimplementedClient
}

// Configuration returns one empty settings object (JSON "{}") per
// requested item. rgit has no configuration of its own to report -- this
// exists only to give a server that insists on an answer one that resolves
// rather than errors; the specific keys a server's own "section" might ask
// for are never read.
func (configClient) Configuration(_ context.Context, params *protocol.ConfigurationParams) ([]protocol.LSPAny, error) {
	out := make([]protocol.LSPAny, len(params.Items))
	for i := range out {
		out[i] = protocol.LSPAny(`{}`)
	}
	return out, nil
}

// Client is a live LSP session sufficient for the one request rgit needs:
// textDocument/documentSymbol. It is not a general-purpose LSP client — no
// diagnostics, no completions, nothing an editor would want. configClient
// above is the one deliberate exception.
type Client struct {
	conn   jsonrpc2.Conn
	server protocol.Server
	// closeFn tears down the transport. Dial sets it per transport kind:
	// closing just the connection for a shared daemon socket (the daemon
	// outlives this process), or killing the subprocess for a one-shot
	// stdio session (nothing else will ever reuse it — no server outside
	// gopls was measured offering a listen-mode daemon). NewClient
	// defaults it to rwc.Close.
	closeFn func() error
}

// NewClient wraps rwc in the LSP wire protocol (Content-Length framing,
// go.lsp.dev/protocol's union-aware codec) and completes the
// initialize/initialized handshake against root, a filesystem path used to
// build the workspace root URI. handshakeCtx bounds only the handshake
// calls, not the connection's lifetime — see the background-context
// comment a few lines below for why those must not share a context.
//
// Exported so tests can drive a fake server over an in-memory pipe without
// going through Dial's socket-probe/spawn machinery; production callers use
// Dial.
func NewClient(handshakeCtx context.Context, rwc io.ReadWriteCloser, root string) (*Client, error) {
	stream := jsonrpc2.NewStream(rwc)
	// The background context here is deliberate: it governs the
	// connection's read goroutine (jsonrpc2 conn.Go), which must keep
	// running for the client's whole lifetime. Handing it handshakeCtx
	// would tear the connection down the moment the handshake's bounded
	// context expires or its caller cancels it, killing every later
	// DocumentSymbols call too.
	_, conn, server := protocol.NewClient(context.Background(), configClient{}, stream)

	pidValue := os.Getpid()
	if pidValue > math.MaxInt32 {
		_ = conn.Close()
		return nil, fmt.Errorf("lsp: process id %d exceeds LSP int32 range", pidValue)
	}
	pid := int32(pidValue) //nolint:gosec // range check above rejects values outside LSP's int32 ProcessID field
	// The workspace is announced through workspaceFolders, not the rootUri
	// the LSP spec deprecates in its favour. The two are not interchangeable
	// on the wire: a server only reads workspaceFolders if the client says it
	// supports them, so the capability below is what makes the folder visible
	// at all rather than a redundant declaration of it.
	params := &protocol.InitializeParams{
		ProcessID: &pid,
		Capabilities: protocol.ClientCapabilities{
			Workspace: &protocol.WorkspaceClientCapabilities{
				WorkspaceFolders: new(true),
			},
			TextDocument: &protocol.TextDocumentClientCapabilities{
				DocumentSymbol: &protocol.DocumentSymbolClientCapabilities{
					HierarchicalDocumentSymbolSupport: new(true),
				},
			},
		},
	}
	params.WorkspaceFolders = protocol.NewNullable([]protocol.WorkspaceFolder{
		{URI: uri.File(root), Name: filepath.Base(root)},
	})
	if _, err := server.Initialize(handshakeCtx, params); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("lsp: initialize: %w", err)
	}
	if err := server.Initialized(handshakeCtx, &protocol.InitializedParams{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("lsp: initialized: %w", err)
	}

	return &Client{conn: conn, server: server, closeFn: rwc.Close}, nil
}

// Close tears down the client's transport per the rules described on
// Client.closeFn.
func (c *Client) Close() error {
	if c.closeFn == nil {
		return nil
	}
	return c.closeFn()
}

// DocumentSymbols requests textDocument/documentSymbol for path with
// content src and returns the flattened, shape-normalized result. ctx is
// wrapped in queryDeadline internally; callers do not need their own
// timeout for this specific call.
func (c *Client) DocumentSymbols(ctx context.Context, path string, src []byte) ([]Symbol, error) {
	ctx, cancel := context.WithTimeout(ctx, queryDeadline())
	defer cancel()

	langID, ok := LanguageKindFor(path)
	if !ok {
		// resolve.ForExtension has already gated which extensions reach
		// here in practice, but a newly registered grammar can land
		// without this switch being updated for it -- nine grammars ship
		// today and more are planned, so "unreachable" is a claim with a
		// shelf life. Erroring degrades this query cleanly (the same
		// path a crashed or absent server already takes) instead of
		// sending a fabricated languageId a real server would answer
		// nonsensically for.
		return nil, fmt.Errorf("lsp: documentSymbol %s: no LSP languageId for this extension", path)
	}

	docURI := uri.File(path)
	if err := c.server.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        docURI,
			LanguageID: langID,
			Version:    1,
			Text:       string(src),
		},
	}); err != nil {
		return nil, fmt.Errorf("lsp: didOpen %s: %w", path, err)
	}
	// gopls is a long-lived daemon reused across invocations and anchors
	// within one invocation re-open the same file, so every didOpen above
	// must be matched by a didClose here -- otherwise open documents
	// accumulate in the server for as long as it stays up. A
	// fresh, short-lived context derived without cancellation rather than
	// ctx: ctx is already scoped to this one query and may be at or past
	// queryDeadline by the time a slow documentSymbol round trip below
	// returns, which would silently drop this notification exactly when a
	// real server (not the deadline) is the reason it is late.
	defer func(baseCtx context.Context) {
		closeCtx, closeCancel := context.WithTimeout(baseCtx, queryDeadline())
		defer closeCancel()
		_ = c.server.DidClose(closeCtx, &protocol.DidCloseTextDocumentParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
		})
	}(context.WithoutCancel(ctx))

	result, err := c.server.DocumentSymbol(ctx, &protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
	})
	if err != nil {
		return nil, fmt.Errorf("lsp: documentSymbol %s: %w", path, err)
	}
	syms, ok := flatten(result)
	if !ok {
		// protocol.DocumentSymbolResult is a two-member sealed union
		// (DocumentSymbolSlice, SymbolInformationSlice); result reaching
		// neither case means either an explicit LSP "null" response (a
		// legitimate no-info answer per the spec) or, if go.lsp.dev/
		// protocol ever grows a third variant, a shape this package does
		// not understand. Either way this must not silently read as "the
		// file genuinely has zero symbols" -- that is exactly the class
		// of failure the cross-check and this repo's own posture (fail
		// loudly, never continue on a shape you cannot account for) both
		// warn against, and it would be
		// indistinguishable from a real empty outline downstream. Erring
		// out here degrades this query the same way a query error
		// already does, rather than returning a result that looks
		// identical to "nothing to report."
		return nil, fmt.Errorf("lsp: documentSymbol %s: unrecognized result shape %T", path, result)
	}
	trimTrailingBlankLines(src, syms)
	return syms, nil
}

// trimTrailingBlankLines pulls each symbol's own EndLine back past any
// wholly-blank (or whitespace-only) trailing lines within its own range,
// in place. yaml-language-server's nested mapping ranges consistently
// extend one line past their own last real content, through the single
// blank line separating them from a following sibling key at the same
// level -- not block-scalar-specific: a plain-scalar sibling reproduces it
// identically. tree-sitter-yaml's
// own node never does this, stopping at its own last real content line
// instead.
//
// This is the same shape of normalization already applied on the
// tree-sitter side for a doc-comment prefix (declOnlyExtent strips it
// before comparing) -- a well-defined, content-free byte category one side
// includes and the comparison should not penalize -- so it is applied here
// uniformly, to every symbol from every wired server, not special-cased to
// YAML: it can only ever narrow a symbol's own reported EndLine toward its
// StartLine, never grow it, and it stops the instant it reaches a
// non-blank line, so a genuine content-level disagreement (a server that
// actually claims a neighbour's real content, the way taplo's dotted
// sub-table nesting does) is untouched and still fails the cross-check.
//
// The very last element bytes.Split produces is deliberately never
// trimmed: for a symbol with no following sibling, both tree-sitter and
// the server extend through the file's own trailing newline all the way to
// that final (often empty) element -- consuming it, not separating
// anything from a sibling. Trimming it would manufacture a mismatch on
// every last declaration in a file.
func trimTrailingBlankLines(src []byte, syms []Symbol) {
	lines := bytes.Split(src, []byte{'\n'})
	lastIdx := len(lines) - 1
	for i := range syms {
		end := syms[i].EndLine
		for end > syms[i].StartLine && int(end) < lastIdx && len(bytes.TrimSpace(lines[end])) == 0 {
			end--
		}
		syms[i].EndLine = end
	}
}

// flatten normalizes DocumentSymbolResult's two possible shapes —
// DocumentSymbolSlice (a tree, via Children) and SymbolInformationSlice (a
// flat list with a Location) — into one []Symbol. ok=false means result
// matched neither: an explicit LSP "null" (nil interface) or, if
// go.lsp.dev/protocol ever adds a third union member, a shape this
// function does not recognize. Callers must not treat that the same as a
// recognized-but-genuinely-empty result — see DocumentSymbols's own use of
// this return.
func flatten(result protocol.DocumentSymbolResult) (syms []Symbol, ok bool) {
	switch v := result.(type) {
	case protocol.DocumentSymbolSlice:
		return flattenTree([]protocol.DocumentSymbol(v), ""), true
	case protocol.SymbolInformationSlice:
		return flattenFlat([]protocol.SymbolInformation(v)), true
	default:
		return nil, false
	}
}

// lastLine converts an LSP range end, which is exclusive, into the last line
// the range actually covers. A range ending at character 0 stops before that
// line, so the final covered line is the one above it.
//
// Servers differ sharply here and only one side of the difference is visible
// in a hand-written fixture: gopls ends a declaration mid-line on its closing
// brace, so its End.Character is never 0 and a verbatim copy is right by
// accident. yaml-language-server ends every non-terminal block mapping at
// column 0 of the following line, where a verbatim copy overstates the extent
// by exactly one line -- measured as 81% of all cross-check disagreements
// across a real corpus, every one of them the comparison blaming a correct
// tree-sitter extent.
func lastLine(r protocol.Range) uint32 {
	if r.End.Character == 0 && r.End.Line > r.Start.Line {
		return r.End.Line - 1
	}
	return r.End.Line
}

func flattenTree(syms []protocol.DocumentSymbol, container string) []Symbol {
	out := make([]Symbol, 0, len(syms))
	for _, s := range syms {
		out = append(out, Symbol{
			Name:      s.Name,
			Container: container,
			StartLine: s.Range.Start.Line,
			EndLine:   lastLine(s.Range),
		})
		if len(s.Children) > 0 {
			out = append(out, flattenTree(s.Children, s.Name)...)
		}
	}
	return out
}

func flattenFlat(syms []protocol.SymbolInformation) []Symbol {
	out := make([]Symbol, 0, len(syms))
	for _, s := range syms {
		container := ""
		if s.ContainerName != nil {
			container = *s.ContainerName
		}
		out = append(out, Symbol{
			Name:      s.Name,
			Container: container,
			StartLine: s.Location.Range.Start.Line,
			EndLine:   lastLine(s.Location.Range),
		})
	}
	return out
}

// LanguageKindFor maps a file extension to the LSP languageId didOpen
// requires. ok=false means this package has no mapping for path's
// extension. resolve.ForExtension has already gated which extensions reach
// here in practice, so this should never fire today -- but a newly
// registered grammar reaching Dial without a matching case here must not
// silently didOpen as some other language a real server would then answer
// nonsensically for; the caller degrades instead.
//
// Exported so internal/resolve's own drift guard
// (TestServerLanguages_HaveLanguageKindMapping) can call it directly rather
// than a second, hand-maintained copy of this switch: internal/lsp cannot
// import internal/resolve (resolve already imports lsp, for
// CrossCheckExtents' Dial call), so that guard has to reach in from the
// resolve side, which needs this exported.
func LanguageKindFor(path string) (kind protocol.LanguageKind, ok bool) {
	switch filepath.Ext(path) {
	case ".go":
		return protocol.LanguageKindGo, true
	case ".ts", ".mts", ".cts":
		return protocol.LanguageKindTypeScript, true
	case ".tsx":
		return protocol.LanguageKindTypeScriptReact, true
	case ".jsx":
		return protocol.LanguageKindJavaScriptReact, true
	case ".js", ".mjs", ".cjs":
		return protocol.LanguageKindJavaScript, true
	case ".py", ".pyi":
		return protocol.LanguageKindPython, true
	case ".rs":
		return protocol.LanguageKindRust, true
	case ".sh", ".bash":
		return protocol.LanguageKindShellScript, true
	case ".yaml", ".yml":
		return protocol.LanguageKindYAML, true
	case ".json":
		return protocol.LanguageKindJSON, true
	case ".css":
		return protocol.LanguageKindCSS, true
	case ".md", ".markdown":
		return protocol.LanguageKindMarkdown, true
	case ".html", ".htm":
		return protocol.LanguageKindHTML, true
	default:
		return "", false
	}
}
