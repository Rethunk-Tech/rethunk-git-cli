package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

func TestResolve_UnsupportedLanguage(t *testing.T) {
	t.Parallel()
	// A symbol anchor on a file whose language has no grammar is the
	// caller's exit 9 (docs/ANCHORS.md § Language support); the resolver's
	// contribution is just reporting the extension unclaimed.
	_, ok := resolve.ForExtension(".rb")
	qt.Assert(t, qt.IsFalse(ok))

	// internal/diff/run.go's validateSym constructs exactly this shape --
	// Code set, no Candidates -- for the identical failure reached through
	// `rgit diff --sym`. Error() must actually say what docs/CODES.md's
	// exit-9 row promises ("Unsupported / deferred language for a symbol
	// anchor"), not silently fall through to the same "unresolved" label
	// exit 3 (AnchorUnresolvable) uses -- the code is right, the message
	// must not contradict it.
	err := &resolve.ResolveError{Code: exitcode.UnsupportedLanguage, Anchor: "main"}
	qt.Assert(t, qt.Equals(err.Error(), `resolve: "main": unsupported language`))
}

func TestResolve_ForPathShebangFallback(t *testing.T) {
	t.Parallel()
	// A recognized extension is authoritative and never even looks at
	// content: passing shebang-shaped bytes that would map to a different
	// language must not steer a ".go" file anywhere else.
	lang, ok := resolve.ForPathFolding("main.go", []byte("#!/usr/bin/env python3\n"), false)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(lang.Name(), "go"))

	// Both forms of both shells named in the spec resolve to "shell", for an
	// extensionless path.
	for _, shebang := range []string{
		"#!/bin/bash\n", "#!/usr/bin/env bash\n",
		"#!/bin/sh\n", "#!/usr/bin/env sh\n",
	} {
		lang, ok := resolve.ForPathFolding("hooks/pre-commit", []byte(shebang+"foo() {\n  echo hi\n}\n"), false)
		qt.Assert(t, qt.IsTrue(ok), qt.Commentf("shebang %q", shebang))
		qt.Assert(t, qt.Equals(lang.Name(), "shell"))
	}

	// python3 falls out cleanly too: the same adapter Python's own
	// extension resolves to, with no special-casing for how it was reached.
	lang, ok = resolve.ForPathFolding("bin/tool", []byte("#!/usr/bin/env python3\n\ndef foo():\n    return 1\n"), false)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(lang.Name(), "python"))

	// zsh is a real interpreter, not a typo, and is deliberately refused:
	// tree-sitter-bash would mis-parse zsh-only syntax rather than honestly
	// fail. An unrecognized interpreter (perl) and a leading line that is
	// an ordinary comment, not a shebang, both refuse the same way, as does
	// an extensionless path with no content at all to sniff.
	for _, c := range []struct {
		path    string
		content string
	}{
		{"hooks/pre-commit", "#!/bin/zsh\nfoo() {}\n"},
		{"hooks/pre-commit", "#!/usr/bin/env perl\n"},
		{"hooks/pre-commit", "# just a comment, not a shebang\nfoo() {}\n"},
		{"hooks/pre-commit", ""},
	} {
		_, ok := resolve.ForPathFolding(c.path, []byte(c.content), false)
		qt.Assert(t, qt.IsFalse(ok), qt.Commentf("content %q", c.content))
	}
}
