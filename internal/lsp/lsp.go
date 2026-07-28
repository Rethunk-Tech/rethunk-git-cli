// Package lsp is a minimal language-server client: enough JSON-RPC and
// textDocument/documentSymbol handling to cross-check a tree-sitter extent
// against a live gopls, vtsls, or pyright, and no more. internal/resolve is
// the only caller; tree-sitter, not this package, always produces the
// extent that gets staged (AGENTS.md § Resolution model) — a language
// server here only verifies.
package lsp

import "time"

// DialBudget bounds how long Dial waits to reach a live daemon socket
// before giving up and reporting degraded=true (specs/design.md).
const DialBudget = 150 * time.Millisecond

// QueryDeadline bounds a single textDocument/documentSymbol round trip once
// connected (specs/design.md).
//
// 2s, not the 250ms the design originally specified. That figure was derived
// from a warm gopls daemon, where a query returns in single-digit
// milliseconds. It does not survive contact with the stdio servers: vtsls
// answers its first documentSymbol after didOpen in roughly half a second and
// beat 250ms on no measured run, so the TypeScript cross-check degraded to
// [ts-only] on every real invocation — present in the code, absent in effect.
// A deadline that only ever fires is not a budget, it is a disabled feature.
const QueryDeadline = 2 * time.Second

// Symbol is one language-server-reported document symbol, flattened out of
// LSP's DocumentSymbolResult union (a tree of DocumentSymbol or a flat list
// of SymbolInformation — go.lsp.dev/protocol models both as a sealed
// interface, and a client that assumes one shape decodes the other
// wrongly) and reduced to what the cross-check needs.
//
// Name is the server's own spelling, unnormalized — gopls reports methods
// as "(*A).Get" with no container; vtsls and pyright report a container
// name separately. Comparison-side normalization lives in internal/resolve,
// which already owns rgit's anchor-qualification rules.
type Symbol struct {
	Name      string
	Container string
	// StartLine and EndLine are 0-based, matching LSP's Position.Line.
	StartLine uint32
	EndLine   uint32
}
