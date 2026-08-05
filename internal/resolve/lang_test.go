package resolve

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPeekShebangLine covers the guarantee: a successful os.Open must not
// be enough on its own to report ok=true -- a non-EOF read error or a
// zero-byte read both mean there is no line to report, the same as a
// failed Open.
func TestPeekShebangLine(t *testing.T) {
	t.Parallel()

	t.Run("ordinary shebang line", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "script.sh")
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		line, ok := PeekShebangLine(path)
		if !ok {
			t.Fatal("PeekShebangLine() ok = false; want true")
		}
		if string(line) != "#!/bin/sh\n" {
			t.Errorf("line = %q; want %q", line, "#!/bin/sh\n")
		}
	})

	// No newline within shebangPeekBytes (or the whole file, for one
	// shorter than that): ReadString reports io.EOF, which must still be
	// treated as a legitimate partial read, not an error.
	t.Run("no newline in the peeked window", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "noeol")
		if err := os.WriteFile(path, []byte("#!/bin/sh"), 0o644); err != nil {
			t.Fatal(err)
		}
		line, ok := PeekShebangLine(path)
		if !ok {
			t.Fatal("PeekShebangLine() ok = false; want true for a short, newline-less file")
		}
		if string(line) != "#!/bin/sh" {
			t.Errorf("line = %q; want %q", line, "#!/bin/sh")
		}
	})

	// Before PeekShebangLine's own fix, a successful os.Open on a zero-byte
	// file still reported ok=true with an empty line -- indistinguishable
	// from a genuine (if shebang-less) first line to a caller that only
	// checks the bool.
	t.Run("empty file reports ok=false", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "empty")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := PeekShebangLine(path); ok {
			t.Error("PeekShebangLine() ok = true for an empty file; want false")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		if _, ok := PeekShebangLine(filepath.Join(t.TempDir(), "does-not-exist")); ok {
			t.Error("PeekShebangLine() ok = true for a missing file; want false")
		}
	})

	// A directory's os.Open succeeds; its Read does not (EISDIR). Before
	// PeekShebangLine's own fix, that non-EOF read error was silently
	// discarded and this still reported ok=true.
	t.Run("directory reports ok=false", func(t *testing.T) {
		t.Parallel()
		if _, ok := PeekShebangLine(t.TempDir()); ok {
			t.Error("PeekShebangLine() ok = true for a directory; want false")
		}
	})
}

