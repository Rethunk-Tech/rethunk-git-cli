package diff

import (
	"bytes"
	"fmt"
)

// RenderPorcelain renders report as docs/CODES.md § Output records' stable
// tab-separated records: FILE<TAB>SYMBOL<TAB>STATUS<TAB>ADDED<TAB>DELETED,
// one line per row, no header.
func RenderPorcelain(report *Report) string {
	if report == nil {
		return ""
	}
	var buf bytes.Buffer
	for _, f := range report.Files {
		for _, row := range f.Rows {
			fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%s\n", f.Path, row.Symbol, row.Status.Porcelain(), row.Added, row.Deleted)
		}
	}
	return buf.String()
}
