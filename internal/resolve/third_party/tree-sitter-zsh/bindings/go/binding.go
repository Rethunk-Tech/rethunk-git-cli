// Package tree_sitter_zsh supplies the Language symbol that
// github.com/georgeharker/tree-sitter-zsh v0.63.5 binding tests pass to
// tree_sitter.NewLanguage. Nothing publishes that module path, so this
// package exists only to typecheck those tests. The zsh grammar this repo
// parses with is the georgeharker module.
package tree_sitter_zsh

import "unsafe"

// Language is the pointer shape NewLanguage accepts. It is not a grammar.
func Language() unsafe.Pointer { return nil }
