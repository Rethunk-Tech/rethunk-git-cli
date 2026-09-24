//go:build rgit_sql

// Coverage for a binary built WITH the SQL grammar (-tags rgit_sql).
// languages_nosql_test.go is this file's mirror for a plain build; between
// the two, a change that breaks the same binary's ability to answer "what
// do you support" two different ways must fail in whichever lane it
// actually breaks it in, per CONTRIBUTING.md.
package app

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

// TestRun_LanguagesListsSQLWhenTagged pins `rgit languages` and `rgit
// --version`'s agreement when the tag is on: both must name "sql" and mark
// it gated.
func TestRun_LanguagesListsSQLWhenTagged(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	stdout, _, code := runApp(t, "-C", cwd, "languages")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "sql"))
	qt.Assert(t, qt.StringContains(stdout, ".sql"))
	qt.Assert(t, qt.StringContains(stdout, "(build-tag gated)"))

	version, _, code := runApp(t, "-C", cwd, "--version")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(version, "optional grammars: sql"))
}

// TestRun_LanguagesPorcelainMarksSQLGatedWhenTagged pins SQL's build-specific
// fields: with the grammar compiled in, its record reads "1" and "ts-only".
func TestRun_LanguagesPorcelainMarksSQLGatedWhenTagged(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	stdout, _, code := runApp(t, "-C", cwd, "languages", "--porcelain")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "sql\t.sql\t1\tts-only\n"))
}

// TestRun_DoctorListsSQLWhenTagged is doctor's own agreement with the two
// above: its "Grammars compiled in" section reuses the same
// resolve.Languages() data, so it must never disagree with `rgit languages`.
func TestRun_DoctorListsSQLWhenTagged(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	stdout, _, code := runApp(t, "-C", cwd, "doctor")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "sql"))
}

// TestRun_SQLAnchorWithTagResolvesNormally pins the positive case: with the
// grammar actually compiled in, an unresolved .sql anchor fails as an
// ordinary AnchorUnresolvable (exit 3) -- never the gated-miss exit 9 the
// untagged build produces for the identical anchor
// (languages_nosql_test.go) -- and carries no rebuild hint, since there is
// nothing left to rebuild for.
func TestRun_SQLAnchorWithTagResolvesNormally(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	writeAppFile(t, dir, "q.sql", "CREATE TABLE users (id INT);\n")
	gittest.Commit(t.Context(), t, dir, "chore: add sql fixture")
	writeAppFile(t, dir, "q.sql", "CREATE TABLE users (id INT);\nCREATE TABLE accounts (id INT);\n")

	_, stderr, code := runApp(t, "-C", dir, "commit", "-m", "feat(x): y", "q.sql:NoSuchSymbol")

	qt.Assert(t, qt.Equals(code, exitcode.AnchorUnresolvable))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "rgit_sql")))
}
