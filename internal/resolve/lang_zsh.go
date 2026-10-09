package resolve

import ts "github.com/tree-sitter/go-tree-sitter"

// zshLanguage resolves zsh through its own grammar (tree-sitter-zsh), because
// tree-sitter-bash mis-parses zsh-only syntax such as `repeat`, glob
// qualifiers and nested parameter-expansion flags. The zsh grammar names
// functions, variable assignments, comments and `source` commands exactly as
// the Bash grammar does, so shellLanguage's declaration, import and header
// rules apply unchanged; only the grammar and the claimed extensions differ.
type zshLanguage struct {
	shellLanguage
}

func newZshLanguage() *zshLanguage {
	return &zshLanguage{shellLanguage{lang: zshGrammar()}}
}

func (z *zshLanguage) Name() string { return "zsh" }

// Extensions includes zsh's startup dotfiles, whose whole name is the
// extension to filepath.Ext.
func (z *zshLanguage) Extensions() []string {
	return []string{".zsh", ".zshrc", ".zshenv", ".zprofile"}
}

func (z *zshLanguage) TSLanguage() *ts.Language { return z.lang }
