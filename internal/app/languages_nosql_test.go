//go:build !rgit_sql

// Coverage for a plain build (no -tags rgit_sql), the default `go test
// ./...` lane runs. languages_sql_test.go is this file's mirror for a
// build with the tag; see its own doc comment for why the split exists.
package app

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

// TestRun_LanguagesAndDoctorOmitSQLWithoutTag pins the one build-specific
// guarantee across every rendering of resolve.Languages() this binary
// exposes: without -tags rgit_sql, nothing may claim SQL is compiled in --
// not `rgit languages`'s plain or --porcelain output, not `rgit doctor`'s
// grammar section (which reuses the identical data), and not `rgit
// --version`'s own summary line, which says so explicitly rather than by
// omission. The negative case (an actual .sql anchor) still fails with the
// same generic "no grammar registered" exit 9 every unsupported language
// gets (TestRun_UnsupportedLanguageGetsNoRebuildHint in app_test.go covers
// that shared refusal), but the message also names the build tag and
// points at the rebuild -- unlike a language this resolver has never
// supported at all.
//
// Each subtest is sequential, not parallel: several call t.Chdir, which
// forbids it.
func TestRun_LanguagesAndDoctorOmitSQLWithoutTag(t *testing.T) {
	t.Run("languages and --version agree", func(t *testing.T) {
		t.Chdir(t.TempDir())

		stdout, _, code := runApp(t, "languages")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "sql")))

		version, _, code := runApp(t, "--version")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.StringContains(version, "optional grammars: none compiled in"))
	})

	// Without the grammar compiled in, there is no "sql" record at all --
	// not one with GATED "0", which would wrongly claim the grammar exists
	// but happens not to be gated.
	t.Run("languages --porcelain", func(t *testing.T) {
		t.Chdir(t.TempDir())

		stdout, _, code := runApp(t, "languages", "--porcelain")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "sql\t")))
	})

	t.Run("doctor", func(t *testing.T) {
		t.Chdir(t.TempDir())

		stdout, _, code := runApp(t, "doctor")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Not(qt.StringContains(stdout, "sql")))
	})

	t.Run("sql anchor still refused, with a rebuild hint", func(t *testing.T) {
		dir := chdirTempRepo(t)
		writeAppFile(t, dir, "q.sql", "SELECT 1;\n")
		gittest.Commit(t.Context(), t, dir, "chore: add sql fixture")
		writeAppFile(t, dir, "q.sql", "SELECT 2;\n")

		_, stderr, code := runApp(t, "commit", "-m", "feat(x): y", "q.sql:Anything")

		qt.Assert(t, qt.Equals(code, exitcode.UnsupportedLanguage))
		qt.Assert(t, qt.StringContains(stderr, "no grammar registered for q.sql"))
		qt.Assert(t, qt.StringContains(stderr, `extension ".sql"`))
		qt.Assert(t, qt.StringContains(stderr, "rgit_sql"))
		qt.Assert(t, qt.StringContains(stderr, "docs/INSTALL.md"))
	})

	// commit is not the only command that meets a gated grammar, and a
	// gated miss is only useful if it says so wherever it happens: symbols
	// used to refuse the same file with a bare message, leaving the reader
	// to guess the grammar existed at all.
	t.Run("symbols reports the gated miss the same way commit does", func(t *testing.T) {
		dir := chdirTempRepo(t)
		writeAppFile(t, dir, "q.sql", "SELECT 1;\n")
		gittest.Commit(t.Context(), t, dir, "chore: add sql fixture")

		_, stderr, code := runApp(t, "symbols", "q.sql")

		qt.Assert(t, qt.Equals(code, exitcode.UnsupportedLanguage))
		qt.Assert(t, qt.StringContains(stderr, "rgit_sql"))
		qt.Assert(t, qt.StringContains(stderr, "docs/INSTALL.md"))
	})
}
