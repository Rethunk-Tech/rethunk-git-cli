package diff

import (
	"bytes"
	"fmt"
	"text/tabwriter"
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
		return "-> use --file " + path
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
