package resolve

import (
	"slices"
	"testing"
)

const zshSample = "#!/usr/bin/env zsh\n# prompt helpers\nsource ~/.zshenv\nautoload -Uz compinit\n\nTMP=1\n\n# say hello\nfunction greet { print hi }\n\nbye() { repeat 3 print bye; }\n\nfor f in *(.N); do print $f; done\n"

func TestZsh_ParsesZshOnlySyntaxWithoutErrors(t *testing.T) {
	t.Parallel()
	lang, ok := ForExtension(".zsh")
	if !ok {
		t.Fatal(".zsh is not registered")
	}
	f, err := Open(lang, []byte(zshSample))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// `repeat` and the `*(.N)` glob qualifier are what tree-sitter-bash
	// turns into ERROR nodes; losing a declaration after them would show here.
	want := []string{"TMP", "greet", "bye"}
	if got := f.DeclOrder(); !slices.Equal(got, want) {
		t.Fatalf("DeclOrder = %v; want %v", got, want)
	}
}

func TestZsh_ExtentsAndPseudoAnchors(t *testing.T) {
	t.Parallel()
	lang, _ := ForExtension(".zsh")
	src := []byte(zshSample)

	res, err := Resolve(lang, src, "greet")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(src[res.Extent.Start:res.Extent.End]); got != "# say hello\nfunction greet { print hi }" {
		t.Errorf("greet extent = %q; want the doc comment attached", got)
	}

	res, err = Resolve(lang, src, "@imports")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(src[res.Extent.Start:res.Extent.End]); got != "source ~/.zshenv" {
		t.Errorf("@imports = %q; want only the source line", got)
	}
}

func TestZsh_StartupDotfilesAndShebang(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{".zsh", ".zshrc", ".zshenv", ".zprofile"} {
		lang, ok := ForExtension(ext)
		if !ok || lang.Name() != "zsh" {
			t.Errorf("ForExtension(%q) = (%v, %v); want zsh", ext, lang, ok)
		}
	}
	// .sh stays on the Bash grammar.
	if lang, _ := ForExtension(".sh"); lang.Name() != "shell" {
		t.Errorf(".sh resolves to %q; want shell", lang.Name())
	}
}
