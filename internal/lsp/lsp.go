// Package lsp is a minimal language-server client: enough JSON-RPC and
// textDocument/documentSymbol handling to cross-check a tree-sitter extent
// against a live gopls, vtsls, or pyright, and no more. internal/resolve is
// the only caller; tree-sitter, not this package, always produces the
// extent that gets staged (AGENTS.md § Resolution model) — a language
// server here only verifies.
package lsp

import (
	"os"
	"time"
)

// defaultDialBudget bounds how long Dial waits to reach a live daemon socket
// before giving up and reporting degraded=true (specs/design.md), unless
// overridden by RGIT_LSP_DIAL_TIMEOUT.
//
// Unexported: no caller outside this package reaches Dial's timing directly
// (n13 in the 2026-07-29 audit found none when this was still exported) --
// internal/resolve calls CrossCheckExtent/CrossCheckExtents, never Dial or
// these constants themselves.
const defaultDialBudget = 150 * time.Millisecond

// defaultQueryDeadline bounds a single textDocument/documentSymbol round
// trip once connected (specs/design.md), unless overridden by
// RGIT_LSP_QUERY_TIMEOUT.
//
// 2s, not a tighter budget: a warm gopls daemon answers in single-digit
// milliseconds, but vtsls's first documentSymbol after didOpen can take
// roughly half a second, and a deadline a real server routinely misses
// degrades the cross-check to [ts-only] on every invocation instead of
// occasionally -- a deadline that only ever fires is not a budget, it is a
// disabled feature.
//
// Unexported alongside defaultDialBudget -- see its comment.
const defaultQueryDeadline = 2 * time.Second

// dialBudget and queryDeadline read their env override on every call, not
// once at package init, so RGIT_LSP_DIAL_TIMEOUT/RGIT_LSP_QUERY_TIMEOUT
// apply process-wide the instant they are set and t.Setenv works in tests.
// Slow hosts or a cold gopls index may need more than the defaults without
// recompiling (docs/INSTALL.md § Environment variables).
func dialBudget() time.Duration { return durationEnv("RGIT_LSP_DIAL_TIMEOUT", defaultDialBudget) }

func queryDeadline() time.Duration {
	return durationEnv("RGIT_LSP_QUERY_TIMEOUT", defaultQueryDeadline)
}

// durationEnv fails closed: a missing var, a malformed value, or a
// zero/negative duration all fall back to def rather than disabling the
// timeout it exists to enforce.
func durationEnv(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

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
