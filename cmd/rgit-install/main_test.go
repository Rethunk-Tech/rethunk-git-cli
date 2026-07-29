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

func TestResolvePrefix(t *testing.T) {
	t.Parallel()
	// Only the flagPrefix branch is a pure transformation; the GOBIN/GOPATH
	// fallback shells out to `go env` via goEnv and is exercised by hand
	// instead, rather than mocked here.
	got, err := resolvePrefix("/custom/prefix")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(got, "/custom/prefix"))
}

func TestCopyFile(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		src := filepath.Join(dir, "src.txt")
		dst := filepath.Join(dir, "nested", "dst.txt")
		qt.Assert(t, qt.IsNil(os.WriteFile(src, []byte("hello"), 0o644)))
		qt.Assert(t, qt.IsNil(os.MkdirAll(filepath.Dir(dst), 0o755)))
		qt.Assert(t, qt.IsNil(copyFile(src, dst)))

		got, err := os.ReadFile(dst)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(string(got), "hello"))
	})

	t.Run("missing source", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		err := copyFile(filepath.Join(dir, "nope"), filepath.Join(dir, "dst"))
		qt.Assert(t, qt.IsNotNil(err))
	})
}

func TestInstallBinary(t *testing.T) {
	t.Parallel()

	t.Run("fresh install reports not replaced", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		bin := filepath.Join(dir, "built")
		qt.Assert(t, qt.IsNil(os.WriteFile(bin, []byte("binary content"), 0o644)))
		dest := filepath.Join(dir, "prefix", "rgit") // prefix/ does not exist yet

		replaced, err := installBinary(bin, dest)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.IsFalse(replaced))

		got, err := os.ReadFile(dest)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(string(got), "binary content"))

		info, err := os.Stat(dest)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(info.Mode().Perm(), os.FileMode(0o755)))
	})

	// main's "Installed" vs "Replaced existing binary at" wording reads
	// directly off this return value.
	t.Run("existing binary reports replaced and its content is overwritten", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		bin := filepath.Join(dir, "built")
		qt.Assert(t, qt.IsNil(os.WriteFile(bin, []byte("new content"), 0o644)))
		dest := filepath.Join(dir, "rgit")
		qt.Assert(t, qt.IsNil(os.WriteFile(dest, []byte("old content"), 0o755)))

		replaced, err := installBinary(bin, dest)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.IsTrue(replaced))

		got, err := os.ReadFile(dest)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(string(got), "new content"))
	})

	t.Run("missing source binary", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := installBinary(filepath.Join(dir, "nope"), filepath.Join(dir, "dest"))
		qt.Assert(t, qt.IsNotNil(err))
	})
}

func TestSQLCSRCContentHash(t *testing.T) {
	t.Parallel()

	writeCSRC := func(t *testing.T, content string) string {
		t.Helper()
		pkgDir := t.TempDir()
		csrc := filepath.Join(pkgDir, "csrc")
		qt.Assert(t, qt.IsNil(os.MkdirAll(csrc, 0o755)))
		qt.Assert(t, qt.IsNil(os.WriteFile(filepath.Join(csrc, "parser.c"), []byte(content), 0o644)))
		return pkgDir
	}

	t.Run("deterministic for the same content", func(t *testing.T) {
		t.Parallel()
		pkgDir := writeCSRC(t, "same content")
		h1, err1 := sqlCSRCContentHash(pkgDir)
		h2, err2 := sqlCSRCContentHash(pkgDir)
		qt.Assert(t, qt.IsNil(err1))
		qt.Assert(t, qt.IsNil(err2))
		qt.Assert(t, qt.Equals(h1, h2))
	})

	// buildBinary's cache-busting only defeats a stale go build cache if
	// this hash actually changes when csrc/ content does. A hash that
	// stayed constant across different content would silently make the
	// cache-busting a no-op.
	t.Run("different content changes the hash", func(t *testing.T) {
		t.Parallel()
		a := writeCSRC(t, "content A")
		b := writeCSRC(t, "content B")
		ha, erra := sqlCSRCContentHash(a)
		hb, errb := sqlCSRCContentHash(b)
		qt.Assert(t, qt.IsNil(erra))
		qt.Assert(t, qt.IsNil(errb))
		qt.Assert(t, qt.Not(qt.Equals(ha, hb)))
	})

	t.Run("missing csrc dir", func(t *testing.T) {
		t.Parallel()
		_, err := sqlCSRCContentHash(t.TempDir())
		qt.Assert(t, qt.IsNotNil(err))
	})
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

	// An outdated tree-sitter CLI can emit ABI 14 despite tree-sitter.json
	// sitting right next to grammar.js; catching that is the whole point of
	// checking LANGUAGE_VERSION directly.
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