// TestStripQuotes covers the helper lang_yaml.go's yamlKeyName and
// lang_toml.go's tomlKeyName both delegate to (nit 2) instead of each
// carrying their own copy of the same one-byte-off-each-end trim.
func TestStripQuotes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    string
		wantOK  bool
		comment string
	}{
		{name: "ordinary double-quoted", in: `"abc"`, want: "abc", wantOK: true},
		{name: "ordinary single-quoted", in: `'abc'`, want: "abc", wantOK: true},
		{name: "empty quoted string", in: `""`, want: "", wantOK: true},
		{name: "single character: too short to have both quotes", in: `"`, wantOK: false},
		{name: "empty string", in: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := stripQuotes(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("stripQuotes(%q) ok = %v; want %v", tt.in, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("stripQuotes(%q) = %q; want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestParseOrdinal covers ParseOrdinal's merged parsing rule: docs/ANCHORS.md's
// positional "Bare#N" form requires a non-empty bare name and a strictly
// positive N, unifying what crosscheck.go's old splitOrdinal (neither
// check) and internal/synth/stage.go's isOrdinalAnchor (both checks) used
// to enforce differently.
func TestParseOrdinal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		anchor   string
		wantBare string
		wantN    int
		wantOK   bool
	}{
		{anchor: "init#2", wantBare: "init", wantN: 2, wantOK: true},
		{anchor: "init", wantOK: false},
		{anchor: "init#", wantOK: false},
		{anchor: "init#abc", wantOK: false},
		{anchor: "init#0", wantOK: false},
		{anchor: "init#-1", wantOK: false},
		{anchor: "#2", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.anchor, func(t *testing.T) {
			t.Parallel()
			bare, n, ok := ParseOrdinal(tt.anchor)
			if ok != tt.wantOK {
				t.Fatalf("ParseOrdinal(%q) ok = %v; want %v", tt.anchor, ok, tt.wantOK)
			}
			if ok {
				if bare != tt.wantBare || n != tt.wantN {
					t.Errorf("ParseOrdinal(%q) = (%q, %d); want (%q, %d)", tt.anchor, bare, n, tt.wantBare, tt.wantN)
				}
			}
		})
	}
}

// TestTSFamily_CachesLanguage covers the guarantee: TSLanguage() must
// return the same *ts.Language on every call rather than invoking
// ts.NewLanguage again -- go-tree-sitter's own NewLanguage allocates a
// fresh *Language struct on every call even though the underlying grammar
// table is the same static data, so two calls returning identical pointers
// is only true once the value is actually cached rather than recomputed.
func TestTSFamily_CachesLanguage(t *testing.T) {
	t.Parallel()

	tsLang := newTypeScriptLanguage()
	a := tsLang.TSLanguage()
	b := tsLang.TSLanguage()
	if a != b {
		t.Error("TypeScript TSLanguage() returned a different pointer on a second call; want the cached one reused")
	}

	tsxLang := newTSXLanguage()
	c := tsxLang.TSLanguage()
	d := tsxLang.TSLanguage()
	if c != d {
		t.Error("TSX TSLanguage() returned a different pointer on a second call; want the cached one reused")
	}

	if a == c {
		t.Error("TypeScript and TSX must not share the same *ts.Language -- they are different grammars")
	}
}

// TestShebangInterpreter_NodeJSEcosystem pins docs/ANCHORS.md's shebang
// sniffing for the Node/TypeScript ecosystem: node/nodejs/tsx/ts-node/bun,
// the "env -S NAME ..." unwrap, and the npx/bunx package-runner unwrap to
// the tool they actually run. An interpreter this resolver has no adapter
// for at all stays honestly unmapped rather than guessed at.
func TestShebangInterpreter_NodeJSEcosystem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
		want string
		ok   bool
	}{
		{"direct node", "#!/usr/bin/env node\n", "node", true},
		{"nodejs binary name", "#!/usr/bin/env nodejs\n", "nodejs", true},
		{"env -S with a flag of its own", "#!/usr/bin/env -S node --import tsx\n", "node", true},
		{"npx unwraps to its target", "#!/usr/bin/env npx tsx\n", "tsx", true},
		{"bunx unwraps to its target", "#!/usr/bin/env bunx ts-node\n", "ts-node", true},
		{"bun run stays bun, trailing subcommand ignored", "#!/usr/bin/env bun run\n", "bun", true},
		{"bare npx with nothing after it stays unmapped by shebangExtension, but shebangInterpreter still reports it", "#!/usr/bin/env npx\n", "npx", true},
		{"unrecognized interpreter still reports its name", "#!/usr/bin/perl\n", "perl", true},
		{"not a shebang at all", "# just a comment\n", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := shebangInterpreter([]byte(tt.line))
			if ok != tt.ok || got != tt.want {
				t.Errorf("shebangInterpreter(%q) = (%q, %v); want (%q, %v)", tt.line, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestForPath_NodeJSEcosystemRoutesToTypeScript pins the acceptance
// criteria's own fixtures end-to-end through ForPath: each extensionless
// shebang resolves via the TypeScript adapter, an unmapped interpreter
// (perl) still refuses, and zsh -- excluded on purpose, tree-sitter-bash
// mis-parses it -- stays unmapped rather than silently routed to the shell
// adapter.
func TestForPath_NodeJSEcosystemRoutesToTypeScript(t *testing.T) {
	t.Parallel()

	typescript, ok := ForExtension(".ts")
	if !ok {
		t.Fatal("ForExtension(.ts) not registered")
	}

	fixtures := []string{
		"#!/usr/bin/env node\nconsole.log(1)\n",
		"#!/usr/bin/env -S node --import tsx\nconsole.log(1)\n",
		"#!/usr/bin/env npx tsx\nconsole.log(1)\n",
		"#!/usr/bin/env bun run\nconsole.log(1)\n",
	}
	for _, content := range fixtures {
		lang, ok := ForPath("script", []byte(content))
		if !ok || lang.Name() != typescript.Name() {
			t.Errorf("ForPath(%q) = (%v, %v); want the TypeScript adapter", content, lang, ok)
		}
	}

	if _, ok := ForPath("script", []byte("#!/usr/bin/perl\nprint 1;\n")); ok {
		t.Error(`ForPath with a perl shebang resolved; want exit-9 unmapped`)
	}
	if _, ok := ForPath("script", []byte("#!/usr/bin/env zsh\necho hi\n")); ok {
		t.Error("ForPath with a zsh shebang resolved; zsh stays excluded (tree-sitter-bash mis-parse)")
	}
}
