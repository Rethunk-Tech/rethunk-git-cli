package diff

import (
	"bytes"
	"fmt"
	"path/filepath"
	"text/tabwriter"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// RenderText renders report in the default aligned layout from
// docs/USAGE.md § Commands. An empty report renders as the empty string,
// matching docs/INSTALL.md: "A repo with no uncommitted changes prints
// nothing."
func RenderText(report *Report) string {
	if report == nil || len(report.Files) == 0 {
		return ""
	}

	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	for _, f := range report.Files {
		for i, row := range f.Rows {
			file := ""
			if i == 0 {
				file = f.Path
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", file, rowLabel(row), rowStatusWord(row), rowCounts(row), rowHint(f.Path, row))
		}
	}
	// Writes go to a bytes.Buffer, which never fails.
	_ = tw.Flush()
	return buf.String()
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
	default:
		return ""
	}
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

// rowCounts renders "+N/-M", or "-/-" for a binary row — matching `git diff
// --numstat`'s own convention for binary counts (docs/USAGE.md § Output).
func rowCounts(row Row) string {
	if row.Added == "-" {
		return "-/-"
	}
	return "+" + row.Added + "/-" + row.Deleted
}

// rowHint is the trailing "-> use ..." suggestion docs/USAGE.md's example
// shows for rows a plain positional can't stage directly.
func rowHint(path string, row Row) string {
	switch row.Status {
	case StatusUnanchorable:
		return unanchorableHint(path)
	case StatusUntracked:
		if row.HintSymbol != "" {
			return "-> use --sym " + path + ":" + row.HintSymbol + " or --file " + path
		}
		return "-> use --file " + path
	case StatusMode:
		return "-> use rgit commit " + path
	default:
		return ""
	}
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
// Deciding this by lang.Name() rather than a new resolve.Language predicate
// keeps the distinction where the rest of this package already draws similar
// ones (isMultiDeclaratorLang in attribute.go): a hint string is internal/
// diff's own concern, not something the resolver needs to expose.
func unanchorableHint(path string) string {
	if lang, ok := resolve.ForExtension(filepath.Ext(path)); ok && lang.Name() == "markdown" {
		return "-> use --sym " + path + ":@toplevel or --file " + path
	}
	return "-> use --file " + path
}
