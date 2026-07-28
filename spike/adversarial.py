"""Adversarial round: the cases designed to break the synthesis model."""

import sys
from synth import extents, synthesize, parser, check

failures = 0

print("A. blank line MUST break doc attribution (locked decision)")
src = b'''package p

func A() {}

// free-floating note about the module

// Doc for B.
func B() {}
'''
ext, _ = extents(src)
b_text = src[ext["B"][0]:ext["B"][1]]
failures += not check("B's extent starts at 'Doc for B'",
                      b_text.startswith(b"// Doc for B."), True)
failures += not check("free-floating note NOT swallowed into B",
                      b"free-floating" in b_text, False)

print("\nB. contiguous comments with no blank line ARE attributed")
src2 = b'''package p

// line one
// line two
// line three
func C() {}
'''
ext2, _ = extents(src2)
c_text = src2[ext2["C"][0]:ext2["C"][1]]
failures += not check("all three comment lines included",
                      c_text.count(b"// line"), 3)

print("\nC. deletion synthesis — remove a symbol's extent from HEAD")
head = b'''package p

// A does a thing.
func A() {}

// B does another.
func B() {}

func C() {}
'''
ext3, _ = extents(head)
s, e = ext3["B"]
# emulate DESIGN's deletion path: excise the extent, collapse the blank gap
deleted = head[:s] + head[e:].lstrip(b"\n")
failures += not check("B removed", b"func B()" in deleted, False)
failures += not check("B's doc comment removed too", b"B does another" in deleted, False)
failures += not check("A survives", b"func A()" in deleted, True)
failures += not check("C survives", b"func C()" in deleted, True)
failures += not check("result still parses", parser.parse(deleted).root_node.has_error, False)

print("\nD. method disambiguation by receiver")
src4 = b'''package p

type A struct{}
type B struct{}

func (a *A) Get() int { return 1 }

func (b *B) Get() int { return 2 }
'''
ext4, order4 = extents(src4)
failures += not check("A.Get addressable", "A.Get" in ext4, True)
failures += not check("B.Get addressable", "B.Get" in ext4, True)
failures += not check("bare 'Get' is NOT a key (forces qualification)",
                      "Get" in ext4, False)

print("\nE. splicing one overload leaves the other intact")
work4 = src4.replace(b"return 2", b"return 99")
got = synthesize(src4, work4, ["B.Get"])
failures += not check("B.Get updated", b"return 99" in got, True)
failures += not check("A.Get untouched", b"return 1" in got, True)
failures += not check("still parses", parser.parse(got).root_node.has_error, False)

print("\nF. adjacent symbols, no blank line between them")
head6 = b'package p\nfunc A() { return }\nfunc B() { return }\n'
work6 = b'package p\nfunc A() { return }\nfunc B() { return 7 }\n'
got6 = synthesize(head6, work6, ["B"])
failures += not check("B updated", b"return 7" in got6, True)
failures += not check("A intact", got6.count(b"func A()"), 1)
failures += not check("no run-together syntax error",
                      parser.parse(got6).root_node.has_error, False)

print("\nG. symbol whose body contains a nested func literal")
head7 = b'''package p

func Outer() func() int {
\treturn func() int { return 1 }
}

func After() {}
'''
work7 = head7.replace(b"return 1", b"return 2")
ext7, order7 = extents(head7)
failures += not check("nested literal not treated as top-level symbol",
                      order7, ["Outer", "After"])
got7 = synthesize(head7, work7, ["Outer"])
failures += not check("nested body updated", b"return 2" in got7, True)
failures += not check("After intact", b"func After() {}" in got7, True)

print(f"\n{'ALL PASS' if failures == 0 else f'{failures} FAILURE(S)'}")
sys.exit(1 if failures else 0)
