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

// TestRun_LanguagesOmitsSQLWithoutTag pins `rgit languages` and `rgit
// --version`'s agreement when the tag is off: neither may claim SQL is
// compiled in, and --version says so explicitly rather than by omission.
func TestRun_LanguagesOmitsSQLWithoutTag(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, _, code := runApp(t, "languages")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "sql")))

	version, _, code := runApp(t, "--version")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(version, "optional grammars: none compiled in"))
}

// TestRun_DoctorOmitsSQLWithoutTag is doctor's own agreement with the
// above: its grammar section reuses the identical resolve.Languages() data.
func TestRun_DoctorOmitsSQLWithoutTag(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, _, code := runApp(t, "doctor")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Not(qt.StringContains(stdout, "sql")))
}

// TestRun_SQLAnchorWithoutTagHintsRebuild pins deliverable 3b's negative
// case: a binary built without rgit_sql still fails a .sql anchor with the
// same generic "no grammar registered" exit 9 every unsupported language
// gets (TestRun_UnsupportedLanguageGetsNoRebuildHint in app_test.go covers
// that shared refusal), but the message also names the build tag and
// points at the fix -- unlike a language this resolver has never supported.
func TestRun_SQLAnchorWithoutTagHintsRebuild(t *testing.T) {
	dir := chdirTempRepo(t)
	writeAppFile(t, dir, "q.sql", "SELECT 1;\n")
	gittest.Commit(t, dir, "chore: add sql fixture")
	writeAppFile(t, dir, "q.sql", "SELECT 2;\n")

	_, stderr, code := runApp(t, "commit", "-m", "feat(x): y", "q.sql:Anything")

	qt.Assert(t, qt.Equals(code, exitcode.UnsupportedLanguage))
	qt.Assert(t, qt.StringContains(stderr, "no grammar registered for .sql"))
	qt.Assert(t, qt.StringContains(stderr, "rgit_sql"))
	qt.Assert(t, qt.StringContains(stderr, "docs/INSTALL.md"))
}
