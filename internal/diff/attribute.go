package diff

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strconv"

	udiff "github.com/aymanbagabas/go-udiff"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// region is one named, addressable byte range in a source file: a real
// declaration from resolve.DeclOrder, or the @header/@imports pseudo-
// anchors — the two pseudo-anchors that own bytes no real declaration does
// (docs/ANCHORS.md). @toplevel is deliberately excluded: it spans every
// declaration's own extent, so including it would double-report every
// change under two names.
type region struct {
	name string
	ext  resolve.Extent
}

// declResolver is the subset of *resolve.File that resolveRegions needs --
// deliberately an interface rather than a concrete *resolve.File parameter,
// so attributeSymbolsOpen (below) can be handed a file buildFileReport
// (run.go) already parsed for its own LSP cross-check instead of forcing a
// second parse of the same bytes. The real implementation, *resolve.File,
// can never actually take the error path resolveRegions guards against:
// DeclOrder and Resolve are both built from the same index in the same
// call, so a name DeclOrder emits always hits Resolve's first lookup. That
// is exactly why this seam exists -- it lets a test double violate the
// invariant deliberately, something no real resolve.Language can do, to
// prove the guard fires and propagates rather than silently dropping a
// region. The assertion below is what would catch this interface drifting
// from *resolve.File's real signatures: a future rename or signature
// change on either method fails this file to compile rather than leaving
// the seam quietly stale.
type declResolver interface {
	DeclOrder() []string
	Resolve(anchor string) (*resolve.Resolution, error)
}

var _ declResolver = (*resolve.File)(nil)

// resolveRegions is attributeSymbolsOpen's own logic, factored out so a
// test can drive it against a declResolver double instead of a real parsed
// file.
//
// A resolve.Resolve failure on a name resolve.DeclOrder itself just
// produced is not something a legitimate source file can trigger, so it is
// propagated rather than swallowed. Both come from the same *resolve.File:
// DeclOrder returns exactly the Symbol.Qualified strings the index's own
// byQualified map was populated with, in the same buildIndex call, from the
// same underlying slice — Resolve's first lookup is a direct hit against
// that map for any anchor equal to one of its own keys, before byBare or
// any fallback is ever consulted (internal/resolve/index.go). A failure
// here means that invariant itself broke — a real bug in the resolver, not
// an edge case a caller's input can reach — and a silently shrunk region
// set would mis-report a file's rows with nothing to say why, which is
// worse than `rgit diff` failing loudly on the file that exposed it.
func resolveRegions(lang resolve.Language, src []byte, f declResolver) ([]region, error) {
	names := f.DeclOrder()
	regions := make([]region, 0, len(names)+2)
	for _, name := range names {
		res, rerr := f.Resolve(name)
		if rerr != nil {
			return nil, fmt.Errorf("resolve: internal inconsistency: %s DeclOrder emitted %q, which Resolve then rejected: %w", lang.Name(), name, rerr)
		}
		ext := res.Extent
		if isMultiDeclaratorLang(lang) {
			ext = narrowMultiDeclarator(src, ext)
		}
		regions = append(regions, region{name: name, ext: ext})
	}
	for _, pseudo := range []string{"@header", "@imports"} {
		if res, rerr := f.Resolve(pseudo); rerr == nil {
			regions = append(regions, region{name: pseudo, ext: res.Extent})
		}
	}
	return regions, nil
}

