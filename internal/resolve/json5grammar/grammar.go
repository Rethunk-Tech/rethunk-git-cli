// Package json5grammar wraps the tree-sitter JSON5 grammar's generated C sources
// (github.com/Joakker/tree-sitter-json5 v0.1.0, MIT) as a cgo binding, because that repository
// cannot be imported as a Go module: its go.mod declares the module path github.com/joakker/tree-sitter-json5.git, which Go rejects for the path it is fetched under. The sources are vendored unmodified
// under csrc/.
package json5grammar

// The generated C lives in csrc/, not beside this file: a .c file in the
// package directory is compiled once by cgo's file-globbing and pulled in
// again by the #include below, producing duplicate-symbol link errors.

// #cgo CFLAGS: -std=c11 -fPIC -I${SRCDIR}/csrc
// #include "csrc/parser.c"
import "C"

import "unsafe"

// Language returns the grammar's tree_sitter_json5() constructor in the same
// unsafe.Pointer shape every bindings/go package exposes (grammars.go).
func Language() unsafe.Pointer { return unsafe.Pointer(C.tree_sitter_json5()) }
