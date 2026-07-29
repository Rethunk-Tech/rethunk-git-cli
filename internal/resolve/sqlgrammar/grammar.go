//go:build rgit_sql

// Package sqlgrammar wraps the tree-sitter SQL grammar's compiled C sources
// as a cgo binding. It is intentionally not the upstream module's own
// bindings/go package: github.com/DerekStride/tree-sitter-sql gitignores its
// generated parser.c at every published tag (v0.1.0 through v0.3.11), so
// that package's own "#include "../../src/parser.c"" cannot compile as
// fetched from the module proxy.
//
// This package's csrc/ subdirectory is generated at install/build time --
// see cmd/rgit-install/main.go's generateSQLParser -- from the module's own
// grammar.js and tree-sitter.json (both of which the module does ship), via
// `tree-sitter generate`. Nothing here imports the upstream module's Go
// package at all, cgo binding included, which is why plain `go build ./...`
// (no rgit_sql tag) never touches this file and go.mod carries no
// requirement for the grammar module -- see specs/design.md § Dependencies.
package sqlgrammar

// The generated C must live in csrc/, a subdirectory of this package, not
// beside this file: measured, a .c file placed directly in the package
// directory is compiled once by cgo's own file-globbing and pulled in a
// second time by the #include below, producing "multiple definition of
// 'tree_sitter_sql'" at link time. -I${SRCDIR}/csrc lets csrc/parser.c
// #include "tree_sitter/parser.h" (copied alongside it by generateSQLParser)
// the same way the grammar's own source tree lays it out.

// #cgo CFLAGS: -std=c11 -fPIC -I${SRCDIR}/csrc
// #include "csrc/parser.c"
// #include "csrc/scanner.c"
import "C"

import "unsafe"

// Language returns the compiled grammar's tree_sitter_sql() constructor, in
// the same unsafe.Pointer shape every other grammar in this resolver's
// bindings/go packages exposes (grammars.go), so lang_sql.go can hand it to
// ts.NewLanguage exactly like any other adapter.
func Language() unsafe.Pointer { return unsafe.Pointer(C.tree_sitter_sql()) }
