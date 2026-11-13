package diff

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"text/tabwriter"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/resolve"
)

// RenderText renders report in the default aligned layout from
// docs/USAGE.md § Commands. An empty report renders as the empty string,
// matching docs/INSTALL.md: "A repo with no uncommitted changes prints
// nothing.".
func RenderText(report *Report) string {
	if report == nil || len(report.Files) == 0 {
		return ""
	}

	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	for _, f := range report.Files {
		rows := rowsForRender(f)
		for i, row := range rows {
			file := ""
			if i == 0 {
				file = f.Path
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", file, rowLabel(row), rowStatusWord(row), rowCounts(row), rowHint(f.Path, f.lang, row))
		}
	}
	// Writes go to a bytes.Buffer, which never fails.
	_ = tw.Flush()
	return TrimLineEnds(buf.String())
}

// rowLabel is the second column: the symbol name for an ordinary row, or
// the file-level status's parenthesized placeholder for a row with no
// symbol.
func rowLabel(row Row) string {
	if row.Symbol != "" {
		return row.Symbol
	}
	switch row.Status {
	case StatusUnanchorable:
		return "(unanchorable)"
	case StatusUntracked:
		return "(untracked)"
	case StatusMode:
		return "(mode " + row.ModeNote + ")"
	case StatusBinary:
		return "(binary)"
	case StatusNoSymbols:
		return "(no symbols)"
	case StatusMod, StatusDeleted:
		return ""
	}
	return ""
}

// rowStatusWord is the third column, used only to mark a named symbol row
// DELETED — every other status already says what it is via rowLabel's
// placeholder, so this column is blank for them.
func rowStatusWord(row Row) string {
	if row.Symbol != "" && row.Status == StatusDeleted {
		return "DELETED"
	}
	return ""
}

// rowCounts renders "+N/-M", or "-/-" for a binary row — matching `git
// diff --numstat`'s own convention for binary counts (docs/CODES.md §
// Output records).
func rowCounts(row Row) string {
	if row.Added == "-" {
		return "-/-"
	}
	return "+" + row.Added + "/-" + row.Deleted
}

// rowHint is the trailing "-> use ..." suggestion docs/USAGE.md's example
// shows for rows a plain positional can't stage directly. lang is
// FileReport.lang -- already resolved by buildFileReport, shebang fallback
// included, so this never re-derives the language from path's extension
// alone.
func rowHint(path, lang string, row Row) string {
	switch row.Status {
	case StatusUnanchorable:
		return unanchorableHint(path, lang)
	case StatusUntracked:
		if row.HintSymbol != "" {
			return "-> use --sym " + path + ":" + row.HintSymbol + " or --file " + path
		}
		return "-> use --file " + path
	case StatusMode:
		return "-> use rgit commit " + path
	case StatusMod, StatusDeleted, StatusBinary, StatusNoSymbols:
		return ""
	}
	return ""
}

// unanchorableHint is the (unanchorable) row's own suggestion. For most
// languages @toplevel spans every declaration's Full extent -- a superset of
// every row this package already emits, never a target this remainder could
// be -- so --file remains the only way to stage it. Markdown is the
// exception: @toplevel there is a disjoint region, the lede (content before
// the first heading), which no other row can ever cover by construction
// (lang_markdown.go's Declarations never returns an entry for it) -- so an
// (unanchorable) row in a Markdown file is always the lede, and always
// stageable more precisely than the whole path.
//
// Deciding this by lang (FileReport.lang, a resolve.Language's own Name())
// rather than a new resolve.Language predicate keeps the distinction where
// the rest of this package already draws similar ones (isMultiDeclaratorLang
// in attribute.go): a hint string is internal/diff's own concern, not
// something the resolver needs to expose.
//
// lang comes from the caller's FileReport rather than being re-derived here
// via path's extension: buildFileReport already resolved it with the
// shebang fallback an extensionless git hook or bin/ entry needs
// (resolve.LanguageForPathFolding), and re-deriving from extension alone
// here would silently disagree with that for such a path.
func unanchorableHint(path, lang string) string {
	if lang == "markdown" {
		return "-> use --sym " + path + ":@toplevel or --file " + path
	}
	return "-> use --file " + path
}

var trailingSpace = regexp.MustCompile(`(?m)[ \t]+$`)

// TrimLineEnds drops the padding tabwriter writes after an empty last cell.
func TrimLineEnds(s string) string { return trailingSpace.ReplaceAllString(s, "") }

// rowsForRender collapses JSON/YAML/TOML files to one path-only row, the
// same filter `rgit symbols --for-commit` uses: commit refuses FILE:key on
// those files, so naming the keys here would send callers into exit 12.
func rowsForRender(f FileReport) []Row {
	lang, ok, _, err := resolve.LanguageForPathFolding("", f.Path, false, nil)
	if err != nil || !ok || !resolve.IsStructuredData(lang) {
		return f.Rows
	}
	if len(f.Rows) == 0 || (len(f.Rows) == 1 && f.Rows[0].Symbol == "") {
		return f.Rows
	}
	return []Row{pathOnlyRow(f.Rows)}
}

func pathOnlyRow(rows []Row) Row {
	status := StatusNoSymbols
	if len(rows) > 0 {
		s := rows[0].Status
		same := true
		for _, r := range rows[1:] {
			if r.Status != s {
				same = false
				break
			}
		}
		if same && (s == StatusUntracked || s == StatusDeleted || s == StatusBinary || s == StatusMode) {
			status = s
		}
	}
	added, deleted := 0, 0
	binary := true
	for _, r := range rows {
		if r.Added == "-" && r.Deleted == "-" {
			continue
		}
		binary = false
		a, _ := strconv.Atoi(r.Added)
		d, _ := strconv.Atoi(r.Deleted)
		added += a
		deleted += d
	}
	row := Row{Status: status, Added: strconv.Itoa(added), Deleted: strconv.Itoa(deleted)}
	if binary {
		row.Added, row.Deleted = "-", "-"
	}
	if status == StatusMode && len(rows) == 1 {
		row.ModeNote = rows[0].ModeNote
	}
	return row
}
