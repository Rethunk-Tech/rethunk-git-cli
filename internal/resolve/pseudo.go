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
// The run also stops at limit, where @toplevel begins. Comments are a header
// kind in both Go and Python, so without that bound a doc comment belonging to
// the first declaration would be swallowed: @header and @toplevel would each
// claim the same bytes, and staging @header alone would commit a comment the
// caller never named.
//
// The bound is @toplevel's start rather than the first declaration's, because
// a declaration can sit inside a top-level node — a spec in a grouped
// `const (...)` block — leaving the block's own doc comment ahead of it and
// therefore inside the header run.
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

// importsExtent spans the first contiguous run of ImportKinds nodes. Go
// emits exactly one import_declaration; TypeScript and Python emit one node
// per import statement, so the run can be many nodes wide — the pseudo-
// anchor must cover the whole run or staging @imports would silently drop
// everything after the first import (contracts-waveB.md).
//
// Comments do not break the run. In TypeScript and Python a grouping
// comment between two imports ("# stdlib", "// external") is an ordinary
// named sibling, and treating it as a terminator ended the extent early —
// staging @imports then reported success while committing only the
// imports above the first comment.
func importsExtent(lang Language, root *ts.Node) (Extent, bool) {
	kinds := kindSet(lang.ImportKinds())
	children := namedChildren(root)

	start, end := -1, -1
	for i := range children {
		c := &children[i]
		if kinds[c.Kind()] {
			if start == -1 {
				start = i
			}
			end = i
			continue
		}
		if start == -1 {
			continue
		}
		// Interior comments are carried along: end only advances on an
		// import, so a comment after the last one stays outside the
		// extent while one between two imports is spanned by it.
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

// toplevelExtent spans every addressable declaration's full extent (leading
// doc comments included). This excludes header and import material by
// construction: a Language's Declarations never returns entries for those.
//
// The bounds widen to whichever top-level node contains the first and last
// declaration, because a declaration is not always a top-level node itself: a
// spec inside a grouped `const (...)` block is addressed on its own, and
// stopping at its extent would leave the block's keyword and parentheses
// outside @toplevel while their contents were inside it.
func toplevelExtent(lang Language, src []byte, root *ts.Node, idx *index) (Extent, bool) {
	if len(idx.order) == 0 {
		return Extent{}, false
	}
	first, last := idx.order[0], idx.order[len(idx.order)-1]
	ext := Extent{Start: first.Full.Start, End: last.Full.End}

	for _, c := range namedChildren(root) {
		c := c
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