// exclusiveText returns src[ext.Start:ext.End] with every other region in
// siblings that lies inside it removed, so a change entirely inside a
// nested named region (a struct's field, a class's method, a Markdown
// section's subsection or setext heading) is attributed to that region
// alone rather than counted again under whichever container encloses it --
// the same principle @toplevel's own exclusion from the region set already
// applies once (region's doc comment), generalized here to every
// container/member pair a language's grammar can nest.
//
// A container that contributes nothing beyond its members -- a Go struct
// whose body is only fields, a class whose body is only methods -- ends up
// with an empty or whitespace-only exclusive text, so its own row is
// naturally absent (isolatedDiff/countLines report zero, and the caller
// skips a zero row): the same (unanchorable)-for-the-container-line answer
// this replaces, but reached because there is genuinely nothing left to
// diff, not because the region was deleted outright before it got a
// chance. A Markdown section is the case that outright deletion got wrong:
// its body is ordinary prose around its subsections, not merely its
// subsections, so removing the section from the region set the moment it
// had any nested heading left that prose with no region owning it at all.
func exclusiveText(src []byte, self region, siblings []region) []byte {
	ext := self.ext
	type hole struct{ start, end uint }
	var holes []hole
	for _, r := range siblings {
		if r.name == self.name {
			continue
		}
		if r.ext.Start >= ext.Start && r.ext.End <= ext.End {
			holes = append(holes, hole{r.ext.Start, r.ext.End})
		}
	}
	if len(holes) == 0 {
		return src[ext.Start:ext.End]
	}
	slices.SortFunc(holes, func(a, b hole) int { return cmp.Compare(a.start, b.start) })

	out := make([]byte, 0, ext.End-ext.Start)
	pos := ext.Start
	for _, h := range holes {
		if h.start > pos {
			out = append(out, src[pos:h.start]...)
		}
		if h.end > pos {
			pos = h.end
		}
	}
	if ext.End > pos {
		out = append(out, src[pos:ext.End]...)
	}
	return out
}

// isMultiDeclaratorLang reports whether lang's grammar can produce more than
// one Declaration sharing the same Full extent — lang_go.go's
// goSpecNameDeclarations: "const a, b = 1, 2" is one const_spec with two
// names and one shared value list, so both "a" and "b" resolve to the whole
// spec, not a range of their own the way TypeScript's separately-noded
// declarators get. narrowMultiDeclarator's bracket-aware truncation still
// runs for these two languages for the same reason it always has --
// exclusiveText's own containment check already treats two regions with the
// identical extent as fully nested in each other and empties both out, so a
// changed Go multi-name spec falls to (unanchorable) rather than being
// double-counted or mis-attributed to whichever name sorts first, honoring
// this package's own "every row sums to the file's true total" invariant.
func isMultiDeclaratorLang(lang resolve.Language) bool {
	switch lang.Name() {
	case "typescript", "tsx", "go":
		return true
	}
	return false
}

// narrowMultiDeclarator restricts ext to the first declarator's own text
// when it spans a multi-declarator statement. resolve.Resolve's extent is
// deliberately the whole statement — that is what rgit commit splices — but
// a diff hunk touching only the second declarator must not be silently
// attributed to the first one's name (the honesty requirement this
// package's doc comment states). This is a best-effort bracket/string-aware
// scan for a top-level comma, not a second parse: it is exactly as
// expensive as it needs to be for the common case, and a false negative
// only means the second declarator's own change falls back to
// (unanchorable) rather than being mis-attributed — never the reverse.
func narrowMultiDeclarator(src []byte, ext resolve.Extent) resolve.Extent {
	text := src[ext.Start:ext.End]
	if comma := topLevelComma(text); comma >= 0 {
		ext.End = ext.Start + uint(comma)
	}
	return ext
}

// topLevelComma finds the first comma in text that sits outside every
// bracket pair and every quoted or backtick-quoted span, or -1 if there is
// none. Backslash escapes inside a quoted span are honoured so a comma
// after an escaped quote character is not mistaken for the span's end.
func topLevelComma(text []byte) int {
	depth := 0
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			return i
		}
	}
	return -1
}

// isNested reports whether r sits inside another region -- a struct's
// field, a class's method, a Markdown subsection. It is the same
// containment test exclusiveText uses to hole a container out, asked the
// other way round.
func isNested(r region, siblings []region) bool {
	for _, s := range siblings {
		if s.name == r.name {
			continue
		}
		if r.ext.Start >= s.ext.Start && r.ext.End <= s.ext.End {
			return true
		}
	}
	return false
}

