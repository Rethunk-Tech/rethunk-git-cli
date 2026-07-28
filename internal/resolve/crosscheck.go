package resolve

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
)

// CrossCheckExtent verifies res's declaration-only extent against a live
// language server. Tree-sitter already produced the extent that gets
// staged; the server's range is never substituted for it.
//
// Callers must not invoke this for deletions -- the symbol exists only in
// HEAD, outside the server's worktree view. res.Pseudo is exempt for the
// same reason and is checked here (specs/design.md § Cross-check
// exemptions).
//
// degraded=true means no comparison happened: absent or slow server,
// unsupported language, or a symbol its outline does not name. None is a
// failure -- the caller prints "[ts-only]" and proceeds. err is non-nil
// only for a genuine range disagreement (exit 6).
func CrossCheckExtent(ctx context.Context, sess *lsp.Session, lang Language, repoRoot, absPath string, src []byte, res *Resolution, isDeletion bool) (degraded bool, err error) {
	if res.Pseudo || isDeletion {
		return true, nil
	}

	// The session owns the client and closes it once per invocation.
	client, deg := sess.Dial(ctx, lang.Name(), repoRoot)
	if deg {
		return true, nil
	}

	symbols, err := client.DocumentSymbols(ctx, absPath, src)
	if err != nil {
		// A live client that then fails mid-query (crash, protocol error)
		// is exactly as uninformative as no client at all -- degrade.
		return true, nil
	}

	found, cmpErr := MatchAndCompare(src, res, symbols)
	if !found {
		return true, nil
	}
	return false, cmpErr
}

// CrossCheckExtents verifies a whole file's worth of resolutions against a
// single language-server query. CrossCheckExtent dials and asks for the
// document's symbols per anchor, which is right when there is one anchor
// and wrong when there are dozens: `rgit diff` resolves every declaration in
// every changed file, and one round trip per symbol would put a language
// server in the middle of the fast path.
//
// degraded=true means no comparison happened at all, exactly as for the
// single-anchor form. mismatches holds one error per resolution whose range
// the server disagreed with; a resolution the server does not name at all is
// not a mismatch (specs/design.md's fourth exemption).
func CrossCheckExtents(ctx context.Context, sess *lsp.Session, lang Language, repoRoot, absPath string, src []byte, list []*Resolution) (degraded bool, mismatches []error) {
	if len(list) == 0 {
		return true, nil
	}
	client, deg := sess.Dial(ctx, lang.Name(), repoRoot)
	if deg {
		return true, nil
	}
	symbols, err := client.DocumentSymbols(ctx, absPath, src)
	if err != nil {
		return true, nil
	}

	for _, res := range list {
		if res == nil || res.Pseudo {
			continue
		}
		if _, cerr := MatchAndCompare(src, res, symbols); cerr != nil {
			mismatches = append(mismatches, cerr)
		}
	}
	return false, mismatches
}

// MatchAndCompare is CrossCheckExtent's comparison, factored out so it can
// be driven with an already-fetched symbol table instead of a live
// connection -- the seam resolver_test.go's mock-server and normalization
// coverage uses, since a mock cannot exercise Dial's real socket/subprocess
// machinery but can exercise everything this function does.
//
// found=false means symbols simply does not name res.Anchor (see
// CrossCheckExtent's doc on the fourth cross-check exemption); err is
// non-nil only when a match was found and its range disagreed.
func MatchAndCompare(src []byte, res *Resolution, symbols []lsp.Symbol) (found bool, err error) {
	match, ok := matchLSPSymbol(res.Anchor, symbols)
	if !ok {
		return false, nil
	}

	wantStart, wantEnd := lineOf(src, res.DeclOnly.Start), lineOf(src, res.DeclOnly.End)
	if wantStart == match.StartLine && wantEnd == match.EndLine {
		return true, nil
	}

	return true, &ResolveError{
		Code:            exitcode.ExtentMismatch,
		Anchor:          res.Anchor,
		TreeSitterRange: formatRange(wantStart, wantEnd),
		LSPRange:        formatRange(match.StartLine, match.EndLine),
	}
}

// lineOf converts a byte offset to a 0-based line number, matching LSP's
// Position.Line convention directly so callers never juggle a 1-based/
// 0-based mismatch across the comparison.
func lineOf(src []byte, offset uint) uint32 {
	return uint32(bytes.Count(src[:offset], []byte{'\n'}))
}

func formatRange(start, end uint32) string {
	return fmt.Sprintf("L%d..L%d", start+1, end+1)
}

// matchLSPSymbol finds the server-reported symbol corresponding to anchor,
// the qualified name Resolve produced (docs/ANCHORS.md's Container.Bare
// form, a bare name, or a Bare#Ordinal fallback).
//
// Two server shapes need normalizing to that same form before comparison:
// a containerName-bearing symbol (vtsls, pyright, for a nested member)
// becomes Container.Bare directly, and gopls's own receiver spelling,
// "(*A).Get" with no containerName at all, is normalized by the same
// normalizeAnchorInput used for anchor input (index.go) since it is
// exactly the same string transform.
//
// Ordinal anchors ("init#2") have no literal match in either server's
// output -- both report every overload/repeat under the identical bare
// name -- so they fall back to matching the Nth same-named symbol in the
// server's own reported order, which is source order for every grammar
// rgit supports.
func matchLSPSymbol(anchor string, symbols []lsp.Symbol) (lsp.Symbol, bool) {
	bare, ordinal := splitOrdinal(anchor)

	var byBare []lsp.Symbol
	for _, s := range symbols {
		qualified := qualifyLSPSymbol(s)
		if qualified == anchor {
			return s, true
		}
		if ordinal > 0 && (qualified == bare || s.Name == bare) {
			byBare = append(byBare, s)
		}
	}

	if ordinal > 0 && ordinal <= len(byBare) {
		return byBare[ordinal-1], true
	}
	return lsp.Symbol{}, false
}

func qualifyLSPSymbol(s lsp.Symbol) string {
	if s.Container != "" {
		return s.Container + "." + s.Name
	}
	return normalizeAnchorInput(s.Name)
}

// splitOrdinal separates "init#2" into ("init", 2); a name with no "#"
// returns (anchor, 0).
func splitOrdinal(anchor string) (bare string, ordinal int) {
	before, after, ok := strings.Cut(anchor, "#")
	if !ok {
		return anchor, 0
	}
	n, err := strconv.Atoi(after)
	if err != nil {
		return anchor, 0
	}
	return before, n
}
