"""Does the declaration-only normalization rule actually hold against live gopls?"""
import json, sys, os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "spike"))
from synth import extents, parser, check

src = open("mod/auth.go","rb").read()
ts, order = extents(src)
gopls = {s["name"]: s for s in json.load(open("gopls.json"))}

def line_of(byte_off): return src[:byte_off].count(b"\n") + 1

def decl_only_start(name):
    """Strip the leading comment block: DESIGN's normalization."""
    s, e = ts[name]
    seg = src[s:e]
    # skip contiguous leading comment lines
    lines = seg.split(b"\n")
    i = 0
    while i < len(lines) and lines[i].lstrip().startswith(b"//"):
        i += 1
    return s + len(b"\n".join(lines[:i])) + (1 if i else 0)

failures = 0
print("tree-sitter (declaration-only) vs gopls range:\n")
print(f"  {'anchor':14} {'ts_full':>9} {'ts_declOnly':>12} {'lsp_range':>11}   match")
NAMEMAP = {"ValidateToken":"ValidateToken", "A.Get":"(*A).Get", "B.Get":"(*B).Get"}
for ours, theirs in NAMEMAP.items():
    if ours not in ts or theirs not in gopls:
        print(f"  {ours:14} MISSING (ts={ours in ts}, gopls={theirs in gopls})"); failures += 1; continue
    fs, fe = ts[ours]
    d = decl_only_start(ours)
    ts_full = f"L{line_of(fs)}..L{line_of(fe)}"
    ts_decl = f"L{line_of(d)}..L{line_of(fe)}"
    g = gopls[theirs]
    lsp = f"L{g['startLine']}..L{g['endLine']}"
    ok = (line_of(d) == g["startLine"] and line_of(fe) == g["endLine"])
    print(f"  {ours:14} {ts_full:>9} {ts_decl:>12} {lsp:>11}   {'YES' if ok else 'NO'}")
    failures += not ok

print()
failures += not check("normalization required (raw ts extent != lsp range for documented symbol)",
                      line_of(ts['ValidateToken'][0]) == gopls['(*A).Get' if False else 'ValidateToken']['startLine'], False)
failures += not check("gopls names methods with pointer receiver syntax",
                      "(*A).Get" in gopls, True)
failures += not check("our DESIGN form 'A.Get' is NOT what gopls emits",
                      "A.Get" in gopls, False)
failures += not check("gopls provides no containerName for methods",
                      gopls["(*A).Get"].get("container"), None)
print(f"\n{'ALL PASS' if failures==0 else f'{failures} FAILURE(S)'}")
sys.exit(1 if failures else 0)