// separatorLines reports how many blank lines a region that exists on only
// one side takes with it, so attribution counts what internal/synth will
// actually move rather than the declaration's extent alone.
//
// The answer is at most one, because that is precisely what synth moves:
// joinWithSeparator writes exactly one blank line between two top-level
// declarations when splicing an insert, and spliceExcise collapses exactly
// that gap on a delete. Attributing every adjacent blank line instead would
// be wrong wherever a formatter writes more than one -- PEP 8 writes two
// between top-level defs, synth still moves one, and the second genuinely
// stays unowned. That leftover is what (unanchorable) is for.
//
// A nested region gets none: members are separated by a single newline
// rather than a blank line (joinWithSeparator's member case), which the
// member's own extent already accounts for.
//
// The gap is looked for after the region first and before it only at
// end-of-file, mirroring spliceExcise: it trims the following gap when
// anything follows, and falls back to the preceding one when the region was
// last in the file.
func separatorLines(src []byte, self region, siblings []region) int {
	return SeparatorLines(src, self.ext.Start, self.ext.End, isNested(self, siblings))
}

// SeparatorLines is separatorLines' own rule, taking a plain byte range so
// internal/synth can apply it to the edit it is about to splice.
//
// Exported for the same reason LineCounts is: `rgit diff` and
// `rgit commit --dry-run` promise to agree row for row (docs/CODES.md), and
// two implementations of "does this symbol carry its separator" would be
// free to drift apart, producing a phantom (unanchorable) row.
func SeparatorLines(src []byte, start, end uint, member bool) int {
	if member {
		return 0
	}

	trailing := 0
	for e := end; e < uint(len(src)) && src[e] == '\n'; e++ {
		trailing++
	}
	if end+uint(trailing) < uint(len(src)) {
		return min(trailing, 1)
	}

	// Nothing but the file's own terminator follows, so the gap that moves
	// is the one before this region. One of those newlines ends the
	// previous declaration's last line and is already counted there.
	leading := 0
	for s := start; s > 0 && src[s-1] == '\n'; s-- {
		leading++
	}
	return min(max(leading-1, 0), 1)
}

// indexRegions maps each region's name to its extent for O(1) lookup.
func indexRegions(regions []region) map[string]resolve.Extent {
	out := make(map[string]resolve.Extent, len(regions))
	for _, r := range regions {
		out[r.name] = r.ext
	}
	return out
}

// attributeSymbols splits one file's total change into per-symbol rows plus
// an (unanchorable) remainder.
//
// Each named region that exists on both sides gets an isolated line diff of
// just its own extent (go-udiff, in process — no fork/exec per anchor,
// specs/design.md's dependency justification for it). A region that exists
// on only one side is a pure addition or deletion of that whole extent, so
// its count is a plain line count, no diff needed.
//
// (unanchorable) is the remainder against git's own numstat totals rather
// than a hunk-by-hunk classification, which makes "every row sums to the
// file's true total" true by construction. It clamps at zero: isolated
// per-symbol diffs and git's whole-file diff can align ambiguous content
// (duplicate lines, say) differently.
func attributeSymbols(lang resolve.Language, oldSrc, newSrc []byte, totalAdded, totalDeleted int) ([]Row, error) {
	oldFile, err := resolve.Open(lang, oldSrc)
	if err != nil {
		return nil, err
	}
	defer oldFile.Close()
	newFile, err := resolve.Open(lang, newSrc)
	if err != nil {
		return nil, err
	}
	defer newFile.Close()
	return attributeSymbolsOpen(lang, oldSrc, newSrc, oldFile, newFile, totalAdded, totalDeleted)
}

