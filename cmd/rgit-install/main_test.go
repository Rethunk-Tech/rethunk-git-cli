package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	qt "github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/prereq"
)

// Most subprocess-driving functions here are pure past their one shell-out,
// with that shell-out either injected (a lookPath func) or pulled out into
// its own tested step -- CONTRIBUTING.md's "test the pure logic" boundary.
// git and the go toolchain are hard requirements of this repo (CONTRIBUTING.md,
// internal/gittest), so calling them for real in a test is the real
// dependency, not a double, and stays fast because both commands are cheap.
//
// The exceptions -- runSQLGeneration, downloadSQLGrammarModule, and
// runInstall's actual `go install` -- need the network or a multi-second real
// compile; each carries its own doc comment saying what would catch drift
// there instead of a test.

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

func TestFatalMessage(t *testing.T) {
	t.Parallel()
	qt.Assert(t, qt.Equals(fatalMessage("build failed: %v", "boom"), "rgit-install: build failed: boom\n"))
	qt.Assert(t, qt.Equals(fatalMessage("no args here"), "rgit-install: no args here\n"))
}

func TestParseGOMODOutput(t *testing.T) {
	t.Parallel()

	t.Run("ordinary path", func(t *testing.T) {
		t.Parallel()
		got, err := parseGOMODOutput("/repo/go.mod\n")
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(got, "/repo"))
	})

	t.Run("empty output means no module", func(t *testing.T) {
		t.Parallel()
		_, err := parseGOMODOutput("\n")
		qt.Assert(t, qt.IsNotNil(err))
	})

	// `go env GOMOD` prints os.DevNull, not an empty string, outside any
	// module -- the case this test pins so a future reader does not "fix"
	// away the os.DevNull check as dead code.
	t.Run("devnull means no module", func(t *testing.T) {
		t.Parallel()
		_, err := parseGOMODOutput(os.DevNull + "\n")
		qt.Assert(t, qt.IsNotNil(err))
	})
}

func TestResolveRepoRoot(t *testing.T) {
	t.Parallel()
	// Run from within this checkout, so this is the real dependency
	// (`go env GOMOD`) rather than a double -- go is CONTRIBUTING.md's own
	// hard requirement, and this call is fast (no build, no network).
	got, err := resolveRepoRoot()
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(filepath.IsAbs(got)))
	if _, statErr := os.Stat(filepath.Join(got, "go.mod")); statErr != nil {
		t.Fatalf("resolveRepoRoot returned %q, which has no go.mod: %v", got, statErr)
	}
}

func TestPrereqFatal(t *testing.T) {
	t.Parallel()

	ok := prereq.Check{OK: true}
	failed := prereq.Check{OK: false}

	tests := []struct {
		name                                 string
		goCheck, gitCheck, cgoCheck, ccCheck prereq.Check
		wantNil                              bool
		wantContains                         string
	}{
		{"all pass", ok, ok, ok, ok, true, ""},
		{"go missing", failed, ok, ok, ok, false, "go"},
		{"git missing", ok, failed, ok, ok, false, "git"},
		{"cgo disabled", ok, ok, prereq.Check{OK: false, Detail: "0"}, ok, false, "cgo"},
		{"C compiler missing", ok, ok, ok, failed, false, "C compiler"},
		// Regression this guards: with go AND git both missing, the caller
		// must see one root cause, not whichever error happened to be
		// constructed last -- go's own message, since it is checked first.
		{"go and git both missing reports go first", failed, failed, ok, ok, false, "go not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := prereqFatal(tt.goCheck, tt.gitCheck, tt.cgoCheck, tt.ccCheck, "gcc")
			if tt.wantNil {
				qt.Assert(t, qt.IsNil(err))
				return
			}
			qt.Assert(t, qt.IsNotNil(err))
			qt.Assert(t, qt.StringContains(err.Error(), tt.wantContains))
		})
	}
}

func TestRunPrereqChecks(t *testing.T) {
	t.Parallel()
	// Real toolchain, run from within this checkout: go, git, CGO, and a
	// C compiler are hard requirements to even build this repo
	// (AGENTS.md's delegation boundary, CONTRIBUTING.md's cgo note), so
	// this environment always passes those four -- pinning that the
	// orchestration itself (five checks, fatal nil when those four are
	// sound) stays wired correctly. tree-sitter CLI is informational:
	// SQL generation degrades without it.
	checks, fatal := runPrereqChecks()
	qt.Assert(t, qt.IsNil(fatal))
	qt.Assert(t, qt.HasLen(checks, 5))

	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = c.Name
		fatalOK := c.Name == "go toolchain" || c.Name == "git" || c.Name == "CGO_ENABLED" || strings.HasPrefix(c.Name, "C compiler (")
		if fatalOK {
			qt.Assert(t, qt.IsTrue(c.OK), qt.Commentf("fatal check %q failed", c.Name))
		}
	}
	qt.Assert(t, qt.SliceContains(names, "go toolchain"))
	qt.Assert(t, qt.SliceContains(names, "git"))
	qt.Assert(t, qt.SliceContains(names, "CGO_ENABLED"))
	qt.Assert(t, qt.SliceContains(names, "tree-sitter CLI"))
}

