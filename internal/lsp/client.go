package lsp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// configClient answers workspace/configuration, the one server-initiated
// request measured necessary for correct behaviour rather than added
// speculatively: taplo (TOML) silently reports zero symbols for a document
// it has just been told to open -- via a "this document has been excluded"
// diagnostic, not an error -- whenever that request fails, which is what
// protocol.UnimplementedClient's own Configuration does by design (returns
// errNotImplemented, turned into a JSON-RPC error response). Verified this
// is what was happening: dialling a real taplo through NewClient's prior
// protocol.UnimplementedClient{} produced a successful but empty
// DocumentSymbols result, and a raw-protocol probe against the same binary
// showed the exclusion diagnostic appearing only when workspace/
// configuration errored, disappearing once it was answered instead. gopls,
// vtsls, pyright, and bash-language-server all cross-check correctly
// without ever sending this request, so answering it with an empty
// settings object per requested item is a safe default: nothing changes
// for a server that never asks, and the one that requires an answer stops
// treating an opened document as excluded.
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
// above is the one deliberate, measured exception.
type Client struct {
	conn   jsonrpc2.Conn
	server protocol.Server
	// closeFn tears down the transport. Dial sets it per transport kind:
	// closing just the connection for a shared daemon socket (the daemon
	// outlives this process), or killing the subprocess for a one-shot
	// stdio session (nothing else will ever reuse it — specs/design.md's
	// transport-support measurement). NewClient defaults it to rwc.Close.
	closeFn func() error
}

// NewClient wraps rwc in the LSP wire protocol (Content-Length framing,
// go.lsp.dev/protocol's union-aware codec) and completes the
// initialize/initialized handshake against root, a filesystem path used to
// build the workspace root URI. handshakeCtx bounds only the handshake
// calls, not the connection's lifetime — see the comment on the internal
// connCtx use in Dial for why those must not share a context.
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

	rootURI := uri.File(root)
	pid := int32(os.Getpid())
	if _, err := server.Initialize(handshakeCtx, &protocol.InitializeParams{
		ProcessID: &pid,
		RootURI:   &rootURI,
		Capabilities: protocol.ClientCapabilities{
			TextDocument: &protocol.TextDocumentClientCapabilities{
				DocumentSymbol: &protocol.DocumentSymbolClientCapabilities{
					HierarchicalDocumentSymbolSupport: new(true),
				},
			},
		},
	}); err != nil {
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
// wrapped in QueryDeadline internally; callers do not need their own
// timeout for this specific call.
func (c *Client) DocumentSymbols(ctx context.Context, path string, src []byte) ([]Symbol, error) {
	ctx, cancel := context.WithTimeout(ctx, QueryDeadline)
	defer cancel()

	docURI := uri.File(path)
	if err := c.server.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        docURI,
			LanguageID: languageKindFor(path),
			Version:    1,
			Text:       string(src),
		},
	}); err != nil {
		return nil, fmt.Errorf("lsp: didOpen %s: %w", path, err)
	}

	result, err := c.server.DocumentSymbol(ctx, &protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
	})
	if err != nil {
		return nil, fmt.Errorf("lsp: documentSymbol %s: %w", path, err)
	}
	return flatten(result), nil
}

// flatten normalizes DocumentSymbolResult's two possible shapes —
// DocumentSymbolSlice (a tree, via Children) and SymbolInformationSlice (a
// flat list with a Location) — into one []Symbol. The shape
// distinction was validated against a live gopls.
func flatten(result protocol.DocumentSymbolResult) []Symbol {
	switch v := result.(type) {
	case protocol.DocumentSymbolSlice:
		return flattenTree([]protocol.DocumentSymbol(v), "")
	case protocol.SymbolInformationSlice:
		return flattenFlat([]protocol.SymbolInformation(v))
	default:
		return nil
	}
}

func flattenTree(syms []protocol.DocumentSymbol, container string) []Symbol {
	out := make([]Symbol, 0, len(syms))
	for _, s := range syms {
		out = append(out, Symbol{
			Name:      s.Name,
			Container: container,
			StartLine: s.Range.Start.Line,
			EndLine:   s.Range.End.Line,
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
			EndLine:   s.Location.Range.End.Line,
		})
	}
	return out
}

// languageKindFor maps a file extension to the LSP languageId didOpen
// requires. resolve.ForExtension has already gated which extensions reach
// here, so the fallback is unreachable in practice, not a real case.
func languageKindFor(path string) protocol.LanguageKind {
	switch filepath.Ext(path) {
	case ".go":
		return protocol.LanguageKindGo
	case ".ts", ".mts", ".cts":
		return protocol.LanguageKindTypeScript
	case ".tsx":
		return protocol.LanguageKindTypeScriptReact
	case ".jsx":
		return protocol.LanguageKindJavaScriptReact
	case ".js", ".mjs", ".cjs":
		return protocol.LanguageKindJavaScript
	case ".py", ".pyi":
		return protocol.LanguageKindPython
	case ".sh", ".bash":
		return protocol.LanguageKindShellScript
	case ".yaml", ".yml":
		return protocol.LanguageKindYAML
	case ".json":
		return protocol.LanguageKindJSON
	case ".css":
		return protocol.LanguageKindCSS
	case ".md", ".markdown":
		return protocol.LanguageKindMarkdown
	default:
		return protocol.LanguageKindTypeScript
	}
}
