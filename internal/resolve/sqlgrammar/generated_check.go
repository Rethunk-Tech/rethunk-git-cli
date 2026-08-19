//go:build rgit_sql

package sqlgrammar

import _ "embed"

// This file fails the build fast, with a diagnosable message, when csrc/ was
// never generated: go:embed's existence check fires during package load,
// before grammar.go's cgo #include reaches the C compiler. Without it a
// `-tags rgit_sql` build run before `make sql-parser` dies inside cgo --
// either a bare "No such file or directory", or, if csrc/ is stale, an
// undefined-reference link error naming a C symbol, neither of which hints
// that the fix is regenerating a file.
//
// scanner.c is the probe rather than parser.c though both are produced by
// the same `tree-sitter generate`: parser.c is ~17MB, and a go:embed target
// becomes part of the compiled package on top of cgo compiling it once, so
// embedding it would bloat every rgit_sql binary by that much. The content
// is never read; there is nothing useful to do with generated C as a Go
// string.
//
//go:embed csrc/scanner.c
var _ string