func TestGoEnv(t *testing.T) {
	t.Parallel()
	// Real toolchain: GOPATH is always non-empty once `go env` has a
	// default to fall back to, so this is a safe, fast check against the
	// real dependency rather than a double for "does `go env` succeed."
	qt.Assert(t, qt.Not(qt.Equals(goEnv("GOPATH"), "")))

	// `go env` prints an empty line for an unrecognized name rather than
	// erroring -- goEnv's TrimSpace-of-empty-output path, pinned so a
	// future reader knows this returns "", not an error.
	qt.Assert(t, qt.Equals(goEnv("RGIT_INSTALL_NOT_A_REAL_GO_ENV_VAR"), ""))
}

func TestParseSQLAdapterListing(t *testing.T) {
	t.Parallel()

	// go list -json ./... streams concatenated JSON objects, not an array.
	const adapterFound = `{"Dir":"/repo/internal/resolve/sqlgrammar","Name":"sqlgrammar","CgoFiles":["grammar.go"]}
{"Dir":"/repo/internal/resolve","Name":"resolve","CgoFiles":[]}`

	t.Run("adapter present with cgo files", func(t *testing.T) {
		t.Parallel()
		dir, ok := parseSQLAdapterListing([]byte(adapterFound))
		qt.Assert(t, qt.IsTrue(ok))
		qt.Assert(t, qt.Equals(dir, "/repo/internal/resolve/sqlgrammar"))
	})

	// The regression this guards: findSQLAdapter's own doc comment records
	// that matching by name alone once matched an unrelated package that
	// merely shared it -- len(CgoFiles) > 0 is what tells the cgo binding
	// apart from a same-named non-cgo package.
	t.Run("name matches but no cgo files -- not the adapter", func(t *testing.T) {
		t.Parallel()
		const noCgo = `{"Dir":"/repo/somewhere","Name":"sqlgrammar","CgoFiles":[]}`
		_, ok := parseSQLAdapterListing([]byte(noCgo))
		qt.Assert(t, qt.IsFalse(ok))
	})

	t.Run("adapter absent", func(t *testing.T) {
		t.Parallel()
		const noAdapter = `{"Dir":"/repo/internal/app","Name":"app","CgoFiles":[]}`
		_, ok := parseSQLAdapterListing([]byte(noAdapter))
		qt.Assert(t, qt.IsFalse(ok))
	})

	t.Run("malformed JSON", func(t *testing.T) {
		t.Parallel()
		_, ok := parseSQLAdapterListing([]byte("not json"))
		qt.Assert(t, qt.IsFalse(ok))
	})
}

func TestFindSQLAdapter(t *testing.T) {
	t.Parallel()
	// Real toolchain, real repo: internal/resolve/sqlgrammar genuinely is
	// the SQL adapter package in this checkout, so this proves
	// findSQLAdapter's own `go list` invocation and its delegation to
	// parseSQLAdapterListing (tested above in isolation) actually wire
	// together end to end, not just that the parsing logic alone is right.
	repoRoot, err := resolveRepoRoot()
	qt.Assert(t, qt.IsNil(err))

	dir, ok := findSQLAdapter(repoRoot)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.StringContains(dir, filepath.Join("internal", "resolve", "sqlgrammar")))
}

func TestFindSQLAdapterOutsideAModule(t *testing.T) {
	t.Parallel()
	// `go list` itself fails outside a module -- findSQLAdapter's other
	// return path, not exercised by the happy-path case above.
	_, ok := findSQLAdapter(t.TempDir())
	qt.Assert(t, qt.IsFalse(ok))
}

func TestGenerateSQLParserWith(t *testing.T) {
	t.Parallel()
	// The one branch reachable without the network or a real tree-sitter
	// CLI: not found on PATH at all, the common case on a machine that
	// never opted into SQL support. Everything past this needs both for
	// real -- see generateSQLParser's own doc comment.
	ok, msg := generateSQLParserWith("/repo", "/repo/internal/resolve/sqlgrammar", false,
		func(string) (string, error) { return "", os.ErrNotExist })
	qt.Assert(t, qt.IsFalse(ok))
	qt.Assert(t, qt.StringContains(msg, "tree-sitter CLI not found on PATH"))
}

func TestInstallArgs(t *testing.T) {
	t.Parallel()

	t.Run("without SQL", func(t *testing.T) {
		t.Parallel()
		got := installArgs(false, "v1.2.3")
		qt.Assert(t, qt.DeepEquals(got, []string{"install", "-ldflags", "-s -w -X main.version=v1.2.3", "./cmd/rgit"}))
	})

	// The regression this guards: -tags rgit_sql must land before the
	// package path, not appended after it (go install parses flags
	// positionally -- a misplaced -tags silently becomes a package argument
	// instead of a flag).
	t.Run("with SQL, -tags lands before the package path", func(t *testing.T) {
		t.Parallel()
		got := installArgs(true, "")
		qt.Assert(t, qt.DeepEquals(got, []string{"install", "-ldflags", "-s -w", "-tags", "rgit_sql", "./cmd/rgit"}))
	})
}

