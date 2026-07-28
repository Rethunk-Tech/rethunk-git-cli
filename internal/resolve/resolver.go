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

	// Container is the enclosing symbol's bare name when this Resolution
	// names a struct field, interface method, receiver method, or class/
	// namespace member -- empty for a top-level declaration and for every
	// pseudo-anchor. internal/synth's insertion path uses it to decide
	// whether a newly spliced-in symbol gets top-level blank-line padding
	// or sits flush against its siblings, the way container members already
	// sit in the worktree (TODO.md § Known limitations).
	Container string
}

// File is one source parsed once and held open, so a caller with several
// anchors against the same buffer pays for the parse and the anchor index a
// single time. Resolve and DeclOrder below are the one-shot forms, kept for
// callers that genuinely have one question to ask.
//
// The saving is not marginal: attributing a diff resolves every declaration
// in a file, and blob synthesis walks a file's declarations looking for the
// nearest one HEAD also has. Both were O(declarations) full parses of the
// same bytes, and class members multiplied the declaration count.
type File struct {
	lang Language
	src  []byte
	tree *ts.Tree
	idx  *index
}

// Open parses src and builds its anchor index. The caller must Close it:
// tree-sitter trees hold C-side memory the garbage collector cannot see.
func Open(lang Language, src []byte) (*File, error) {
	tree, err := parseSource(lang, src)
	if err != nil {
		return nil, err
	}
	return &File{lang: lang, src: src, tree: tree, idx: buildIndex(lang, src, tree.RootNode())}, nil
}

// Close releases the parse tree. A Resolution carries byte offsets and
// strings only, never a node, so results already handed out stay valid.
func (f *File) Close() {
	if f == nil || f.tree == nil {
		return
	}
	f.tree.Close()
	f.tree = nil
}

// Resolve answers one anchor against the already-parsed source.
func (f *File) Resolve(anchor string) (*Resolution, error) {
	if isPseudoAnchor(anchor) {
		return resolvePseudo(f.lang, f.src, f.tree.RootNode(), f.idx, anchor)
	}
	sym, err := f.idx.resolve(anchor)
	if err != nil {
		return nil, err
	}
	return &Resolution{Extent: sym.Full, DeclOnly: sym.DeclOnly, Anchor: sym.Qualified, Container: sym.Decl.Container}, nil
}

// DeclOrder returns the anchor rgit emits for each declaration, in source
// order — the same strings Resolve accepts back.
func (f *File) DeclOrder() []string {
	out := make([]string, len(f.idx.order))
	for i, s := range f.idx.order {
		out[i] = s.Qualified
	}
	return out
}

// Resolve maps anchor — a bare or qualified symbol name, an ordinal form
// like "init#2", a gopls-spelled receiver like "(*A).Get", or a pseudo-
// anchor ("@header", "@imports", "@toplevel") — to the byte extent that
// names it in src. lang must be the adapter registered for src's language.
func Resolve(lang Language, src []byte, anchor string) (*Resolution, error) {
	f, err := Open(lang, src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Resolve(anchor)
}

// DeclOrder returns the anchor rgit emits for each top-level declaration in
// src, in source order — the same strings Resolve accepts back.
//
// Blob synthesis needs the whole ordered table, not one named anchor: placing
// a new symbol means walking the worktree's declarations outward from it to
// find the nearest sibling that also exists in HEAD. Exported here rather
// than recomputed by the caller so the qualification rules — container
// prefix, bare name, ordinal fallback — have exactly one implementation and
// cannot drift into disagreeing about what an anchor is called.
func DeclOrder(lang Language, src []byte) ([]string, error) {
	f, err := Open(lang, src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.DeclOrder(), nil
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

func resolvePseudo(lang Language, src []byte, root *ts.Node, idx *index, anchor string) (*Resolution, error) {
	var (
		ext Extent
		ok  bool
	)
	switch anchor {
	case "@header":
		// Bounded by where @imports or @toplevel starts, so @header cannot
		// claim leading comments that belong to imports or top-level declarations.
		limit := ^uint(0)
		if tl, found := toplevelExtent(lang, src, root, idx); found {
			limit = tl.Start
		}
		if imp, found := importsExtent(lang, root); found && imp.Start < limit {
			limit = imp.Start
		}
		ext, ok = headerExtent(lang, root, limit)
	case "@imports":
		ext, ok = importsExtent(lang, root)
	case "@toplevel":
		ext, ok = toplevelExtent(lang, src, root, idx)
	}
	if !ok {
		return nil, &ResolveError{Code: exitcode.AnchorUnresolvable, Anchor: anchor}
	}
	return &Resolution{Extent: ext, Anchor: anchor, Pseudo: true}, nil
}
