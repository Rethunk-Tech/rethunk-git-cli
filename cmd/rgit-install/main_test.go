package main

import (
	"os"
	"path/filepath"
	"testing"

	qt "github.com/go-quicktest/qt"
)

// The subprocess-driving functions (build, install, generation) are exercised
// by hand against a real toolchain rather than mocked here -- CONTRIBUTING.md
// prefers the real dependency over a double, and there is no meaningful
// double for "does `go build` succeed." These are the pure helpers left over.

func TestLdflags(t *testing.T) {
	t.Parallel()
	qt.Assert(t, qt.Equals(ldflags(""), "-s -w"))
	qt.Assert(t, qt.Equals(ldflags("v1.2.3"), "-s -w -X main.version=v1.2.3"))
}

func TestVersionOrDev(t *testing.T) {
	t.Parallel()
	qt.Assert(t, qt.Equals(versionOrDev(""), "dev"))
	qt.Assert(t, qt.Equals(versionOrDev("abc123"), "abc123"))
}

func TestFirstField(t *testing.T) {
	t.Parallel()
	qt.Assert(t, qt.Equals(firstField(""), ""))
	qt.Assert(t, qt.Equals(firstField("gcc"), "gcc"))
	qt.Assert(t, qt.Equals(firstField("zig cc -target x86_64-linux-gnu"), "zig"))
}

func TestRelTo(t *testing.T) {
	t.Parallel()
	qt.Assert(t, qt.Equals(relTo("/repo", "/repo/internal/resolve/sql"), "internal/resolve/sql"))
	// A path outside root falls back to itself rather than erroring.
	qt.Assert(t, qt.Equals(relTo("/repo", "relative/path"), "relative/path"))
}

func TestParserABIVersion(t *testing.T) {
	t.Parallel()

	write := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "parser.c")
		qt.Assert(t, qt.IsNil(os.WriteFile(path, []byte(content), 0o644)))
		return path
	}

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		path := write(t, "#define LANGUAGE_VERSION 15\n#define STATE_COUNT 4\n")
		abi, err := parserABIVersion(path)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(abi, 15))
	})

	// The regression this guards: an outdated tree-sitter CLI emitting ABI
	// 14 despite tree-sitter.json sitting right next to grammar.js.
	t.Run("stale ABI", func(t *testing.T) {
		t.Parallel()
		path := write(t, "#define LANGUAGE_VERSION 14\n")
		abi, err := parserABIVersion(path)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(abi, 14))
	})

	t.Run("no define", func(t *testing.T) {
		t.Parallel()
		path := write(t, "// nothing useful here\n")
		_, err := parserABIVersion(path)
		qt.Assert(t, qt.IsNotNil(err))
	})
}
