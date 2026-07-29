package resolve

import ts "github.com/tree-sitter/go-tree-sitter"

// namedChildren is the []ts.Node convenience form of Node.NamedChildren,
// which otherwise requires callers to manage a *ts.TreeCursor themselves.
func namedChildren(node *ts.Node) []ts.Node {
	cursor := node.Walk()
	defer cursor.Close()
	return node.NamedChildren(cursor)
}

func kindSet(kinds []string) map[string]bool {
	set := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		set[k] = true
	}
	return set
}

// headerExtent spans the leading contiguous run of HeaderKinds nodes — a Go
// file's build-tag comments, package doc, and package_clause, for example —
// starting at byte 0. Unlike a symbol's doc-comment attribution, this run is
// not broken by blank lines: Go itself requires one between a build
// constraint and the package clause, and both belong to @header regardless
// (docs/ANCHORS.md).
//
// The run stops at limit, where @toplevel begins. Comments are a header kind
// in both Go and Python, so without that bound @header would swallow the
// first declaration's doc comment and both anchors would claim the same
// bytes. The bound is @toplevel's start rather than the first declaration's,
// since a declaration can sit inside a top-level node -- a spec in a grouped
// `const (...)` block -- leaving the block's doc comment ahead of it.
func headerExtent(lang Language, root *ts.Node, limit uint) (Extent, bool) {
	kinds := kindSet(lang.HeaderKinds())
	children := namedChildren(root)

	end := uint(0)
	found := false
	for i := range children {
		c := &children[i]
		if !kinds[c.Kind()] || c.StartByte() >= limit {
			break
		}
		end = c.EndByte()
		found = true
	}
	if !found {
		return Extent{}, false
	}
	return Extent{Start: 0, End: end}, true
}

// importsExtent spans the first contiguous run of import nodes. Go emits
// exactly one import_declaration; TypeScript and Python emit one node per
// import statement, so the run can be many nodes wide — the pseudo-anchor
// must cover the whole run or staging @imports would silently drop
// everything after the first import.
//
// Comments do not break the run: in TypeScript and Python a grouping
// comment between two imports ("# stdlib", "// external") is an ordinary
// named sibling, and the block a person means by @imports spans it.
//
// What counts as "an import node" is lang's own ImportMatcher when it
// implements one, falling back to an ImportKinds lookup otherwise — see
// importPredicate.
func importsExtent(lang Language, src []byte, root *ts.Node) (Extent, bool) {
	isImport := importPredicate(lang, src)
	children := namedChildren(root)

	start, end := -1, -1
	for i := range children {
		c := &children[i]
		if isImport(c) {
			if start == -1 {
				start = i
			}
			end = i
			continue
		}
		if start == -1 {
			continue
		}
		// end advances only on an import, so an interior comment is
		// spanned while a trailing one stays outside.
		if lang.IsComment(c.Kind()) {
			continue
		}
		break
	}
	if start == -1 {
		return Extent{}, false
	}
	return Extent{Start: children[start].StartByte(), End: children[end].EndByte()}, true
}

// importPredicate builds the test importsExtent walks root's children with:
// lang's own IsImport when it implements ImportMatcher — needed by a grammar
// like shell's, where an import is a node kind shared with everything else,
// distinguished only by its own text — or a plain ImportKinds membership
// test otherwise. Built once per call rather than re-checked per node, so
// the common (kind-only) path still pays for exactly one map allocation,
// same as before this existed.
func importPredicate(lang Language, src []byte) func(*ts.Node) bool {
	if m, ok := lang.(ImportMatcher); ok {
		return func(n *ts.Node) bool { return m.IsImport(src, n) }
	}
	kinds := kindSet(lang.ImportKinds())
	return func(n *ts.Node) bool { return kinds[n.Kind()] }
}

// OwnsTrailingSeparator reports lang's own answer (Language.
// OwnsTrailingSeparator) to whether its formatting convention treats the
// blank line following its @header or @imports region as belonging to that
// region, rather than as free-floating whitespace between two otherwise
// unrelated fragments. gofmt inserts exactly one there unconditionally for
// Go -- after the package clause and after the import block -- so in a
// gofmt'd file that blank line is as much "part of the header" as the
// package clause's own trailing newline is.
//
// Kept as a free function, rather than inlining lang.OwnsTrailingSeparator()
// at each call site, only so ExtendThroughOwnedSeparator below and every
// external caller keep the same call shape they had before this became an
// interface method.
func OwnsTrailingSeparator(lang Language) bool {
	return lang.OwnsTrailingSeparator()
}

