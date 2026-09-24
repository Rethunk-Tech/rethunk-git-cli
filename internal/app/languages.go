// Package app implements the `rgit languages` command surface: lists every grammar compiled into
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
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

const languagesHelp = `usage: rgit languages [--porcelain] [--in-repo]

List every grammar compiled into this binary: name, file extensions, and
whether it is present only because a build tag selected it (currently only
SQL, behind -tags rgit_sql -- see docs/INSTALL.md § SQL support).

--porcelain lists stable tab-separated records instead: see
docs/CODES.md#output-records.

--in-repo filters that listing to grammars with at least one matching
file in the current repository -- advisory only, since the binary
still contains every compiled-in grammar regardless. Requires a git repo;
plain "rgit languages" does not.

Full reference: docs/USAGE.md
`

// runLanguages's flag surface is small enough to parse by hand rather than
// pulling in pflag's machinery the way diff and commit's much larger flag
// surfaces need -- matching doctor.go and completion.go's own minimal style
// for a subcommand this small.
func runLanguages(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	porcelain := false
	inRepo := false
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprint(stdout, languagesHelp)
			return exitcode.Success
		case "--porcelain":
			porcelain = true
		case "--in-repo":
			inRepo = true
		default:
			fmt.Fprintf(stderr, "rgit: languages: unrecognized argument %q\n", a)
			fmt.Fprint(stderr, languagesHelp)
			return exitcode.InvalidUsage
		}
	}

	langs := resolve.Languages()
	if inRepo {
		filtered, code := filterLanguagesInRepo(ctx, dir, langs, stderr)
		if code != exitcode.Success {
			return code
		}
		langs = filtered
	}
	if porcelain {
		fmt.Fprint(stdout, renderLanguagesPorcelain(langs))
	} else {
		fmt.Fprint(stdout, renderLanguages(langs))
	}
	return exitcode.Success
}

// filterLanguagesInRepo narrows langs to the adapters with at least one
// matching file in the repository at dir. Tracked paths use HEAD as a
// fallback for deleted extensionless shebang scripts; untracked paths are
// resolved from the worktree. A gitlink or submodule directory simply never
// matches any adapter (its "file" can't be opened as one), so no separate
// exclusion is needed for those paths.
func filterLanguagesInRepo(ctx context.Context, dir string, langs []resolve.LanguageInfo, stderr io.Writer) ([]resolve.LanguageInfo, exitcode.Code) {
	root, _, repo, code := openRepo(ctx, dir, stderr)
	if code != exitcode.Success {
		return nil, code
	}

	ignoreCase, err := repo.IgnoreCase(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot read core.ignorecase: %v\n", err)
		return nil, exitcode.GitFailure
	}

	tracked, err := repo.LsFilesTracked(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return nil, exitcode.GitFailure
	}

	others, err := repo.LsFilesOthers(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return nil, exitcode.GitFailure
	}

	present := map[string]bool{}
	for _, path := range tracked {
		lang, ok, _, err := resolve.LanguageForPathFolding(root, path, ignoreCase, func() ([]byte, bool, error) {
			return repo.CatFileSample(ctx, "HEAD", path, resolve.ShebangPeekBytes)
		})
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return nil, exitcode.GitFailure
		}
		if ok {
			present[lang.Name()] = true
		}
	}
	for _, path := range others {
		if lang, ok, _ := resolve.LanguageForWorktreePathFolding(root, path, ignoreCase); ok {
			present[lang.Name()] = true
		}
	}

	filtered := make([]resolve.LanguageInfo, 0, len(langs))
	for _, l := range langs {
		if present[l.Name] {
			filtered = append(filtered, l)
		}
	}
	return filtered, exitcode.Success
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
// tab-separated record: NAME<TAB>EXTENSIONS<TAB>GATED<TAB>CROSS-CHECK, one line
// per language sorted by NAME (resolve.Languages() already returns them sorted,
// so no further sort is needed here), no header -- the same "no STATUS column
// when every row would carry the same shape of value" economy rgit commit
// --porcelain already applies (docs/CODES.md), except GATED really does vary
// per row, so it stays. CROSS-CHECK is derived from the lsp package's server
// catalog and reports compile-time wiring, not server reachability.
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
	wired := map[string]struct{}{}
	for _, server := range lsp.Servers() {
		for _, name := range server.Languages {
			wired[name] = struct{}{}
		}
	}
	for _, l := range langs {
		gated := "0"
		if l.Gated {
			gated = "1"
		}
		crossCheck := "ts-only"
		if _, ok := wired[l.Name]; ok {
			crossCheck = "wired"
		}
		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\n", l.Name, strings.Join(l.Extensions, " "), gated, crossCheck)
	}
	return buf.String()
}

// gatedLanguageNames returns the Name of every registered adapter that is
// present only because a build tag selected it -- "sql" alone when built
// with -tags rgit_sql, empty otherwise. Used by `rgit --version` (app.go)
// so it can never disagree with `rgit languages` about which grammars are
// gated in this exact binary.
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
