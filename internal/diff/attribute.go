package diff

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strconv"

	udiff "github.com/aymanbagabas/go-udiff"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/resolve"
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

// regionTree is one side's region set with its nesting resolved once.
//
// exclusiveText and isNested both ask the same containment question, and
// asking it per region against every other region made attribution quadratic
// in the number of declarations: a 16 000-declaration generated file spent
// seconds here where git's own diff takes none. The containment relation never
// changes during one attribution, so it is computed once -- one sorted walk
// with a stack, O(n log n) -- and every per-region question is then a map
// lookup.
type regionTree struct {
	// holes maps a region name to the extents exclusiveText must cut out of
	// it: its direct children, plus the extent of any sibling occupying the
	// identical range (two names sharing one declaration, a Go multi-name
	// spec), which contain each other just as fully.
	holes map[string][]resolve.Extent

	// nested marks a region that sits inside another one -- a struct's field,
	// a class's method, a Markdown subsection, or one of those identical-range
	// siblings.
	nested map[string]bool
}

// buildRegionTree resolves containment for regions in one pass. A region's
// parent is the innermost region that strictly contains it, which a walk in
// ascending start (descending end, so a container precedes its members)
// maintains on a stack: everything popped has ended, so whatever remains on
// top is the deepest open container.
func buildRegionTree(regions []region) *regionTree {
	ordered := slices.Clone(regions)
	slices.SortFunc(ordered, func(a, b region) int {
		if c := cmp.Compare(a.ext.Start, b.ext.Start); c != 0 {
			return c
		}
		return cmp.Compare(b.ext.End, a.ext.End)
	})

	t := &regionTree{
		holes:  make(map[string][]resolve.Extent, len(regions)),
		nested: make(map[string]bool, len(regions)),
	}
	// byRange groups the names sharing one extent, for the identical-range
	// case below.
	byRange := make(map[resolve.Extent][]string, len(regions))

	var stack []region
	for _, r := range ordered {
		for len(stack) > 0 && !containsExtent(stack[len(stack)-1].ext, r.ext) {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			parent := stack[len(stack)-1]
			if parent.ext == r.ext {
				// Identical ranges contain each other equally, so neither is
				// the other's child; both are nested, and each cuts the other
				// out. Leaving the first on the stack keeps a following
				// strictly-inner region parented to the same container.
				t.nested[r.name] = true
				t.nested[parent.name] = true
			} else {
				t.nested[r.name] = true
				t.holes[parent.name] = append(t.holes[parent.name], r.ext)
			}
		}
		byRange[r.ext] = append(byRange[r.ext], r.name)
		stack = append(stack, r)
	}

	for ext, names := range byRange {
		if len(names) < 2 {
			continue
		}
		for _, name := range names {
			for _, other := range names {
				if other != name {
					t.holes[name] = append(t.holes[name], ext)
				}
			}
		}
	}
	for name := range t.holes {
		slices.SortFunc(t.holes[name], func(a, b resolve.Extent) int { return cmp.Compare(a.Start, b.Start) })
	}
	return t
}

// containsExtent reports whether outer spans inner, endpoints included: a
// declaration's own extent and its container's touch but do not overlap.
func containsExtent(outer, inner resolve.Extent) bool {
	return inner.Start >= outer.Start && inner.End <= outer.End
}

