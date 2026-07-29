// The `rgit languages` command surface: lists every grammar compiled into
// this binary -- name, extensions, and whether it exists only because a
// build tag selected it. resolve.Languages() is the only supported way to
// learn this (AGENTS.md's delegation boundary keeps rgit out of resolve's
// own registry); SQL is the first grammar gated this way, generated at
// install time when the tree-sitter CLI is on PATH (docs/INSTALL.md § SQL
// support), so the same binary can legitimately answer "what do you
// support" two different ways -- this makes the answer runtime-accurate
// instead of the hand-maintained prose that has already gone stale twice.
package app

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const languagesHelp = `usage: rgit languages

List every grammar compiled into this binary: name, file extensions, and
whether it is present only because a build tag selected it (currently only
SQL, behind -tags rgit_sql -- see docs/INSTALL.md § SQL support).

Full reference: docs/USAGE.md
`

// runLanguages has no flags beyond -h/--help: the listing is unconditional,
// so there is nothing yet to filter or format differently. A --porcelain
// form is deliberately not implemented here -- docs/CODES.md is the machine
// contract and has no record shape for this listing, and inventing one
// without a matching entry there would give scripts a contract this repo
// never agreed to. See the worker report for the record shape that would
// need adding first.
func runLanguages(args []string, stdout, stderr io.Writer) exitcode.Code {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, languagesHelp)
		return exitcode.Success
	}
	if len(args) != 0 {
		fmt.Fprintln(stderr, "rgit: languages takes no arguments")
		fmt.Fprint(stderr, languagesHelp)
		return exitcode.InvalidUsage
	}

	fmt.Fprint(stdout, renderLanguages(resolve.Languages()))
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

// gatedLanguageNames returns the Name of every registered adapter that is
// present only because a build tag selected it -- currently "sql" alone
// when built with -tags rgit_sql, empty otherwise. Shared by `rgit
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
