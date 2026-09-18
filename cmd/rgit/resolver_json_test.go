package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

func TestResolve_JSON(t *testing.T) {
	t.Parallel()
	// A realistic config-shaped document: a nested object (container
	// qualification), an array (a leaf, never descended), and a top-level
	// scalar.
	src := []byte(`{
  "name": "example",
  "server": {
    "port": 8080,
    "host": "localhost"
  },
  "list": [1, 2, 3]
}
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".json", src, "name"), `"name": "example"`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".json", src, "server.port"), `"port": 8080`))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".json", src, "server.host"), `"host": "localhost"`))

	// Naming the container claims the whole nested object.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".json", src, "server"),
		"\"server\": {\n    \"port\": 8080,\n    \"host\": \"localhost\"\n  }"))

	// An array is a leaf -- "list" addresses the whole array, but there is
	// no "list.0" to address one element by.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".json", src, "list"), `"list": [1, 2, 3]`))

	lang, ok := resolve.ForExtension(".json")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "list.0")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// JSON has no comment syntax, so @header and @imports both resolve to
	// nothing -- the same degraded-but-not-an-error result Markdown gives a
	// file with no shebang.
	_, err = resolve.Resolve(lang, src, "@header")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	_, err = resolve.Resolve(lang, src, "@imports")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))

	// @toplevel reaches the whole document -- there is no header or import
	// material to exclude, the same as YAML once its own @header is set
	// aside.
	toplevel := mustResolveExt(t, ".json", src, "@toplevel")
	qt.Assert(t, qt.StringContains(toplevel, `"name": "example"`))
	qt.Assert(t, qt.StringContains(toplevel, `"list": [1, 2, 3]`))

	// A bare top-level array has no key to address at all.
	_, ok = resolve.ForExtension(".json")
	qt.Assert(t, qt.IsTrue(ok))
	_, err = resolve.Resolve(lang, []byte("[1, 2, 3]\n"), "name")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
}
