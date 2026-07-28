"""Are @imports / @header / @toplevel actually addressable nodes?"""
import sys
from synth import parser, check

src = b'''//go:build linux

// Package p does things.
package p

import (
\t"fmt"
\t"os"
)

const Version = "1.0"

var logger *os.Logger

type Config struct{ Name string }

func Run() { fmt.Println(Version) }
'''
tree = parser.parse(src)
kids = [(c.type, src[c.start_byte:c.end_byte][:34]) for c in tree.root_node.named_children]
print("top-level node types:")
for t, txt in kids:
    print(f"   {t:26} {txt!r}")

types = [t for t, _ in kids]
failures = 0
print()
failures += not check("@imports -> single import_declaration node",
                      types.count("import_declaration"), 1)
failures += not check("@header -> package_clause present",
                      "package_clause" in types, True)
failures += not check("@header -> build tag is a comment before package_clause",
                      types.index("comment") < types.index("package_clause"), True)
failures += not check("@toplevel -> const/var/type all addressable",
                      all(t in types for t in ("const_declaration","var_declaration","type_declaration")), True)
print(f"\n{'ALL PASS' if failures==0 else f'{failures} FAILURE(S)'}")
sys.exit(1 if failures else 0)
