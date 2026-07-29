package resolve

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tspy "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	tsmd "github.com/tree-sitter-grammars/tree-sitter-markdown/bindings/go"
	tstoml "github.com/tree-sitter-grammars/tree-sitter-toml/bindings/go"
	tsyaml "github.com/tree-sitter-grammars/tree-sitter-yaml/bindings/go"
	tsbash "github.com/tree-sitter/tree-sitter-bash/bindings/go"
	tscss "github.com/tree-sitter/tree-sitter-css/bindings/go"
	tsjson "github.com/tree-sitter/tree-sitter-json/bindings/go"
)

// The compiled grammars, in one place so each adapter calls a constructor
// rather than importing the C bindings itself.
//
// ABI 14 vs 15 splits across these grammars by upstream release cadence, not
// by anything rgit chose: TypeScript (v0.23.2) and JSON (v0.24.8) report ABI
// 14 where Go, Python, and CSS (all v0.25.0) report 15 (measured from each
// module's own parser.c LANGUAGE_VERSION). go-tree-sitter v0.25.0 accepts
// both, so the skew is not something adapters need to handle. Checked
// against the module proxy: both v0.23.2 and v0.24.8 are already each
// module's newest tagged release, so this is not a lagging pin waiting to
// be bumped -- it is upstream's own ABI split, and go.mod cannot paper over
// it by pinning a version that does not exist.

func goGrammar() *ts.Language { return ts.NewLanguage(tsgo.Language()) }

func pythonGrammar() *ts.Language { return ts.NewLanguage(tspy.Language()) }

// typescriptGrammar parses .ts and .mts. TSX is a separate grammar rather than
// a mode: the two disagree on whether angle brackets open a type assertion or
// a JSX element, so a .tsx file parsed as TypeScript yields ERROR nodes.
func typescriptGrammar() *ts.Language { return ts.NewLanguage(tsts.LanguageTypescript()) }

// tsxGrammar parses .tsx and .jsx.
func tsxGrammar() *ts.Language { return ts.NewLanguage(tsts.LanguageTSX()) }

// markdownGrammar parses the block grammar only — headings, sections, and
// frontmatter never depend on inline parsing (emphasis, links, code spans),
// so nothing here ever calls tsmd.InlineLanguage. The two grammars ship as
// one Go package (bindings/go holds markdown.go and markdown_inline.go
// together, not two importable packages), so this is the only lever that
// exists to avoid pulling the inline grammar in on purpose; see design.md §
// Dependencies for what that is actually measured to cost.
func markdownGrammar() *ts.Language { return ts.NewLanguage(tsmd.Language()) }

// bashGrammar parses .sh and .bash. Not .zsh: tree-sitter-bash is a POSIX/Bash
// grammar and mis-parses zsh-only syntax (lang_shell.go).
func bashGrammar() *ts.Language { return ts.NewLanguage(tsbash.Language()) }

// yamlGrammar parses .yaml and .yml.
func yamlGrammar() *ts.Language { return ts.NewLanguage(tsyaml.Language()) }

// cssGrammar parses .css. Not .scss/.sass: no SCSS/SASS tree-sitter grammar
// ships Go bindings (specs/design.md § Dependencies) -- an upstream gap,
// not a scoping choice, the same distinction lang_shell.go draws for .zsh.
func cssGrammar() *ts.Language { return ts.NewLanguage(tscss.Language()) }

// jsonGrammar parses .json.
func jsonGrammar() *ts.Language { return ts.NewLanguage(tsjson.Language()) }

// tomlGrammar parses .toml.
func tomlGrammar() *ts.Language { return ts.NewLanguage(tstoml.Language()) }
