//go:build rgit_sql

package sqlgrammar

import _ "embed"

// This file's only job is to fail the build fast, with a diagnosable
// message, when csrc/ was never generated -- go:embed's own compile-time
// existence check on csrc/scanner.c fires during the Go compiler's package
// load, before grammar.go's cgo #include "csrc/parser.c" / "csrc/scanner.c"
// ever reaches the C compiler. Without this, a `-tags rgit_sql` build run
// before `make sql-parser` fails inside cgo instead: either the C
// preprocessor's own bare "No such file or directory" (no mention that a
// generation step exists at all), or -- worse, if csrc/ exists but is
// stale or partial -- an undefined-reference link error naming a C symbol
// like tree_sitter_sql_external_scanner_create, which gives no hint that
// the fix is regenerating a file, not writing missing code. go:embed's own
// "pattern csrc/scanner.c: no matching files found" at least names the
// missing file plainly; CONTRIBUTING.md's own release step ("run
// `make sql-parser`") is one grep away from there.
//
// scanner.c is checked, not parser.c, though parser.c is the file this
// package's own doc comment (grammar.go) explains upstream gitignores at
// every tag: both are produced together by the same `tree-sitter generate`
// step (cmd/rgit-install's generateSQLParser), so either is an equally
// valid proxy for "was this step run" -- but parser.c is tens of megabytes
// (measured ~17MB) and a go:embed target becomes part of this package's own
// compiled data on top of cgo already compiling it once, so embedding it a
// second time would bloat every rgit_sql binary by that much for a check
// scanner.c, a few kilobytes, answers just as well.
//
// The embedded content is never read: the blank identifier means nothing
// here needs it to be, and there is nothing useful to do with generated C
// source as a Go string.
//
//go:embed csrc/scanner.c
var _ string
