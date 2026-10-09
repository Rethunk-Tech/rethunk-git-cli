// Package scssgrammar wraps the tree-sitter SCSS grammar's generated C sources
// (github.com/tree-sitter-grammars/tree-sitter-scss v1.0.0, MIT) as a cgo binding, because that repository
// cannot be imported as a Go module: it ships no bindings/go. The sources are vendored unmodified
// under csrc/.
package scssgrammar

// The generated C lives in csrc/, not beside this file: a .c file in the
// package directory is compiled once by cgo's file-globbing and pulled in
// again by the #include below, producing duplicate-symbol link errors.

// #cgo CFLAGS: -std=c11 -fPIC -I${SRCDIR}/csrc
// #include "csrc/parser.c"
// #include "csrc/scanner.c"
import "C"

import "unsafe"

// Language returns the grammar's tree_sitter_scss() constructor in the same
// unsafe.Pointer shape every bindings/go package exposes (grammars.go).
func Language() unsafe.Pointer { return unsafe.Pointer(C.tree_sitter_scss()) }
