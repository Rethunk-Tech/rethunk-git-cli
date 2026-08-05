// The `rgit doctor` command surface: reports environment health at *run*
// time -- what a caller can actually reach right now -- rather than at
// install time, which cmd/rgit-install's own runPrereqChecks already
// covers for a build from source. Its output shape ("[ok]/MISSING name
// detail") is matched deliberately, so the two commands read as one tool;
// internal/prereq holds that shared probe-and-format mechanism. The check
// *list* stays reimplemented here rather than imported, because doctor's
// own fatal/informational split differs from the installer's (see
// runEnvironmentChecks).
package app

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/prereq"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const doctorHelp = `usage: rgit doctor [--porcelain]

Report environment health: git on PATH, the optional tree-sitter CLI (only
needed to rebuild with SQL support), which language servers this binary can
reach for the extent cross-check, and which grammars it was compiled with.

Exits non-zero only when rgit genuinely cannot function -- a missing
language server or the tree-sitter CLI is informational, since degraded
[ts-only] resolution is normal and documented, not an error.

--porcelain lists stable tab-separated records instead of the aligned
human report: see docs/CODES.md#output-records. Grammars are not repeated
here -- "rgit languages --porcelain" already covers them.

Full reference: docs/USAGE.md
`

// runDoctor's flag surface is doctor.go's own precedent for a command this
// small: hand-parsed rather than pulling in pflag.
func runDoctor(args []string, stdout, stderr io.Writer) exitcode.Code {
	porcelain := false
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprint(stdout, doctorHelp)
			return exitcode.Success
		case "--porcelain":
			porcelain = true
		default:
			fmt.Fprintf(stderr, "rgit: doctor: unrecognized argument %q\n", a)
			fmt.Fprint(stderr, doctorHelp)
			return exitcode.InvalidUsage
		}
	}

	essential, fatal := runEnvironmentChecks()

	servers := make([]prereq.Check, 0, len(lsp.Servers()))
	for _, s := range lsp.Servers() {
		label := s.Bin + " (" + strings.Join(s.Languages, ", ") + ")"
		servers = append(servers, prereq.LookPath(label, s.Bin, "not on PATH -- see docs/INSTALL.md § Language servers"))
	}

	if porcelain {
		fmt.Fprint(stdout, renderDoctorPorcelain(essential, servers))
	} else {
		// One width across both sections, not one per section: the language
		// server names are far longer than git's, and measuring separately
		// would leave the report's two blocks with detail columns that do not
		// line up with each other.
		width := prereq.Width(append(slices.Clip(essential), servers...)...)

		fmt.Fprintln(stdout, "Environment:")
		for _, c := range essential {
			prereq.Print(stdout, width, c)
		}

		fmt.Fprintln(stdout, "\nLanguage servers (optional -- a missing one falls back to [ts-only]; install with docs/INSTALL.md § Language servers):")
		for _, c := range servers {
			prereq.Print(stdout, width, c)
		}

		fmt.Fprintln(stdout, "\nGrammars compiled in:")
		fmt.Fprint(stdout, renderLanguages(resolve.Languages()))
	}

	if fatal != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", fatal)
		return exitcode.GitFailure
	}
	return exitcode.Success
}

// renderDoctorPorcelain renders essential and servers as docs/CODES.md's
// stable tab-separated record: KIND<TAB>NAME<TAB>STATUS<TAB>DETAIL, one line
// per check, no header. KIND is "env" for essential/optional environment
// checks and "server" for language servers -- the same two sections the
// human report prints, just machine-shaped. STATUS is "ok" or "MISSING",
// matching the human report's own two spellings exactly, so a caller
// grepping either output for one recognizes the other. Grammars are
// deliberately excluded: "rgit languages --porcelain" already owns that
// listing, and repeating it here would be a second place to keep in sync.
func renderDoctorPorcelain(essential, servers []prereq.Check) string {
	var buf strings.Builder
	for _, c := range essential {
		writeDoctorRecord(&buf, "env", c)
	}
	for _, c := range servers {
		writeDoctorRecord(&buf, "server", c)
	}
	return buf.String()
}

func writeDoctorRecord(buf *strings.Builder, kind string, c prereq.Check) {
	status := "ok"
	if !c.OK {
		status = "MISSING"
	}
	fmt.Fprintf(buf, "%s\t%s\t%s\t%s\n", kind, c.Name, status, c.Detail)
}

// runEnvironmentChecks probes what rgit needs to run at all, not to build
// it from source. Only git is fatal here (AGENTS.md's delegation boundary:
// rgit shells out to git for everything git already does), unlike
// cmd/rgit-install's runPrereqChecks, which also fails closed on a missing
// go toolchain, CGO_ENABLED, and C compiler -- those three matter only to a
// build from source, never to running an already-built rgit, so doctor
// leaves them out rather than reporting a build-time fact as a run-time
// failure.
//
// The tree-sitter CLI is reported here for the reason the exit-9 rebuild
// hint (shared.go's unsupportedLanguageHint) exists: installing it and
// rebuilding with -tags rgit_sql is the fix for a gated .sql miss, and a
// caller who just saw that hint should be able to check the one thing
// standing between them and it without hunting through docs/INSTALL.md.
func runEnvironmentChecks() (checks []prereq.Check, fatal error) {
	git := prereq.LookPath("git", "git", "not found on PATH -- rgit shells out to git for everything (AGENTS.md)")
	checks = append(checks, git)
	if !git.OK {
		fatal = fmt.Errorf("git not found on PATH -- rgit cannot function without it")
	}

	ts := prereq.LookPath("tree-sitter CLI", "tree-sitter", "optional -- only needed to rebuild with SQL support, see docs/INSTALL.md § SQL support")
	checks = append(checks, ts)

	return checks, fatal
}
