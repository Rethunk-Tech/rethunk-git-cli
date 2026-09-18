package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

func TestResolve_Shell(t *testing.T) {
	t.Parallel()
	// Both function forms, a top-level var, source lines in two spellings, a
	// heredoc whose body only looks like a function definition, and a
	// redefinition.
	src := []byte(`#!/usr/bin/env bash
source ./lib.sh
. ./other.sh

TOP_VAR=1

foo() {
  echo "foo"
}

function bar {
  echo "bar"
}

cat <<'EOF2'
function fake_in_heredoc() {
  echo "not real"
}
EOF2

foo() {
  echo "redefined foo"
}
`)

	// A shebang parses as an ordinary comment (no dedicated node), so
	// @header inherits Python's semantics with nothing new to add.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sh", src, "@header"), "#!/usr/bin/env bash"))

	// @imports spans both source forms and nothing either side of them --
	// not TOP_VAR ahead, and not the heredoc's look-alike function behind.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sh", src, "@imports"),
		"source ./lib.sh\n. ./other.sh"))

	// Both surface forms of a function definition are addressable by bare
	// name, and the flat namespace disambiguates a redefinition with the
	// same #N ordinal every other language uses -- no shell-specific
	// disambiguation of its own.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sh", src, "foo#1"), "foo() {\n  echo \"foo\"\n}"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sh", src, "foo#2"), "foo() {\n  echo \"redefined foo\"\n}"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sh", src, "bar"), "function bar {\n  echo \"bar\"\n}"))

	lang, ok := resolve.ForExtension(".sh")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "foo")
	var ambigErr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &ambigErr))
	qt.Assert(t, qt.Equals(ambigErr.Code, exitcode.AnchorAmbiguous))

	// A bare top-level assignment is addressable.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".sh", src, "TOP_VAR"), "TOP_VAR=1"))

	// The heredoc body is a real node the parser never mistakes for a
	// sibling declaration: "fake_in_heredoc" names nothing.
	_, err = resolve.Resolve(lang, src, "fake_in_heredoc")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// .bash is claimed too; .zsh deliberately is not (lang_shell.go) --
	// tree-sitter-bash mis-parses zsh-only syntax.
	_, ok = resolve.ForExtension(".bash")
	qt.Assert(t, qt.IsTrue(ok))
	_, ok = resolve.ForExtension(".zsh")
	qt.Assert(t, qt.IsFalse(ok))
}
