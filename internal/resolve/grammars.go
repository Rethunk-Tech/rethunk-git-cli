package resolve

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tspy "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// The compiled grammars, in one place so each adapter calls a constructor
// rather than importing the C bindings itself.
//
// TypeScript reports tree-sitter ABI 14 where Go and Python report 15; the
// runtime accepts both, so the version skew between the grammar modules is not
// something adapters need to handle.

func goGrammar() *ts.Language { return ts.NewLanguage(tsgo.Language()) }

func pythonGrammar() *ts.Language { return ts.NewLanguage(tspy.Language()) }

// typescriptGrammar parses .ts and .mts. TSX is a separate grammar rather than
// a mode: the two disagree on whether angle brackets open a type assertion or
// a JSX element, so a .tsx file parsed as TypeScript yields ERROR nodes.
func typescriptGrammar() *ts.Language { return ts.NewLanguage(tsts.LanguageTypescript()) }

// tsxGrammar parses .tsx and .jsx.
func tsxGrammar() *ts.Language { return ts.NewLanguage(tsts.LanguageTSX()) }
