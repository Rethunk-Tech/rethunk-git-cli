package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// TestResolve_TOML covers the grammar's own shapes in one pass: a realistic
// config file for tables/comments/array-of-tables ambiguity, then edge
// shapes it never exercises on its own (the key spellings tomlKeyName does
// and does not turn into a Bare name, and a dotted table header qualifying
// its members).
func TestResolve_TOML(t *testing.T) {
	t.Parallel()
	// A realistic config file: a leading comment, a bare top-level pair, a
	// "[table]" whose members include one separated from its neighbour by
	// an own-line comment, and an array of tables ("[[servers]]") whose two
	// elements share one header spelling.
	src := []byte(`# leading comment

title = "example"

[server]
port = 8080

# comment for host
host = "localhost"

[[servers]]
name = "a"

[[servers]]
name = "b"
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "@header"), "# leading comment"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "title"), `title = "example"`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "server.port"), "port = 8080"))

	// The comment sitting directly above "host" with no blank line between
	// them attaches to it -- the shared blank-line rule (docs/ANCHORS.md),
	// unmodified for TOML.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "server.host"),
		"# comment for host\nhost = \"localhost\""))

	// Naming a table claims the whole table, header through its last
	// member -- including the blank line before the next section header,
	// which the grammar attributes to the table node itself: "table"'s own
	// EndByte reaches the byte immediately before "[[servers]]" starts, not
	// the end of "host"'s own line.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "server"),
		"[server]\nport = 8080\n\n# comment for host\nhost = \"localhost\"\n\n"))

	// Two "[[servers]]" elements share one header spelling and collide the
	// same way two same-named Go functions do: ambiguous (exit 4), not a
	// silent pick of one, both for the table's own anchor and its member's.
	lang, ok := resolve.ForExtension(".toml")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "servers")
	var ambigErr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &ambigErr))
	qt.Assert(t, qt.Equals(ambigErr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(ambigErr.Candidates, []string{"servers#1", "servers#2"}))

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "servers#1"), "[[servers]]\nname = \"a\"\n\n"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "servers.name#1"), `name = "a"`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", src, "servers.name#2"), `name = "b"`))

	// TOML has no include/import directive of any kind.
	_, err = resolve.Resolve(lang, src, "@imports")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))

	// .toml is claimed.
	_, ok = resolve.ForExtension(".toml")
	qt.Assert(t, qt.IsTrue(ok))

	// The key spellings tomlKeyName does and does not turn into a Bare
	// name, isolated from the realistic fixture above: a quoted key, a
	// dotted pair key left undecomposed, an inline table left undescended,
	// and an array left undescended.
	keyShapes := []byte(`"quoted key" = 1
inline = { a = 1, b = 2 }
arr = [1, 2, 3]
dotted.pair = 1
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", keyShapes, "quoted key"), `"quoted key" = 1`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", keyShapes, "inline"), "inline = { a = 1, b = 2 }"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", keyShapes, "arr"), "arr = [1, 2, 3]"))

	// A dotted pair key is not decomposed into container.bare -- its Bare is
	// the full dotted spelling, one predictable rule rather than a second
	// qualification scheme layered on top of the "[table]"-header one.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", keyShapes, "dotted.pair"), "dotted.pair = 1"))

	_, err = resolve.Resolve(lang, keyShapes, "inline.a")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// docs/ANCHORS.md's claim that a dotted table header ("[server.tls]")
	// qualifies its members as "server.tls.<key>", not a further-nested
	// "server.tls.tls.<key>" path -- the fixtures above only exercise a
	// plain "[server]" header and a root-level dotted pair
	// ("dotted.pair"), so a header's own dotted spelling qualifying its
	// members was a documented guarantee with no test actually driving it.
	dottedHeader := []byte(`[server.tls]
cert = "a.pem"
`)

	// The header's own dotted spelling is the container verbatim -- not
	// decomposed into "server" containing a nested "tls".
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", dottedHeader, "server.tls"), "[server.tls]\ncert = \"a.pem\"\n"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", dottedHeader, "server.tls.cert"), `cert = "a.pem"`))
	// Unambiguous on its own, the bare member name resolves too.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".toml", dottedHeader, "cert"), `cert = "a.pem"`))

	_, err = resolve.Resolve(lang, dottedHeader, "server.tls.tls.cert")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))
}