// ExtendThroughOwnedSeparator widens ext's End through the whitespace
// immediately following it, up to limit, when lang's convention makes that
// whitespace part of the region itself (OwnsTrailingSeparator) -- otherwise
// ext is returned unchanged.
//
// This is deliberately not built into headerExtent/importsExtent themselves:
// those also compute the extent rgit diff renders for an ordinary (tracked-
// file) change, where the boundary between two regions has never been
// either one's to claim -- rgit diff's own (unanchorable) row is exactly the
// honest answer there. The caller that does need it is internal/synth's
// new-file preamble staging, where @header and @imports are the only things
// that will ever get their own row for that boundary at all.
func ExtendThroughOwnedSeparator(lang Language, src []byte, ext Extent, limit uint) Extent {
	if !OwnsTrailingSeparator(lang) {
		return ext
	}
	end := ext.End
	for end < limit {
		switch src[end] {
		case '\n', '\r', ' ', '\t':
			end++
		default:
			return Extent{Start: ext.Start, End: end}
		}
	}
	return Extent{Start: ext.Start, End: end}
}

// MembersSitFlush reports lang's own answer (Language.MembersSitFlush) to
// whether its convention keeps sibling container members -- struct fields,
// interface methods, class methods -- adjacent with no blank line between
// them, the way gofmt leaves Go struct fields and interface methods and
// prettier leaves TypeScript class methods: neither tool inserts or requires
// a separator there, so whatever the worktree already has is "flush" as far
// as either is concerned.
//
// Python is the opposite case: PEP 8 requires exactly one blank line between
// method definitions inside a class body (linters enforce it as E301), and
// a Python class's only addressable member kind is a method (lang_python.go
// never descends into plain attribute assignments) -- so a newly spliced-in
// Python method is treated as an ordinary top-level-shaped boundary, not a
// flush one, or the synthesized blob would drop a blank line every Python
// style guide expects there.
//
// This only governs a brand-new nested member being inserted, not the
// byte-identical replace/delete path a committed member already takes.
//
// Kept as a free function for the same reason OwnsTrailingSeparator is: so
// classify.go's call site keeps the shape it had before this became an
// interface method.
func MembersSitFlush(lang Language) bool {
	return lang.MembersSitFlush()
}

// toplevelExtent spans every addressable declaration's full extent (leading
// doc comments included). This excludes header and import material by
// construction: a Language's Declarations never returns entries for those.
//
// The bounds widen to whichever top-level node contains the first and last
// declaration, because a declaration is not always a top-level node itself: a
// spec inside a grouped `const (...)` block is addressed on its own, and
// stopping at its extent would leave the block's keyword and parentheses
// outside @toplevel while their contents were inside it.
//
// Markdown's @toplevel is a different region entirely -- the lede, not a
// declaration span -- because sectionDeclarations never returns an entry for
// the lede, so this formula would otherwise compute "first heading through
// end of document", the opposite of the decided design (lang_markdown.go's
// mdLanguage.toplevelExtent).
//
// Deliberately still dispatched via a type assertion rather than a Language
// method, unlike OwnsTrailingSeparator/MembersSitFlush/
// AllowsRawHeadingFallback above: those three are booleans an adapter states
// once and the shared caller branches on, so a missing case is silently
// wrong (the defect this file's other three methods were promoted to fix).
// toplevelExtent is not a flag to branch on -- it is the whole computation,
// needing idx, root, and src together -- so making it a Language method
// would force every adapter to either carry this exact 15-line formula
// itself (duplicated nine times, with no shared source of truth to catch
// the copies drifting apart) or call back into a package-level default
// anyway, which is what the type assertion already does more directly. A
// future grammar shaped like Markdown's -- @toplevel meaning something
// structurally different from "span of declarations" -- gets exactly the
// same override seam this one type assertion already provides; there being
// only one such grammar so far is not a coincidence to design around
// preemptively.
func toplevelExtent(lang Language, src []byte, root *ts.Node, idx *index) (Extent, bool) {
	if md, ok := lang.(*mdLanguage); ok {
		return md.toplevelExtent(src, root, idx)
	}

	if len(idx.order) == 0 {
		return Extent{}, false
	}
	first, last := idx.order[0], idx.order[len(idx.order)-1]
	ext := Extent{Start: first.Full.Start, End: last.Full.End}

	for _, c := range namedChildren(root) {
		if c.StartByte() <= first.Full.Start && first.Full.Start < c.EndByte() {
			if outer := fullExtent(lang, src, &c); outer.Start < ext.Start {
				ext.Start = outer.Start
			}
		}
		if c.StartByte() < last.Full.End && last.Full.End <= c.EndByte() {
			if c.EndByte() > ext.End {
				ext.End = c.EndByte()
			}
		}
	}
	return ext, true
}
