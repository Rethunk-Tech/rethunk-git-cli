package main

import (
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
