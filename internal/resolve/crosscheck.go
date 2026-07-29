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
func CrossCheckExtent(ctx context.Context, sess *lsp.Session, lang Language, repoRoot, absPath string, src []byte, res *Resolution) (degraded bool, err error) {
	if res.Pseudo {
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
// single-anchor form -- including when the server answered but its outline
// omitted at least one of list's own non-pseudo resolutions, aligning this
// batch form with CrossCheckExtent's per-anchor found=false case above
// (:45-47), which also degrades rather than treating "not named" as
// verified. mismatches holds one error per resolution whose range the
// server disagreed with; a resolution the server does not name at all is
// not a mismatch (specs/design.md's fourth exemption), but still marks the
// batch as degraded.
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

	allFound := true
	for _, res := range list {
		if res == nil || res.Pseudo {
			continue
		}
		found, cerr := MatchAndCompare(src, res, symbols)
		if !found {
			allFound = false
			continue
		}
		if cerr != nil {
			mismatches = append(mismatches, cerr)
		}
	}
	return !allFound, mismatches
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
	match, ok := matchLSPSymbol(res.Anchor, res.Sep, symbols)
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
func matchLSPSymbol(anchor, sep string, symbols []lsp.Symbol) (lsp.Symbol, bool) {
	bare, ordinal, hasOrdinal := ParseOrdinal(anchor)

	var byBare []lsp.Symbol
	for _, s := range symbols {
		qualified := qualifyLSPSymbol(s, sep)
		if qualified == anchor {
			return s, true
		}
		if hasOrdinal && (qualified == bare || s.Name == bare) {
			byBare = append(byBare, s)
		}
	}

	if hasOrdinal && ordinal <= len(byBare) {
		return byBare[ordinal-1], true
	}
	return lsp.Symbol{}, false
}

// qualifyLSPSymbol joins s's own Container and Name via joinQualified
// (index.go) -- sep verbatim when the resolution supplied one (CSS
// Nesting's " ", Declaration.Sep's own doc comment), "." otherwise -- so a
// server-reported symbol qualifies to exactly the string Resolve would
// have emitted as Anchor for the same declaration. Unlike
// containerQualified, an empty Container here falls back to
// normalizeAnchorInput, not the bare name verbatim: gopls's own receiver
// spelling ("(*A).Get") arrives with no containerName field at all, so
// this is the one place a server-reported symbol's own name still needs
// the same normalization anchor input already gets.
func qualifyLSPSymbol(s lsp.Symbol, sep string) string {
	if s.Container == "" {
		return normalizeAnchorInput(s.Name)
	}
	return joinQualified(s.Container, s.Name, sep)
}

// ParseOrdinal parses docs/ANCHORS.md's positional "Bare#N" anchor form:
// "init#2" separates into ("init", 2, true). ok=false means anchor does not
// use this form at all -- no "#", an empty bare name before it, or a suffix
// that is not a positive integer -- and bare/n are meaningless. No
// identifier in a supported grammar contains "#", so a suffix that parses
// as a positive integer is unambiguous.
//
// Exported so internal/synth's own ordinal check (stage.go's
// isOrdinalAnchor) can share this one parse instead of maintaining a
// second copy with its own, slightly different rules -- the divergence
// this replaces: isOrdinalAnchor required n > 0 and a non-empty bare name,
// matchLSPSymbol's own former splitOrdinal checked neither.
func ParseOrdinal(anchor string) (bare string, n int, ok bool) {
	before, after, hasHash := strings.Cut(anchor, "#")
	if !hasHash || before == "" {
		return "", 0, false
	}
	parsed, err := strconv.Atoi(after)
	if err != nil || parsed <= 0 {
		return "", 0, false
	}
	return before, parsed, true
}
