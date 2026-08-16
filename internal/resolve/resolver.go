package resolve

import (
	"fmt"
	"sync"

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
	// sit in the worktree (specs/design.md § Blob synthesis).
	Container string

	// Sep is the join between Container and the bare name in Anchor --
	// carried over from Declaration.Sep so the LSP cross-check (crosscheck.go's
	// qualifyLSPSymbol) can qualify a server-reported symbol the same way
	// Anchor was already qualified (containerQualified, index.go), rather
	// than assuming every language joins with ".". Empty means the default
	// ".", the same zero-value convention Declaration.Sep itself uses.
	Sep string

	// Flat mirrors the resolving language's own FlatContainerLanguage
	// answer (lang.go): true means Container is not a real ancestor, so a
	// server-reported symbol's own containerName -- a genuine parent, for
	// HTML the enclosing element -- must not be joined onto its name before
	// comparing against Anchor the way qualifyLSPSymbol does for every other
	// language. HTML already spells its own document-symbol Name exactly
	// "tag#id", identical to Anchor, with no join needed at all; joining a
	// real ancestor's name on top of that would produce a string HTML's own
	// flat anchor space can never contain (crosscheck.go's matchLSPSymbol).
	Flat bool
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
	flat := false
	if fc, ok := f.lang.(FlatContainerLanguage); ok {
		flat = fc.FlatContainer()
	}
	return &Resolution{Extent: sym.Full, DeclOnly: sym.DeclOnly, Anchor: sym.Qualified, Container: sym.Decl.Container, Sep: sym.Decl.Sep, Flat: flat}, nil
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

// Declared is one declaration's anchor paired with the extent that names it.
type Declared struct {
	// Anchor is the string Resolve accepts back, exactly as DeclOrder emits it.
	Anchor string

	// Extent is the same byte extent Resolve returns for that anchor.
	Extent Extent
}

// DeclExtents returns each declaration's anchor and extent, in source order —
// DeclOrder's own table, keeping the extent DeclOrder drops. A caller that
// wants the lines a symbol occupies needs both halves, and reading them off
// this one index is what stops the anchor and the extent from being resolved
// by two paths that could drift into disagreeing.
func (f *File) DeclExtents() []Declared {
	out := make([]Declared, len(f.idx.order))
	for i, s := range f.idx.order {
		out[i] = Declared{Anchor: s.Qualified, Extent: s.Full}
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

// DeclExtents returns each top-level declaration's anchor and extent in src,
// in source order — DeclOrder's table with the extent kept. Exported for the
// same reason DeclOrder is: the qualification rules, and now the extent each
// anchor names, have exactly one implementation to disagree with.
func DeclExtents(lang Language, src []byte) ([]Declared, error) {
	f, err := Open(lang, src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.DeclExtents(), nil
}

// parserCache holds one *ts.Parser per distinct *ts.Language, reused across
// every resolve.Open call rather than constructing and discarding one per
// parse. m5's own fix (internal/diff's attributeSymbolsOpen) already cut how
// many times a file gets opened; this cuts what each Open itself costs --
// ts.NewParser() is real construction work (allocating the parser's C-side
// state), not a cheap wrapper, and buildFileReport/attributeSymbolsOpen and
// internal/synth's openFilePlan between them can call Open several times
// per invocation. Parsers are never closed: like the registered-language
// map (lang.go's registered), they live for the process's lifetime, and
// rgit is a short-lived CLI invocation with nothing else to reclaim them
// for.
//
// Keyed by *ts.Language rather than by name: TSLanguage() already returns
// the one compiled grammar handle each adapter caches for itself
// (lang_go.go's own goLanguage.lang is the pattern every adapter follows),
// so pointer identity is exactly "the same grammar", with no risk of two
// distinct languages colliding on a reused string key the way "go" could
// coincidentally collide with a future adapter's own Name().
var (
	parserCacheMu sync.Mutex
	parserCache   = map[*ts.Language]*ts.Parser{}
)

// parseSource parses src with lang's cached parser. The lock is held for
// the whole parse, not just the cache lookup: a *ts.Parser is not
// documented safe for concurrent use, and this package's own test suite
// (CONTRIBUTING.md's t.Parallel() rule) calls Open from many goroutines at
// once even though no production call path in this codebase parses
// concurrently -- the lock is what makes a shared cache correct under both,
// at a cost this package's own sequential production usage never pays a
// contended wait for.
//
// oldTree is always nil (no caller here does incremental reparsing), so
// reusing a parser across unrelated src buffers is safe without an
// intervening Parser.Reset(): Parse(text, nil) always parses text from
// scratch, regardless of what the same parser instance parsed before.
func parseSource(lang Language, src []byte) (*ts.Tree, error) {
	parserCacheMu.Lock()
	defer parserCacheMu.Unlock()

	parser, err := cachedParserLocked(lang)
	if err != nil {
		return nil, err
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, fmt.Errorf("resolve: %s: parse produced no tree", lang.Name())
	}
	return tree, nil
}

// cachedParserLocked returns lang's cached parser, creating and caching one
// on first use. Callers must hold parserCacheMu.
func cachedParserLocked(lang Language) (*ts.Parser, error) {
	tsLang := lang.TSLanguage()
	if p, ok := parserCache[tsLang]; ok {
		return p, nil
	}
	p := ts.NewParser()
	if err := p.SetLanguage(tsLang); err != nil {
		p.Close()
		return nil, fmt.Errorf("resolve: set language %s: %w", lang.Name(), err)
	}
	parserCache[tsLang] = p
	return p, nil
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
		// claim leading comments belonging to imports or to a top-level
		// declaration.
		limit := ^uint(0)
		if tl, found := toplevelExtent(lang, src, root, idx); found {
			limit = tl.Start
		}
		if imp, found := importsExtent(lang, src, root); found && imp.Start < limit {
			limit = imp.Start
		}
		ext, ok = headerExtent(lang, root, limit)
	case "@imports":
		ext, ok = importsExtent(lang, src, root)
	case "@toplevel":
		ext, ok = toplevelExtent(lang, src, root, idx)
	}
	if !ok {
		return nil, &ResolveError{Code: exitcode.AnchorUnresolvable, Anchor: anchor}
	}
	return &Resolution{Extent: ext, Anchor: anchor, Pseudo: true}, nil
}
