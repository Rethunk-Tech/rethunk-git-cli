"""Throwaway spike: validate rgit's blob-synthesis model against real files.

Tests the four mechanisms the DESIGN specifies but nothing has ever executed:
  1. symbol extent extraction with doc-comment attribution (blank-line rule)
  2. splice of a worktree extent into the HEAD blob
  3. reverse-byte-offset ordering for multiple extents in one file
  4. EOF-newline preservation (must NOT append one)
Plus the nearest-existing-sibling insertion rule for new symbols.
"""

import sys
from tree_sitter import Language, Parser
import tree_sitter_go

GO = Language(tree_sitter_go.language())
parser = Parser(GO)

# Node types that constitute an addressable top-level symbol in Go.
SYMBOL_NODES = {
    "function_declaration",
    "method_declaration",
    "type_declaration",
    "const_declaration",
    "var_declaration",
}


def _name_of(node, src: bytes):
    """Qualified anchor name: Receiver.Method for methods, else the bare name."""
    if node.type == "method_declaration":
        recv = node.child_by_field_name("receiver")
        name = node.child_by_field_name("name")
        if recv and name:
            # strip pointer/parens from "(a *A)" -> "A"
            rtext = src[recv.start_byte : recv.end_byte].decode()
            rtype = rtext.strip("()").split()[-1].lstrip("*")
            return f"{rtype}.{src[name.start_byte:name.end_byte].decode()}"
    n = node.child_by_field_name("name")
    if n:
        return src[n.start_byte : n.end_byte].decode()
    # const/var/type blocks: use the first declared identifier
    for c in node.named_children:
        n = c.child_by_field_name("name")
        if n:
            return src[n.start_byte : n.end_byte].decode()
    return None


def _doc_start(node, src: bytes, root) -> int:
    """Extend the extent upward over contiguous comments with no blank line between.

    DESIGN rule: a comment block directly above a symbol with NO intervening
    blank line belongs to it; a blank line breaks attribution.
    """
    start = node.start_byte
    prev = node.prev_sibling
    while prev is not None and prev.type == "comment":
        between = src[prev.end_byte : start]
        # exactly one newline (plus indentation) means no blank line between
        if between.count(b"\n") > 1:
            break
        start = prev.start_byte
        prev = prev.prev_sibling
    return start


def extents(src: bytes):
    """Map anchor name -> (start_byte, end_byte), doc comments included."""
    tree = parser.parse(src)
    out = {}
    order = []
    for child in tree.root_node.named_children:
        if child.type not in SYMBOL_NODES:
            continue
        name = _name_of(child, src)
        if not name:
            continue
        out[name] = (_doc_start(child, src, tree.root_node), child.end_byte)
        order.append(name)
    return out, order


def synthesize(head: bytes, work: bytes, anchors: list[str]) -> bytes:
    """Replace each named anchor's extent in `head` with its extent from `work`.

    Applies replacements in REVERSE byte-offset order so earlier splices do not
    invalidate later offsets (DESIGN step 3).
    """
    h_ext, h_order = extents(head)
    w_ext, w_order = extents(work)

    edits = []
    for a in anchors:
        if a not in w_ext:
            raise KeyError(f"anchor {a!r} not found in worktree content")
        w_start, w_end = w_ext[a]
        new_text = work[w_start:w_end]
        if a in h_ext:
            edits.append((h_ext[a][0], h_ext[a][1], new_text))
        else:
            edits.append((_insert_point(a, w_order, h_ext, h_order, head), None, new_text))

    # reverse byte-offset order
    edits.sort(key=lambda e: e[0], reverse=True)
    out = head
    for start, end, text in edits:
        if end is None:  # insertion
            sep = b"" if (start == 0 or out[start - 1 : start] == b"\n") else b"\n"
            out = out[:start] + sep + text + b"\n\n" + out[start:]
        else:  # replacement
            out = out[:start] + text + out[end:]
    return out


def _insert_point(name, w_order, h_ext, h_order, head: bytes) -> int:
    """Nearest existing sibling: walk back through worktree siblings to the first
    one present in HEAD and insert after it; else walk forward and insert before;
    else append at end of file."""
    i = w_order.index(name)
    for prev in reversed(w_order[:i]):          # backwards
        if prev in h_ext:
            return h_ext[prev][1] + 1
    for nxt in w_order[i + 1 :]:                # forwards
        if nxt in h_ext:
            return h_ext[nxt][0]
    return len(head)                            # end of enclosing scope



def check(label, got, want):
    ok = got == want
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}")
    if not ok:
        print(f"      got:  {got!r}")
        print(f"      want: {want!r}")
    return ok
