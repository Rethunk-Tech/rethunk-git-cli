package resolve

import (
	"fmt"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

// Resolution is what a successful Resolve returns: the byte extent to
// stage, plus — for a real symbol, not a pseudo-anchor — the declaration-
// only extent a later LSP cross-check compares against. Pseudo-anchors
// leave DeclOnly zero; they are exempt from that cross-check because
// language servers do not report import blocks as document symbols
// (docs/ANCHORS.md).
type Resolution struct {
	Extent   Extent
	DeclOnly Extent
	Anchor   string
	Pseudo   bool
}

// Resolve maps anchor — a bare or qualified symbol name, an ordinal form
// like "init#2", a gopls-spelled receiver like "(*A).Get", or a pseudo-
// anchor ("@header", "@imports", "@toplevel") — to the byte extent that
// names it in src. lang must be the adapter registered for src's language.
func Resolve(lang Language, src []byte, anchor string) (*Resolution, error) {
	tree, err := parseSource(lang, src)
	if err != nil {
		return nil, err
	}
	root := tree.RootNode()

	if isPseudoAnchor(anchor) {
		return resolvePseudo(lang, src, root, anchor)
	}

	idx := buildIndex(lang, src, root)
	sym, err := idx.resolve(anchor)
	if err != nil {
		return nil, err
	}
	return &Resolution{Extent: sym.Full, DeclOnly: sym.DeclOnly, Anchor: sym.Qualified}, nil
}

func parseSource(lang Language, src []byte) (*ts.Tree, error) {
	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang.TSLanguage()); err != nil {
		return nil, fmt.Errorf("resolve: set language %s: %w", lang.Name(), err)
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, fmt.Errorf("resolve: %s: parse produced no tree", lang.Name())
	}
	return tree, nil
}

func isPseudoAnchor(anchor string) bool {
	switch anchor {
	case "@header", "@imports", "@toplevel":
		return true
	}
	return false
}

func resolvePseudo(lang Language, src []byte, root *ts.Node, anchor string) (*Resolution, error) {
	var (
		ext Extent
		ok  bool
	)
	switch anchor {
	case "@header":
		ext, ok = headerExtent(lang, root, buildIndex(lang, src, root))
	case "@imports":
		ext, ok = importsExtent(lang, root)
	case "@toplevel":
		ext, ok = toplevelExtent(buildIndex(lang, src, root))
	}
	if !ok {
		return nil, &ResolveError{Code: exitcode.AnchorUnresolvable, Anchor: anchor}
	}
	return &Resolution{Extent: ext, Anchor: anchor, Pseudo: true}, nil
}
