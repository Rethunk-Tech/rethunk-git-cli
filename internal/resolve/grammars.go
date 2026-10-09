package resolve

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tspy "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsrust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve/json5grammar"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve/scssgrammar"
	tsmd "github.com/tree-sitter-grammars/tree-sitter-markdown/bindings/go"
	tstoml "github.com/tree-sitter-grammars/tree-sitter-toml/bindings/go"
	tsyaml "github.com/tree-sitter-grammars/tree-sitter-yaml/bindings/go"
	tsbash "github.com/tree-sitter/tree-sitter-bash/bindings/go"
	tscss "github.com/tree-sitter/tree-sitter-css/bindings/go"
	tshtml "github.com/tree-sitter/tree-sitter-html/bindings/go"
	tsjson "github.com/tree-sitter/tree-sitter-json/bindings/go"
)

// The compiled grammars, in one place so each adapter calls a constructor
// rather than importing the C bindings itself.
//
// ABI 14 vs 15 splits across these grammars by upstream release cadence, not
// by anything rgit chose: TypeScript (v0.23.2), JSON (v0.24.8), YAML
// (v0.7.2), TOML (v0.7.0), and HTML (v0.23.2) report ABI 14; Go, Python, CSS
// (all v0.25.0), Bash (v0.25.1), and Markdown (v0.5.1) report 15 (measured
// from each module's own parser.c LANGUAGE_VERSION). go-tree-sitter v0.25.0
// accepts both, so the skew is not something adapters need to handle. Checked
// against the module proxy: every ABI-14 module here is already pinned to
// its newest tagged release, so none is a lagging pin waiting to be bumped
// -- it is upstream's own ABI split, and go.mod cannot paper over it by
// pinning a version that does not exist.

func goGrammar() *ts.Language { return ts.NewLanguage(tsgo.Language()) }

func pythonGrammar() *ts.Language { return ts.NewLanguage(tspy.Language()) }

func rustGrammar() *ts.Language { return ts.NewLanguage(tsrust.Language()) }

// typescriptGrammar parses .ts and .mts. TSX is a separate grammar rather than
// a mode: the two disagree on whether angle brackets open a type assertion or
// a JSX element, so a .tsx file parsed as TypeScript yields ERROR nodes.
func typescriptGrammar() *ts.Language { return ts.NewLanguage(tsts.LanguageTypescript()) }

func tsxGrammar() *ts.Language { return ts.NewLanguage(tsts.LanguageTSX()) }

// markdownGrammar parses the block grammar only — headings, sections, and
// frontmatter never depend on inline parsing (emphasis, links, code spans),
// so nothing here ever calls tsmd.InlineLanguage. The two grammars ship as
// one Go package (bindings/go holds markdown.go and markdown_inline.go
// together, not two importable packages), so this is the only lever that
// exists to avoid pulling the inline grammar in on purpose: Markdown's
// grammars add ~768 KB, and cgo links per object file, so the unused inline
// copy cannot be dropped once linked.
func markdownGrammar() *ts.Language { return ts.NewLanguage(tsmd.Language()) }

// bashGrammar parses .sh and .bash. Not .zsh: tree-sitter-bash is a POSIX/Bash
// grammar and mis-parses zsh-only syntax (lang_shell.go).
func bashGrammar() *ts.Language { return ts.NewLanguage(tsbash.Language()) }

func yamlGrammar() *ts.Language { return ts.NewLanguage(tsyaml.Language()) }

// cssGrammar parses .css. SCSS has its own grammar (scssGrammar): plain CSS
// parsing yields ERROR nodes on nesting, variables and mixins.
func cssGrammar() *ts.Language { return ts.NewLanguage(tscss.Language()) }

func jsonGrammar() *ts.Language { return ts.NewLanguage(tsjson.Language()) }

func htmlGrammar() *ts.Language { return ts.NewLanguage(tshtml.Language()) }

func tomlGrammar() *ts.Language { return ts.NewLanguage(tstoml.Language()) }

// json5Grammar and scssGrammar come from vendored C (json5grammar,
// scssgrammar): neither upstream repository is importable as a Go module.
func json5Grammar() *ts.Language { return ts.NewLanguage(json5grammar.Language()) }

func scssGrammar() *ts.Language { return ts.NewLanguage(scssgrammar.Language()) }
