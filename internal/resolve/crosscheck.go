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

// CrossCheckExtents verifies a whole file's worth of resolutions against a
// single language-server query. Tree-sitter already produced the extents
// that get staged; the server's ranges are never substituted for them. One
// round trip covers the whole list: `rgit diff` resolves every declaration
// in every changed file, and asking per anchor would put a language server
// in the middle of the fast path.
//
// Callers must not pass deletions -- the symbol exists only in HEAD,
// outside the server's worktree view. Pseudo resolutions are exempt for the
// same reason (specs/design.md § Cross-check exemptions) and are dropped
// before dialling, so a list with nothing else in it costs no round trip.
//
// degraded=true means no comparison happened at all: absent or slow server,
// unsupported language, or an outline that omitted at least one of list's
// own non-pseudo resolutions -- "not named" degrades rather than counting
// as verified. None is a failure; the caller prints "[ts-only]" and
// proceeds. mismatches holds one error per resolution whose range the
// server disagreed with (exit 6); a resolution the server does not name at
// all is not a mismatch (specs/design.md's fourth exemption), but still
// marks the batch as degraded.
func CrossCheckExtents(ctx context.Context, sess *lsp.Session, lang Language, repoRoot, absPath string, src []byte, list []*Resolution) (degraded bool, mismatches []error) {
	if len(list) == 0 {
		return true, nil
	}
	// Pseudo resolutions never reach a verdict, so a list holding nothing
	// else has nothing to ask about -- exempt, not degraded, and not worth
	// a round trip to discover.
	comparable := false
	for _, res := range list {
		if res != nil && !res.Pseudo {
			comparable = true
			break
		}
	}
	if !comparable {
		return false, nil
	}
	client, deg := sess.Dial(ctx, lang.Name(), repoRoot)
	if deg {
		return true, nil
	}
	symbols, err := client.DocumentSymbols(ctx, absPath, src)
	if err != nil {
		return true, nil
	}

	return crossCheckVerdict(src, list, symbols)
}

// crossCheckVerdict evaluates every non-nil, non-pseudo resolution in list
// against symbols -- a document-symbol table already fetched once, by
// either caller -- and reports the same (degraded, mismatches) shape
// CrossCheckExtents returns directly and CrossCheckExtent derives its own
// two-value return from for a single-resolution list.
//
// This is the one piece of logic factored out specifically so the
// per-anchor and batch forms cannot independently drift on what "not
// found" or "a mismatch" means for identical input: both dial, check
// res.Pseudo or an empty list, and fetch symbols entirely on their own,
// but neither decides a verdict without coming through here.
// TestCrossCheckVerdict_AgreesPerAnchorAndBatch (crosscheck_test.go) drives
// this directly with a shared mock symbol list, which is the closest a
// mock can get to proving the two public forms agree without a live dial.
func crossCheckVerdict(src []byte, list []*Resolution, symbols []lsp.Symbol) (degraded bool, mismatches []error) {
	// evaluated tracks whether any resolution actually reached
	// MatchAndCompare. A list that is non-empty but every entry nil or
	// Pseudo (a file whose only cross-checked resolution is @imports, say)
	// has nothing to compare -- the same fourth cross-check exemption
	// CrossCheckExtents applies before dialling. Reaching here means a
	// resolution went nil after that pre-filter, so this is exempt, not
	// degraded, rather than reporting "[ts-only]" for a list this package
	// itself declares has nothing to verify. allFound's
	// own zero value would otherwise be vacuously true when the loop below
	// never runs a real comparison -- reporting "not degraded, no
	// mismatches" while claiming a genuine verification took place, a false
	// positive this early return prevents.
	evaluated := false
	allFound := true
	for _, res := range list {
		if res == nil || res.Pseudo {
			continue
		}
		evaluated = true
		found, cerr := MatchAndCompare(src, res, symbols)
		if !found {
			allFound = false
			continue
		}
		if cerr != nil {
			mismatches = append(mismatches, cerr)
		}
	}
	if !evaluated {
		return false, nil
	}
	return !allFound, mismatches
}

