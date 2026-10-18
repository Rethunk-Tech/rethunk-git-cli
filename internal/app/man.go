package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

// runMan is hidden: not listed in topLevelHelp. It prints a groff man page
// assembled from the same --help text every other command already owns, so
// the page cannot drift from what `rgit --help` and `rgit <cmd> --help`
// print.
func runMan(ctx context.Context, version string, args []string, stdout, stderr io.Writer) exitcode.Code {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "rgit: man takes no arguments")
		return exitcode.InvalidUsage
	}
	writeManPage(ctx, version, stdout)
	return exitcode.Success
}

func writeManPage(ctx context.Context, version string, w io.Writer) {
	if version == "" {
		version = "rgit"
	}
	fmt.Fprintf(w, ".TH RGIT 1 \"\" \"rgit %s\" \"User Commands\"\n", groffEscape(version))
	fmt.Fprint(w, ".SH NAME\nrgit \\- git add and git commit at symbol granularity\n")
	fmt.Fprint(w, ".SH SYNOPSIS\n.nf\n")
	fmt.Fprint(w, groffPre(usageLine))
	fmt.Fprint(w, "\n.fi\n.SH DESCRIPTION\n.nf\n")
	fmt.Fprint(w, groffPre(topLevelHelp))
	fmt.Fprint(w, "\n.fi\n.SH COMMANDS\n")
	for _, name := range commandNamesFromHelp(topLevelHelp) {
		fmt.Fprintf(w, ".SS %s\n.nf\n", groffEscape(name))
		var buf strings.Builder
		_ = runHelp(ctx, "", []string{name}, &buf, io.Discard)
		fmt.Fprint(w, groffPre(buf.String()))
		fmt.Fprint(w, "\n.fi\n")
	}
}

// commandNamesFromHelp extracts the first word of each line in help's
// "Commands:" section, up to the following blank line.
func commandNamesFromHelp(help string) []string {
	const marker = "Commands:\n"
	_, after, ok := strings.Cut(help, marker)
	if !ok {
		return nil
	}
	rest := after
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}
	var names []string
	for line := range strings.SplitSeq(rest, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			names = append(names, fields[0])
		}
	}
	return names
}

func groffEscape(s string) string {
	return strings.ReplaceAll(s, `\`, `\\`)
}

func groffPre(s string) string {
	s = groffEscape(s)
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		if strings.HasPrefix(line, ".") || strings.HasPrefix(line, "'") {
			b.WriteString("\\&")
		}
		b.WriteString(line)
	}
	return b.String()
}
