//go:build rgit_sql

// Package sqlgrammar wraps the tree-sitter SQL grammar's compiled C sources
// as a cgo binding, rather than using the upstream module's own bindings/go:
// github.com/DerekStride/tree-sitter-sql gitignores its generated parser.c
// at every published tag (v0.1.0 through v0.3.11), so that package cannot
// compile as fetched from the module proxy.
//
// csrc/ is generated at install time by cmd/rgit-install's generateSQLParser,
// from the grammar.js and tree-sitter.json the module does ship. Nothing
// here imports the upstream module, which is why plain `go build ./...`
// never touches this file and go.mod carries no requirement for it -- see
// specs/design.md § Dependencies.
package sqlgrammar

// The generated C must live in csrc/, not beside this file: a .c file in the
// package directory is compiled once by cgo's file-globbing and pulled in
// again by the #include below, producing "multiple definition of
// 'tree_sitter_sql'" at link time. -I${SRCDIR}/csrc lets csrc/parser.c find
// the tree_sitter/parser.h generateSQLParser copies alongside it.

// #cgo CFLAGS: -std=c11 -fPIC -I${SRCDIR}/csrc
// #include "csrc/parser.c"
// #include "csrc/scanner.c"
import "C"

import "unsafe"

// Language returns the grammar's tree_sitter_sql() constructor in the same
// unsafe.Pointer shape every bindings/go package exposes (grammars.go), so
// lang_sql.go hands it to ts.NewLanguage like any other adapter.
func Language() unsafe.Pointer { return unsafe.Pointer(C.tree_sitter_sql()) }
