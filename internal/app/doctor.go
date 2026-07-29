// The `rgit doctor` command surface: reports environment health at *run*
// time -- what a caller can actually reach right now -- rather than at
// install time, which cmd/rgit-install's own runPrereqChecks already
// covers for a build from source. Its output shape ("[ok]/MISSING name
// detail") is matched deliberately, so the two commands read as one tool;
// the checks themselves are reimplemented here rather than imported,
// because cmd/rgit-install is package main and cannot be imported, and
// because doctor's own fatal/informational split differs from the
// installer's (see runEnvironmentChecks).
package app

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const doctorHelp = `usage: rgit doctor

Report environment health: git on PATH, the optional tree-sitter CLI (only
needed to rebuild with SQL support), which language servers this binary can
reach for the extent cross-check, and which grammars it was compiled with.

Exits non-zero only when rgit genuinely cannot function -- a missing
language server or the tree-sitter CLI is informational, since degraded
[ts-only] resolution is normal and documented, not an error.

Full reference: docs/USAGE.md
`

// doctorCheck is one probed fact, rendered by printCheck.
type doctorCheck struct {
	name   string
	ok     bool
	detail string
}

// languageServerCheck is one row of docs/INSTALL.md § Language servers: the
// binary rgit actually shells out to for that language's cross-check, and
// the install line to print when it is missing. Read from INSTALL.md
// (out of fence) rather than duplicating its own reasoning about transport;
// this is only the subset doctor needs to probe PATH.
type languageServerCheck struct {
	language string
	binary   string
	install  string
}

// languageServers mirrors docs/INSTALL.md's own table. Keep it in sync by
// hand if that table's binaries change -- there is no single source both
// a markdown table and this slice could share without docs/ depending on
// Go, which AGENTS.md's delegation boundary gives no reason to introduce.
var languageServers = []languageServerCheck{
	{"go", "gopls", "go install golang.org/x/tools/gopls@latest"},
	{"typescript/javascript", "vtsls", "npm i -g @vtsls/language-server"},
	{"python", "pyright-langserver", "npm i -g pyright"},
	{"shell", "bash-language-server", "npm i -g bash-language-server"},
}

func runDoctor(args []string, stdout, stderr io.Writer) exitcode.Code {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, doctorHelp)
		return exitcode.Success
	}
	if len(args) != 0 {
		fmt.Fprintln(stderr, "rgit: doctor takes no arguments")
		fmt.Fprint(stderr, doctorHelp)
		return exitcode.InvalidUsage
	}

	fmt.Fprintln(stdout, "Environment:")
	essential, fatal := runEnvironmentChecks()
	for _, c := range essential {
		printCheck(stdout, c)
	}

	fmt.Fprintln(stdout, "\nLanguage servers (optional -- a missing one falls back to [ts-only]):")
	for _, ls := range languageServers {
		path, err := exec.LookPath(ls.binary)
		detail := path
		if err != nil {
			detail = "not on PATH -- " + ls.install
		}
		printCheck(stdout, doctorCheck{name: ls.binary + " (" + ls.language + ")", ok: err == nil, detail: detail})
	}

	fmt.Fprintln(stdout, "\nGrammars compiled in:")
	fmt.Fprint(stdout, renderLanguages(resolve.Languages()))

	if fatal != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", fatal)
		return exitcode.GitFailure
	}
	return exitcode.Success
}

// printCheck matches cmd/rgit-install's own "  [ok] name   detail" line
// shape (its main loop's fmt.Printf) so the two commands read as one tool
// rather than two differently-formatted probes of the same kind of fact.
func printCheck(w io.Writer, c doctorCheck) {
	status := "ok"
	if !c.ok {
		status = "MISSING"
	}
	fmt.Fprintf(w, "  [%s] %-24s %s\n", status, c.name, c.detail)
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
func runEnvironmentChecks() (checks []doctorCheck, fatal error) {
	gitPath, err := exec.LookPath("git")
	checks = append(checks, doctorCheck{
		name: "git", ok: err == nil,
		detail: orNote(gitPath, "not found on PATH -- rgit shells out to git for everything (AGENTS.md)"),
	})
	if err != nil {
		fatal = fmt.Errorf("git not found on PATH -- rgit cannot function without it")
	}

	tsPath, tsErr := exec.LookPath("tree-sitter")
	checks = append(checks, doctorCheck{
		name: "tree-sitter CLI", ok: tsErr == nil,
		detail: orNote(tsPath, "optional -- only needed to rebuild with SQL support, see docs/INSTALL.md § SQL support"),
	})

	return checks, fatal
}

// orNote returns path when non-empty, else note -- exec.LookPath's own
// two-value failure shape collapsed into the one detail string printCheck
// wants.
func orNote(path, note string) string {
	if path != "" {
		return path
	}
	return note
}