// attributeSymbolsOpen is attributeSymbols' own logic, taking each side's
// already-parsed file (buildRegions' Open, factored out) rather than
// opening its own. buildFileReport (run.go) already parses newSrc once for
// the LSP cross-check; calling through here with that same *resolve.File
// instead of letting attributeSymbols open a second one is this package's
// own share of the held-parse gain specs/design.md § Blob synthesis
// measures for internal/synth (~39x, one parse per side instead of one per
// anchor) -- attributeSymbols above stays the convenience form for a caller
// (this package's own tests) with nothing already open to hand in.
func attributeSymbolsOpen(lang resolve.Language, oldSrc, newSrc []byte, oldFile, newFile declResolver, totalAdded, totalDeleted int) ([]Row, error) {
	oldRegions, err := resolveRegions(lang, oldSrc, oldFile)
	if err != nil {
		return nil, err
	}
	newRegions, err := resolveRegions(lang, newSrc, newFile)
	if err != nil {
		return nil, err
	}
	oldByName := indexRegions(oldRegions)
	newByName := indexRegions(newRegions)

	var rows []Row
	accAdded, accDeleted := 0, 0
	seen := map[string]bool{}

	handle := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true

		oldExt, inOld := oldByName[name]
		newExt, inNew := newByName[name]
		switch {
		case inOld && inNew:
			oldText := exclusiveText(oldSrc, region{name: name, ext: oldExt}, oldRegions)
			newText := exclusiveText(newSrc, region{name: name, ext: newExt}, newRegions)
			added, deleted := isolatedDiff(oldText, newText)
			if added == 0 && deleted == 0 {
				return
			}
			accAdded += added
			accDeleted += deleted
			rows = append(rows, Row{Symbol: name, Status: StatusMod, Added: itoa(added), Deleted: itoa(deleted), pos: newExt.Start})
		case inOld && !inNew:
			self := region{name: name, ext: oldExt}
			deleted := countLines(exclusiveText(oldSrc, self, oldRegions))
			if deleted == 0 {
				return
			}
			deleted += separatorLines(oldSrc, self, oldRegions)
			accDeleted += deleted
			// A deleted symbol has no position in the new file, so it sorts
			// by where it used to be — stable, and close to where a reader
			// expects to find it.
			rows = append(rows, Row{Symbol: name, Status: StatusDeleted, Added: "0", Deleted: itoa(deleted), pos: oldExt.Start})
		case !inOld && inNew:
			self := region{name: name, ext: newExt}
			added := countLines(exclusiveText(newSrc, self, newRegions))
			if added == 0 {
				return
			}
			added += separatorLines(newSrc, self, newRegions)
			accAdded += added
			rows = append(rows, Row{Symbol: name, Status: StatusMod, Added: itoa(added), Deleted: "0", pos: newExt.Start})
		}
	}

	for _, r := range oldRegions {
		handle(r.name)
	}
	for _, r := range newRegions {
		handle(r.name)
	}

	unAdded := totalAdded - accAdded
	unDeleted := totalDeleted - accDeleted
	if unAdded < 0 {
		unAdded = 0
	}
	if unDeleted < 0 {
		unDeleted = 0
	}
	// Ascending by position, so the listing reads in file order and is
	// identical between runs. Sorted before the remainder row is appended:
	// (unanchorable) is every hunk no symbol owns, spread across the whole
	// file rather than sitting at one offset, so it has no position to sort
	// by and belongs last.
	slices.SortStableFunc(rows, func(a, b Row) int { return cmp.Compare(a.pos, b.pos) })

	if unAdded > 0 || unDeleted > 0 {
		rows = append(rows, Row{Status: StatusUnanchorable, Added: itoa(unAdded), Deleted: itoa(unDeleted)})
	}

	return rows, nil
}

// LineCounts line-diffs two extents' own text in isolation and reports the
// insertions and deletions between them.
//
// Exported so `rgit commit --dry-run` previews the same numbers `rgit diff`
// prints. Two implementations of "how much did this symbol change" would be
// free to disagree, and a preview that disagrees with the diff it previews is
// worse than no preview.
func LineCounts(oldText, newText []byte) (added, deleted int) {
	return isolatedDiff(oldText, newText)
}

// isolatedDiff line-diffs two extents' own text in isolation via go-udiff,
// summing insertions and deletions across every returned edit.
func isolatedDiff(oldText, newText []byte) (added, deleted int) {
	edits := udiff.Lines(string(oldText), string(newText))
	for _, e := range edits {
		deleted += countLines(oldText[e.Start:e.End])
		added += countLines([]byte(e.New))
	}
	return added, deleted
}

// countLines counts s as git counts numstat lines: each embedded newline is
// one line, plus one more for a trailing partial line with no newline
// (git's own "no newline at end of file" convention, AGENTS.md's invariant
// table).
func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte("\n"))
	if !bytes.HasSuffix(b, []byte("\n")) {
		n++
	}
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }
