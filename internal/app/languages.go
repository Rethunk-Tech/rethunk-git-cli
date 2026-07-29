// The `rgit languages` command surface: lists every grammar compiled into
// this binary -- name, extensions, and whether it exists only because a
// build tag selected it. resolve.Languages() is the only supported way to
// learn this (AGENTS.md's delegation boundary keeps rgit out of resolve's
// own registry); SQL is the first grammar gated this way, generated at
// install time when the tree-sitter CLI is on PATH (docs/INSTALL.md § SQL
// support), so the same binary can legitimately answer "what do you
// support" two different ways -- this makes the answer runtime-accurate
// rather than relying on hand-maintained prose that can drift.
package app

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const languagesHelp = `usage: rgit languages [--porcelain]

List every grammar compiled into this binary: name, file extensions, and
whether it is present only because a build tag selected it (currently only
SQL, behind -tags rgit_sql -- see docs/INSTALL.md § SQL support).

--porcelain lists stable tab-separated records instead: see
docs/CODES.md#output-records.

Full reference: docs/USAGE.md
`

// runLanguages's only flag is --porcelain, so it is parsed by hand rather
// than pulling in pflag's machinery the way diff and commit's much larger
// flag surfaces need -- matching doctor.go and completion.go's own minimal
// style for a subcommand this small.
func runLanguages(args []string, stdout, stderr io.Writer) exitcode.Code {
	porcelain := false
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprint(stdout, languagesHelp)
			return exitcode.Success
		case "--porcelain":
			porcelain = true
		default:
			fmt.Fprintf(stderr, "rgit: languages: unrecognized argument %q\n", a)
			fmt.Fprint(stderr, languagesHelp)
			return exitcode.InvalidUsage
		}
	}

	langs := resolve.Languages()
	if porcelain {
		fmt.Fprint(stdout, renderLanguagesPorcelain(langs))
	} else {
		fmt.Fprint(stdout, renderLanguages(langs))
	}
	return exitcode.Success
}

// renderLanguages is the aligned NAME / EXTENSIONS / GATED layout, using the
// same text/tabwriter shape internal/diff/render.go uses for rgit diff's own
// default output, so the two commands' plain-text listings read as one
// tool. `rgit doctor` (doctor.go) reuses it for its own grammar section
// rather than growing a second rendering of the identical data.
func renderLanguages(langs []resolve.LanguageInfo) string {
	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	for _, l := range langs {
		gated := ""
		if l.Gated {
			gated = "(build-tag gated)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", l.Name, strings.Join(l.Extensions, " "), gated)
	}
	// Writes go to a strings.Builder, which never fails.
	_ = tw.Flush()
	return buf.String()
}

// renderLanguagesPorcelain renders langs as docs/CODES.md's stable
// tab-separated record: NAME<TAB>EXTENSIONS<TAB>GATED, one line per
// language sorted by NAME (resolve.Languages() already returns them sorted,
// so no further sort is needed here), no header -- the same "no STATUS
// column when every row would carry the same shape of value" economy
// rgit commit --porcelain already applies (docs/CODES.md), except GATED
// really does vary per row, so it stays.
//
// EXTENSIONS joins with a single space, matching renderLanguages's own
// human-readable join: unambiguous, since a real extension is always
// ".something" and never itself contains whitespace, so a space can never
// be mistaken for part of one. GATED is the literal "1" or "0" rather than
// an empty/present column -- a definite answer for every row, the same way
// internal/diff/porcelain.go's RenderPorcelain never leaves STATUS to be
// inferred from a column's absence.
func renderLanguagesPorcelain(langs []resolve.LanguageInfo) string {
	var buf strings.Builder
	for _, l := range langs {
		gated := "0"
		if l.Gated {
			gated = "1"
		}
		fmt.Fprintf(&buf, "%s\t%s\t%s\n", l.Name, strings.Join(l.Extensions, " "), gated)
	}
	return buf.String()
}

// gatedLanguageNames returns the Name of every registered adapter that is
// present only because a build tag selected it -- "sql" alone when built
// with -tags rgit_sql, empty otherwise. Shared by `rgit
// --version` (app.go) and `rgit doctor` (doctor.go) so the three surfaces
// -- languages, doctor, and --version -- can never disagree about which
// grammars are gated in this exact binary.
func gatedLanguageNames() []string {
	var names []string
	for _, l := range resolve.Languages() {
		if l.Gated {
			names = append(names, l.Name)
		}
	}
	return names
}

// versionGrammarsLine is rgit --version's second line (app.go): which
// optional/gated grammars this exact binary was compiled with, so a user
// can tell a SQL-enabled binary from a plain one without a separate `rgit
// languages` invocation.
func versionGrammarsLine() string {
	gated := gatedLanguageNames()
	if len(gated) == 0 {
		return "optional grammars: none compiled in (see docs/INSTALL.md § SQL support)"
	}
	return "optional grammars: " + strings.Join(gated, " ")
}