// exclusiveText returns src[ext.Start:ext.End] with every region the tree
// records as lying inside it removed, so a change entirely inside a nested
// named region (a struct's field, a class's method, a Markdown section's
// subsection or setext heading) is attributed to that region alone rather
// than counted again under whichever container encloses it -- the same
// principle @toplevel's own exclusion from the region set already applies
// once (region's doc comment), generalized here to every container/member
// pair a language's grammar can nest.
//
// A container that contributes nothing beyond its members -- a Go struct whose
// body is only fields, a class whose body is only methods -- ends up with an
// empty or whitespace-only exclusive text, so its own row is naturally absent
// (isolatedDiff/countLines report zero, and the caller skips a zero row): the
// same (unanchorable)-for-the-container-line answer this replaces, but reached
// because there is genuinely nothing left to diff, not because the region was
// deleted outright before it got a chance. A Markdown section is the case that
// outright deletion got wrong: its body is ordinary prose around its
// subsections, not merely its subsections, so removing the section from the
// region set the moment it had any nested heading left that prose with no
// region owning it at all.
func (t *regionTree) exclusiveText(src []byte, name string, ext resolve.Extent) []byte {
	holes := t.holes[name]
	if len(holes) == 0 {
		return src[ext.Start:ext.End]
	}

	out := make([]byte, 0, ext.End-ext.Start)
	pos := ext.Start
	for _, h := range holes {
		if h.Start > pos {
			out = append(out, src[pos:h.Start]...)
		}
		if h.End > pos {
			pos = h.End
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
func separatorLines(src []byte, ext resolve.Extent, nested bool) int {
	return SeparatorLines(src, ext.Start, ext.End, nested)
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

// attributeSymbolsOpen splits one file's total change into per-symbol rows
// plus an (unanchorable) remainder, taking each side's already-parsed file
// rather than opening its own: buildFileReport (run.go) parses newSrc once
// for the LSP cross-check, and reusing that *resolve.File is this package's
// share of the held-parse gain measured for internal/synth (~39x, one
// parse per side instead of one per anchor).
//
// Each named region that exists on both sides gets an isolated line diff of
// just its own extent (go-udiff, in process — no fork/exec per anchor,
// which is why that dependency is carried). A region that exists
// on only one side is a pure addition or deletion of that whole extent, so
// its count is a plain line count, no diff needed.
//
// (unanchorable) is the remainder against git's own numstat totals rather
// than a hunk-by-hunk classification, which makes "every row sums to the
// file's true total" true by construction. It clamps at zero: isolated
// per-symbol diffs and git's whole-file diff can align ambiguous content
// (duplicate lines, say) differently.
func attributeSymbolsOpen(lang resolve.Language, oldSrc, newSrc []byte, oldFile, newFile declResolver, totalAdded, totalDeleted int) ([]Row, []string, error) {
	oldRegions, err := resolveRegions(lang, oldSrc, oldFile)
	if err != nil {
		return nil, nil, err
	}
	newRegions, err := resolveRegions(lang, newSrc, newFile)
	if err != nil {
		return nil, nil, err
	}
	oldByName := indexRegions(oldRegions)
	newByName := indexRegions(newRegions)
	oldTree := buildRegionTree(oldRegions)
	newTree := buildRegionTree(newRegions)

	var rows []Row
	var notices []string
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
			oldText := oldTree.exclusiveText(oldSrc, name, oldExt)
			newText := newTree.exclusiveText(newSrc, name, newExt)
			added, deleted := LineCounts(oldText, newText)
			if added == 0 && deleted == 0 {
				return
			}
			accAdded += added
			accDeleted += deleted
			rows = append(rows, Row{Symbol: name, Status: StatusMod, Added: strconv.Itoa(added), Deleted: strconv.Itoa(deleted), pos: newExt.Start})
		case inOld && !inNew:
			deleted := countLines(oldTree.exclusiveText(oldSrc, name, oldExt))
			if deleted == 0 {
				return
			}
			deleted += separatorLines(oldSrc, oldExt, oldTree.nested[name])
			accDeleted += deleted
			// A deleted symbol has no position in the new file, so it sorts
			// by its prior line position in the old file — stable, and close to where a reader
			// expects to find it.
			rows = append(rows, Row{Symbol: name, Status: StatusDeleted, Added: "0", Deleted: strconv.Itoa(deleted), pos: oldExt.Start})
		case !inOld && inNew:
			added := countLines(newTree.exclusiveText(newSrc, name, newExt))
			if added == 0 {
				return
			}
			added += separatorLines(newSrc, newExt, newTree.nested[name])
			accAdded += added
			rows = append(rows, Row{Symbol: name, Status: StatusMod, Added: strconv.Itoa(added), Deleted: "0", pos: newExt.Start})
			if container, isNew := newlyEscalatedContainer(lang, oldFile, newFile, name); isNew {
				notices = append(notices, fmt.Sprintf("%s: %s is new; rgit commit would stage the whole container, not just %s", name, container, name))
			}
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
		rows = append(rows, Row{Status: StatusUnanchorable, Added: strconv.Itoa(unAdded), Deleted: strconv.Itoa(unDeleted)})
	}

	return rows, notices, nil
}

// newlyEscalatedContainer reports whether name's enclosing container, if
// any, would make internal/synth's own escalateToContainer (classify.go)
// widen a `rgit commit` of this exact anchor to the whole container instead
// of just the member -- the same condition, mirrored rather than shared,
// because escalateToContainer is entangled with filePlan's staging state
// (headExists, in-progress edit tracking) that diff has no equivalent of
// and no reason to build just to ask this one read-only question.
//
// Mirrors escalateToContainer's two guards precisely, so this can never
// warn on a case synth would not actually escalate: HTML's Container is the
// element's own tag, not a real ancestor (Language.FlatContainer), and a Go
// receiver method's Container names a sibling type, not an enclosing one
// (tested by extent containment, not by the mere presence of a container
// name).
func newlyEscalatedContainer(lang resolve.Language, oldFile, newFile declResolver, name string) (container string, isNew bool) {
	if lang.FlatContainer() {
		return "", false
	}
	member, err := newFile.Resolve(name)
	if err != nil || member.Container == "" {
		return "", false
	}
	outer, err := newFile.Resolve(member.EnclosingName())
	if err != nil || outer.Extent.Start > member.Extent.Start || member.Extent.End > outer.Extent.End {
		return "", false // sibling, not parent (Go receiver)
	}
	if _, err := oldFile.Resolve(member.EnclosingName()); err == nil {
		return "", false // container already exists on the old side
	}
	return member.EnclosingName(), true
}

// LineCounts line-diffs two extents' own text in isolation via go-udiff,
// summing insertions and deletions across every returned edit.
//
// Exported so `rgit commit --dry-run` previews the same numbers `rgit diff`
// prints. Two implementations of "how much did this symbol change" would be
// free to disagree, and a preview that disagrees with the diff it previews is
// worse than no preview.
func LineCounts(oldText, newText []byte) (added, deleted int) {
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