// MatchAndCompare is CrossCheckExtents' comparison, factored out so it can
// be driven with an already-fetched symbol table instead of a live
// connection -- the seam resolver_test.go's mock-server and normalization
// coverage uses, since a mock cannot exercise Dial's real socket/subprocess
// machinery but can exercise everything this function does.
//
// found=false means symbols simply does not name res.Anchor (see
// CrossCheckExtents' doc on the fourth cross-check exemption); err is
// non-nil only when a match was found and its range disagreed.
func MatchAndCompare(src []byte, res *Resolution, symbols []lsp.Symbol) (found bool, err error) {
	match, ok := matchLSPSymbol(res.Anchor, res.Sep, res.Flat, res.SlugAnchors, res.GroupedAnchors, res.SameName, symbols)
	if !ok {
		return false, nil
	}

	end := lastOffset(res.DeclOnly)
	if res.SlugAnchors {
		end = lastContentOffset(src, res.DeclOnly.Start, end)
	}
	wantStart, startOK := lineOf(src, res.DeclOnly.Start)
	wantEnd, endOK := lineOf(src, end)
	if !startOK || !endOK {
		// Not a real tree-sitter/language-server disagreement: res itself
		// names an offset past the end of src, which every offset this
		// package hands lineOf today cannot do -- a Resolution's DeclOnly
		// extent always comes from a Declaration parsed out of this exact
		// src (index.go's buildIndex). Reaching here means that invariant
		// itself broke -- a corrupted *Resolution, not a legitimate input --
		// so it is reported plainly, the same "internal inconsistency"
		// phrasing internal/diff's own resolveRegions uses for its own
		// unreachable-in-practice guard, rather than folded into
		// exitcode.ExtentMismatch below: that code and its message are
		// reserved for an actual tree-sitter/language-server range
		// disagreement, and reusing it here (with no real TreeSitterRange
		// or LSPRange to report) would misdescribe corruption as one.
		// found=true, not false: crossCheckVerdict drops err entirely on
		// found=false (that shape means "the server never named this
		// symbol", nothing to report), and silently discarding this is
		// exactly the "worse than failing loudly" outcome resolveRegions'
		// own comment argues against.
		return true, fmt.Errorf("resolve: internal inconsistency: %q declOnly extent [%d,%d) exceeds source length %d",
			res.Anchor, res.DeclOnly.Start, res.DeclOnly.End, len(src))
	}
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

// lastOffset is the offset of the final byte an extent covers. Extents are
// half-open [Start, End), so the byte at End belongs to whatever follows --
// for a YAML block mapping or a Markdown section that is the first byte of
// the next sibling, one line further down. Converting End directly would
// report an extent one line longer than the one rgit itself stages and
// blames, which is what internal/app's lineRange already avoids the same way.
func lastOffset(ext Extent) uint {
	if ext.End > ext.Start {
		return ext.End - 1
	}
	return ext.End
}

// lastContentOffset walks back from end over trailing whitespace, stopping at
// start. A section in a prose format runs to the blank line before the next
// heading, while a language server reports it ending at its last line of
// actual content; both readings are defensible, so the blank tail is dropped
// before comparing rather than reported as a disagreement neither side owns.
func lastContentOffset(src []byte, start, end uint) uint {
	if end >= uint(len(src)) {
		return end
	}
	for end > start && isSpaceByte(src[end]) {
		end--
	}
	return end
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// lineOf converts a byte offset to a 0-based line number, matching LSP's
// Position.Line convention directly so callers never juggle a 1-based/
// 0-based mismatch across the comparison. ok=false means offset exceeds
// len(src), which src[:offset] would otherwise panic on rather than report.
func lineOf(src []byte, offset uint) (line uint32, ok bool) {
	if offset > uint(len(src)) {
		return 0, false
	}
	return uint32(bytes.Count(src[:offset], []byte{'\n'})), true
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
// rgit supports. sameName gates that fallback: it is the resolver's own
// count of declarations sharing the pre-ordinal name, and the Nth on each
// side is the same declaration only when the two counts agree.
// flat means res.Flat: the resolving language's own Container is not a real
// ancestor (Language.FlatContainer, lang.go's own doc comment), so a
// server-reported symbol's genuine containerName must never be joined onto
// its Name before comparing. HTML's resolver emits "tag#id", while the
// language server may append ".class" selectors to that same Name; strip
// those server-only suffixes before comparing. qualifyLSPSymbol's join is for
// every other language, where Container really is an ancestor a server also
// reports as one.
func matchLSPSymbol(anchor, sep string, flat, slugAnchors, groupedAnchors bool, sameName int, symbols []lsp.Symbol) (lsp.Symbol, bool) {
	bare, ordinal, hasOrdinal := ParseOrdinal(anchor)

	var byBare []lsp.Symbol
	for _, s := range symbols {
		qualified := s.Name
		if flat {
			if classStart := strings.IndexByte(qualified, '.'); classStart >= 0 {
				qualified = qualified[:classStart]
			}
		} else {
			qualified = qualifyLSPSymbol(s, sep)
		}
		if qualified == anchor {
			return s, true
		}
		if slugAnchors && slugQualified(s, sep) == anchor {
			return s, true
		}
		if groupedAnchors && anchorNamesGroupMember(anchor, qualified) {
			return s, true
		}
		if hasOrdinal && (qualified == bare || s.Name == bare) {
			byBare = append(byBare, s)
		}
	}

	// An ordinal names the Nth declaration the resolver found, so indexing
	// the server's list by it only selects the same declaration when both
	// sides found the same number. They routinely do not: a server may
	// split one declaration into several symbols, report a construct the
	// resolver does not treat as a declaration at all, or emit the same
	// symbol twice -- each of which shifts every later ordinal and pairs
	// two unrelated declarations, producing a disagreement in which neither
	// side is wrong. Falling through to "the server does not name this
	// anchor" degrades to [ts-only], which is the honest answer.
	if hasOrdinal && len(byBare) == sameName && ordinal <= len(byBare) {
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
// anchorNamesGroupMember reports whether qualified is one of the
// comma-separated names anchor carries. A CSS rule is anchored by its whole
// selector list while the server reports one symbol per selector, each with
// that same rule's range, so any member identifies the same declaration.
//
// The members are compared whole, never by prefix: ".button" must not match
// ".button-primary" simply because one string contains the other.
func anchorNamesGroupMember(anchor, qualified string) bool {
	if !strings.Contains(anchor, ",") {
		return false
	}
	for member := range strings.SplitSeq(anchor, ",") {
		if strings.TrimSpace(member) == qualified {
			return true
		}
	}
	return false
}

// slugQualified is qualifyLSPSymbol for a language whose anchors are slugs
// of human-readable text. Each part is slugified before the join, never the
// joined string: the separator is not part of either name and slugify would
// fold it into a hyphen.
func slugQualified(s lsp.Symbol, sep string) string {
	if s.Container == "" {
		return slugify(s.Name)
	}
	return joinQualified(slugify(s.Container), slugify(s.Name), sep)
}

func qualifyLSPSymbol(s lsp.Symbol, sep string) string {
	if s.Container == "" {
		return normalizeAnchorInput(s.Name)
	}
	return joinQualified(s.Container, s.Name, sep)
}

// ParseOrdinal parses docs/ANCHORS.md's positional "Bare#N" anchor form:
// "init#2" separates into ("init", 2, true); "div#app#2" into ("div#app", 2,
// true) because HTML's own Sep is "#" and the bare name already contains
// one. The split is on the *last* "#": only a trailing positive-integer
// suffix is the ordinal. ok=false means no such suffix (no "#", empty bare,
// or a non-positive / non-integer tail) -- bare/n are meaningless then.
//
// Exported so internal/synth's own ordinal check (stage.go's
// isOrdinalAnchor) can share this one parse instead of maintaining a
// second copy with its own, slightly different rules -- the divergence
// this replaces: isOrdinalAnchor required n > 0 and a non-empty bare name,
// matchLSPSymbol's own former splitOrdinal checked neither.
func ParseOrdinal(anchor string) (bare string, n int, ok bool) {
	i := strings.LastIndexByte(anchor, '#')
	if i <= 0 {
		return "", 0, false
	}
	before, after := anchor[:i], anchor[i+1:]
	parsed, err := strconv.Atoi(after)
	if err != nil || parsed <= 0 {
		return "", 0, false
	}
	return before, parsed, true
}
