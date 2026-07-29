//go:build rgit_sql

// SQL resolver coverage. Guarded behind the rgit_sql tag, the same as the
// grammar's own generated C (internal/resolve/sqlgrammar): this suite can
// only run after `tree-sitter generate` has produced csrc/, so it has no
// business in the default `go test ./...`/`-short` lane a clean checkout
// runs (CONTRIBUTING.md § Tests).
package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

func TestResolve_SQL(t *testing.T) {
	t.Parallel()

	src := []byte(`-- Doc for users.
CREATE TABLE users (
	id INT PRIMARY KEY,
	name TEXT NOT NULL
);

CREATE VIEW active_users AS SELECT id FROM users WHERE name = 'active';

CREATE FUNCTION touch_updated_at() RETURNS INT AS $$
BEGIN
	RETURN 1;
END;
$$ LANGUAGE plpgsql;

CREATE INDEX idx_users_name ON users (name);

CREATE TRIGGER trg_users_updated BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE TYPE mood AS ENUM ('sad', 'ok', 'happy');
`)

	// The doc comment attaches with no blank line before it -- the same
	// blank-line rule every other language uses, exercised here through
	// the core resolver's docStart, not adapter-specific code.
	users := mustResolveExt(t, ".sql", src, "users")
	qt.Assert(t, qt.Equals(users,
		"-- Doc for users.\nCREATE TABLE users (\n\tid INT PRIMARY KEY,\n\tname TEXT NOT NULL\n)"))

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sql", src, "active_users"),
		"CREATE VIEW active_users AS SELECT id FROM users WHERE name = 'active'"))

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sql", src, "touch_updated_at"),
		"CREATE FUNCTION touch_updated_at() RETURNS INT AS $$\nBEGIN\n\tRETURN 1;\nEND;\n$$ LANGUAGE plpgsql"))

	// A CREATE INDEX statement is named by create_index's own "column"
	// field, which -- despite the name -- holds the index's own identifier,
	// not one of the columns it indexes (lang_sql.go documents the same
	// field shape).
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sql", src, "idx_users_name"),
		"CREATE INDEX idx_users_name ON users (name)"))

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sql", src, "trg_users_updated"),
		"CREATE TRIGGER trg_users_updated BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION touch_updated_at()"))

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sql", src, "mood"),
		"CREATE TYPE mood AS ENUM ('sad', 'ok', 'happy')"))

	// @toplevel spans every declaration, first through last; @header and
	// @imports both degrade to nothing here -- no shebang/package-doc
	// leader precedes the first declaration, and this grammar has no
	// include/import statement at all.
	lang, ok := resolve.ForExtension(".sql")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "@header")
	qt.Assert(t, qt.IsNotNil(err))
	_, err = resolve.Resolve(lang, src, "@imports")
	qt.Assert(t, qt.IsNotNil(err))

	toplevel := mustResolveExt(t, ".sql", src, "@toplevel")
	qt.Assert(t, qt.StringContains(toplevel, "CREATE TABLE users"))
	qt.Assert(t, qt.StringContains(toplevel, "CREATE TYPE mood"))

	// DROP/ALTER/INSERT/SELECT declare no persistent named object and are
	// deliberately unaddressable.
	unaddressable := []byte("DROP TABLE users;\n")
	_, err = resolve.Resolve(lang, unaddressable, "users")
	qt.Assert(t, qt.IsNotNil(err))
	var rerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &rerr))
	qt.Assert(t, qt.Equals(rerr.Code, exitcode.AnchorUnresolvable))
}

func TestResolve_SQLHeaderAndBlockComment(t *testing.T) {
	t.Parallel()
	// A block comment ("/* ... */") parses as "marginalia", a distinct node
	// kind from a line comment's "comment" -- IsComment must accept both, or
	// @header would silently claim nothing for a file that leads with one.
	src := []byte(`/* Copyright Example Corp. */

CREATE TABLE a (id INT);
`)
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sql", src, "@header"), "/* Copyright Example Corp. */"))
}

func TestResolve_SQLSchemaQualification(t *testing.T) {
	t.Parallel()
	// A schema-qualified name carries the schema as object_reference's own
	// "schema" field; it is read as Container, the same one-level
	// qualification a Go receiver type already gives, so "s.t" both
	// addresses the schema-qualified table and disambiguates it from
	// another table named "t" in a different schema in the same file.
	src := []byte(`CREATE TABLE s1.t (id INT);

CREATE TABLE s2.t (id INT);
`)
	lang, ok := resolve.ForExtension(".sql")
	qt.Assert(t, qt.IsTrue(ok))

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sql", src, "s1.t"), "CREATE TABLE s1.t (id INT)"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sql", src, "s2.t"), "CREATE TABLE s2.t (id INT)"))

	// The bare name is ambiguous, not absent -- the same remediation every
	// other container-qualified grammar in this resolver gives.
	_, err := resolve.Resolve(lang, src, "t")
	var rerr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &rerr))
	qt.Assert(t, qt.Equals(rerr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(rerr.Candidates, []string{"s1.t", "s2.t"}))
}

func TestResolve_SQLAnonymousIndexUnaddressable(t *testing.T) {
	t.Parallel()
	// "CREATE INDEX ON t (c)" is legal SQL -- Postgres synthesizes a name --
	// but create_index has no "column" field at all when none is written,
	// and this adapter does not guess at the name Postgres would assign.
	src := []byte("CREATE INDEX ON t (c);\n")
	lang, ok := resolve.ForExtension(".sql")
	qt.Assert(t, qt.IsTrue(ok))
	order, err := resolve.DeclOrder(lang, src)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.HasLen(order, 0))
}

func TestResolve_SQLExtensionsClaimsOnlySQL(t *testing.T) {
	t.Parallel()
	_, ok := resolve.ForExtension(".sql")
	qt.Assert(t, qt.IsTrue(ok))
	// .psql/.pgsql/.ddl are all real conventions in the wild, but this
	// adapter claims ".sql" alone.
	for _, ext := range []string{".psql", ".pgsql", ".ddl"} {
		_, ok := resolve.ForExtension(ext)
		qt.Assert(t, qt.IsFalse(ok))
	}
}
