package diff

import (
	"strconv"
	"strings"

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
	names, err := resolve.DeclOrder(lang, src)
	if err != nil {
		return nil, err
	}

	regions := make([]region, 0, len(names)+2)
	for _, name := range names {
		res, rerr := resolve.Resolve(lang, src, name)
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
		if res, rerr := resolve.Resolve(lang, src, pseudo); rerr == nil {
			regions = append(regions, region{name: pseudo, ext: res.Extent})
		}
	}
	return regions, nil
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
// (unanchorable) is computed as the remainder against totalAdded/
// totalDeleted — git's own numstat totals for the file — rather than by
// walking a whole-file diff and classifying each hunk by byte-range
// overlap. This guarantees by construction that the sum of every reported
// row equals the file's true total change: no hunk can be silently dropped,
// because "everything not attributed to a named symbol" is defined as
// exactly what is left after subtracting what was. The only way this
// remainder could go negative is if isolated per-symbol diffs and git's own
// whole-file diff algorithm disagree on how to align genuinely ambiguous
// content (e.g. duplicate lines); that is clamped to zero rather than
// rendered as a negative count.
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
			added, deleted := isolatedDiff(oldSrc[oldExt.Start:oldExt.End], newSrc[newExt.Start:newExt.End])
			if added == 0 && deleted == 0 {
				return
			}
			accAdded += added
			accDeleted += deleted
			rows = append(rows, Row{Symbol: name, Status: StatusMod, Added: itoa(added), Deleted: itoa(deleted)})
		case inOld && !inNew:
			deleted := countLines(string(oldSrc[oldExt.Start:oldExt.End]))
			if deleted == 0 {
				return
			}
			accDeleted += deleted
			rows = append(rows, Row{Symbol: name, Status: StatusDeleted, Added: "0", Deleted: itoa(deleted)})
		case !inOld && inNew:
			added := countLines(string(newSrc[newExt.Start:newExt.End]))
			if added == 0 {
				return
			}
			accAdded += added
			rows = append(rows, Row{Symbol: name, Status: StatusMod, Added: itoa(added), Deleted: "0"})
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
		deleted += countLines(string(oldText[e.Start:e.End]))
		added += countLines(e.New)
	}
	return added, deleted
}

// countLines counts s as git counts numstat lines: each embedded newline is
// one line, plus one more for a trailing partial line with no newline
// (git's own "no newline at end of file" convention, AGENTS.md's invariant
// table).
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }
