"""Basic happy-path cases, split out so synth.py is importable."""
import sys
from synth import extents, synthesize, parser, check




HEAD = b'''package p

import "fmt"

// ValidateToken checks the JWT.
// Returns ErrExpired if stale.
func ValidateToken(t string) error {
\treturn nil
}

// helper does a thing.
func helper() int { return 1 }

func untouched() {}
'''

WORK = b'''package p

import "fmt"

// ValidateToken checks the JWT.
// Returns ErrExpired if stale, per RFC 7519.
func ValidateToken(t string) error {
\tif t == "" {
\t\treturn fmt.Errorf("empty")
\t}
\treturn nil
}

// helper does a thing.
func helper() int { return 2 }

func untouched() {}
'''

failures = 0
print("1. extent extraction + doc-comment attribution")
ext, order = extents(HEAD)
failures += not check("symbols found in file order", order,
                      ["ValidateToken", "helper", "untouched"])
vt = HEAD[ext["ValidateToken"][0]:ext["ValidateToken"][1]]
failures += not check("ValidateToken extent starts at its doc comment",
                      vt.startswith(b"// ValidateToken checks"), True)
failures += not check("extent ends at closing brace", vt.endswith(b"}"), True)

print("\n2. single-symbol splice leaves siblings untouched")
got = synthesize(HEAD, WORK, ["ValidateToken"])
failures += not check("ValidateToken body updated", b'fmt.Errorf("empty")' in got, True)
failures += not check("helper NOT updated (still returns 1)", b"return 1 }" in got, True)
failures += not check("doc comment updated with symbol", b"RFC 7519" in got, True)

print("\n3. multi-symbol splice, reverse byte-offset order")
got2 = synthesize(HEAD, WORK, ["ValidateToken", "helper"])
failures += not check("both bodies updated", (b'fmt.Errorf("empty")' in got2 and b"return 2 }" in got2), True)
failures += not check("untouched symbol intact", b"func untouched() {}" in got2, True)
failures += not check("no duplicated content", got2.count(b"func helper()"), 1)

print("\n4. EOF-newline preservation (must NOT append one)")
head_nonl = b'package p\n\nfunc A() {}\n\nfunc B() { return }'   # no trailing \n
work_nonl = b'package p\n\nfunc A() {}\n\nfunc B() { return 42 }'  # also none
got3 = synthesize(head_nonl, work_nonl, ["B"])
failures += not check("body updated", b"return 42" in got3, True)
failures += not check("still has NO trailing newline", got3.endswith(b"\n"), False)

print("\n5. new-symbol insertion at nearest existing sibling")
head_ins = b'package p\n\nfunc A() {}\n\nfunc C() {}\n'
work_ins = b'package p\n\nfunc A() {}\n\nfunc XNew() {}\n\nfunc YNew() {}\n\nfunc C() {}\n'
got4 = synthesize(head_ins, work_ins, ["YNew"])          # neighbour XNew is ALSO new
ia, iy, ic = got4.find(b"func A()"), got4.find(b"func YNew()"), got4.find(b"func C()")
failures += not check("YNew inserted", iy != -1, True)
failures += not check("ordered A < YNew < C (walked back past new XNew to A)",
                      ia < iy < ic, True)
failures += not check("XNew NOT staged (was not named)", b"XNew" in got4, False)

print("\n6. synthesized output still parses as valid Go")
for label, blob in (("single", got), ("multi", got2), ("insert", got4)):
    t = parser.parse(blob)
    failures += not check(f"{label}: no ERROR nodes", t.root_node.has_error, False)

print(f"\n{'ALL PASS' if failures == 0 else f'{failures} FAILURE(S)'}")
sys.exit(1 if failures else 0)