func TestGitVersion(t *testing.T) {
	t.Parallel()

	dir, _ := gittest.New(t.Context(), t)
	gittest.Write(t, dir, "f.txt", "one\n")
	gittest.Commit(t.Context(), t, dir, "initial")
	gittest.Git(t.Context(), t, dir, "tag", "v1.2.3")

	qt.Assert(t, qt.Equals(gitVersion(dir), "v1.2.3"))

	gittest.Write(t, dir, "untracked.txt", "no\n")
	qt.Assert(t, qt.Equals(gitVersion(dir), "v1.2.3"))

	// git describe's own "-dirty" suffix -- the exact spelling
	// cmd/rgit/main.go's resolveVersion and the Makefile's cross-build
	// filenames both already match against; a change here would silently
	// desync all three.
	gittest.Write(t, dir, "f.txt", "one\nmodified\n")
	qt.Assert(t, qt.Equals(gitVersion(dir), "v1.2.3-dirty"))
}

func TestGitVersionNotARepo(t *testing.T) {
	t.Parallel()
	qt.Assert(t, qt.Equals(gitVersion(t.TempDir()), ""))
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
		qt.Assert(t, qt.IsNil(os.WriteFile(src, []byte("hello"), 0o600)))
		qt.Assert(t, qt.IsNil(os.MkdirAll(filepath.Dir(dst), 0o750)))
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

// finalizeGenerated is runSQLGeneration's own finishing swap, factored out
// so its atomicity is testable without a real tree-sitter CLI: the two
// directories it moves between don't care what generated them.
func TestFinalizeGenerated(t *testing.T) {
	t.Parallel()

	t.Run("no prior final: staging becomes final", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		final := filepath.Join(dir, "csrc")
		staging := filepath.Join(dir, "csrc.tmp")
		qt.Assert(t, qt.IsNil(os.MkdirAll(staging, 0o750)))
		qt.Assert(t, qt.IsNil(os.WriteFile(filepath.Join(staging, "new.txt"), []byte("new"), 0o600)))

		qt.Assert(t, qt.IsNil(finalizeGenerated(staging, final)))

		got, err := os.ReadFile(filepath.Join(final, "new.txt"))
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(string(got), "new"))
	})

	t.Run("prior final replaced on success, .old cleaned up", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		final := filepath.Join(dir, "csrc")
		qt.Assert(t, qt.IsNil(os.MkdirAll(final, 0o750)))
		qt.Assert(t, qt.IsNil(os.WriteFile(filepath.Join(final, "old.txt"), []byte("old"), 0o600)))

		staging := filepath.Join(dir, "csrc.tmp")
		qt.Assert(t, qt.IsNil(os.MkdirAll(staging, 0o750)))
		qt.Assert(t, qt.IsNil(os.WriteFile(filepath.Join(staging, "new.txt"), []byte("new"), 0o600)))

		qt.Assert(t, qt.IsNil(finalizeGenerated(staging, final)))

		_, err := os.Stat(filepath.Join(final, "old.txt"))
		qt.Assert(t, qt.IsTrue(os.IsNotExist(err)))
		got, err := os.ReadFile(filepath.Join(final, "new.txt"))
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(string(got), "new"))
		_, err = os.Stat(final + ".old")
		qt.Assert(t, qt.IsTrue(os.IsNotExist(err)))
	})

	// The regression this guards against: a failure finalizing must restore
	// the prior csrc/ exactly, not delete it and then fail with nothing to
	// build from. staging pointing nowhere is a deterministic way to make
	// the finishing os.Rename fail without touching permissions.
	t.Run("prior final restored when the finishing rename fails", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		final := filepath.Join(dir, "csrc")
		qt.Assert(t, qt.IsNil(os.MkdirAll(final, 0o750)))
		marker := filepath.Join(final, "marker.txt")
		qt.Assert(t, qt.IsNil(os.WriteFile(marker, []byte("prior generation"), 0o600)))

		staging := filepath.Join(dir, "csrc.tmp-never-created")

		err := finalizeGenerated(staging, final)
		qt.Assert(t, qt.IsNotNil(err))

		got, rerr := os.ReadFile(marker)
		qt.Assert(t, qt.IsNil(rerr))
		qt.Assert(t, qt.Equals(string(got), "prior generation"))
		_, statErr := os.Stat(final + ".old")
		qt.Assert(t, qt.IsTrue(os.IsNotExist(statErr)))
	})
}

func TestSQLCSRCContentHash(t *testing.T) {
	t.Parallel()

	writeCSRC := func(t *testing.T, content string) string {
		t.Helper()
		pkgDir := t.TempDir()
		csrc := filepath.Join(pkgDir, "csrc")
		qt.Assert(t, qt.IsNil(os.MkdirAll(csrc, 0o750)))
		qt.Assert(t, qt.IsNil(os.WriteFile(filepath.Join(csrc, "parser.c"), []byte(content), 0o600)))
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

	// runInstall's cache-busting only defeats a stale go build cache if
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
		qt.Assert(t, qt.IsNil(os.WriteFile(path, []byte(content), 0o600)))
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
