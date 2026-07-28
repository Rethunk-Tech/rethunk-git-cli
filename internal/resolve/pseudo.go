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
func headerExtent(lang Language, root *ts.Node) (Extent, bool) {
	kinds := kindSet(lang.HeaderKinds())
	children := namedChildren(root)

	end := uint(0)
	found := false
	for i := range children {
		c := &children[i]
		if !kinds[c.Kind()] {
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
		if start != -1 {
			break
		}
	}
	if start == -1 {
		return Extent{}, false
	}
	return Extent{Start: children[start].StartByte(), End: children[end].EndByte()}, true
}

// toplevelExtent spans every addressable declaration's full extent (leading
// doc comments included). This excludes header and import material by
// construction: a Language's Declarations never returns entries for those.
func toplevelExtent(idx *index) (Extent, bool) {
	if len(idx.order) == 0 {
		return Extent{}, false
	}
	first, last := idx.order[0], idx.order[len(idx.order)-1]
	return Extent{Start: first.Full.Start, End: last.Full.End}, true
}
