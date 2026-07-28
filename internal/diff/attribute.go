package diff

import (
	"bytes"
	"cmp"
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

// buildRegions computes every named region in src. Errors from
// resolve.Resolve on a name resolve.DeclOrder itself just produced cannot
// happen in practice — same source, same deterministic computation — but
// are treated as "no region" rather than propagated, since a missing
// region only ever downgrades a row to (unanchorable), never mis-attributes
// one.
func buildRegions(lang resolve.Language, src []byte) ([]region, error) {
	// One parse for the whole file: this resolves every declaration in it,
	// and it runs once per side of every comparison.
	f, err := resolve.Open(lang, src)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	names := f.DeclOrder()
	regions := make([]region, 0, len(names)+2)
	for _, name := range names {
		res, rerr := f.Resolve(name)
		if rerr != nil {
			continue
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

// isMultiDeclaratorLang reports whether lang's grammar can produce a
// Declaration whose Full extent covers more than the addressable name
// alone — docs/ANCHORS.md and lang_typescript.go: "const a = 1, b = 2" is
// one lexical_declaration statement, and only the first declarator is
// named, but the staged extent is the whole statement.
func isMultiDeclaratorLang(lang resolve.Language) bool {
	switch lang.Name() {
	case "typescript", "tsx":
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
	oldRegions, err := buildRegions(lang, oldSrc)
	if err != nil {
		return nil, err
	}
	newRegions, err := buildRegions(lang, newSrc)
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
			deleted := countLines(exclusiveText(oldSrc, region{name: name, ext: oldExt}, oldRegions))
			if deleted == 0 {
				return
			}
			accDeleted += deleted
			// A deleted symbol has no position in the new file, so it sorts
			// by where it used to be — stable, and close to where a reader
			// expects to find it.
			rows = append(rows, Row{Symbol: name, Status: StatusDeleted, Added: "0", Deleted: itoa(deleted), pos: oldExt.Start})
		case !inOld && inNew:
			added := countLines(exclusiveText(newSrc, region{name: name, ext: newExt}, newRegions))
			if added == 0 {
				return
			}
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
