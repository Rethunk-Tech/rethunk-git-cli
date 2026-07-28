"""Drive a live gopls and check the DESIGN's declaration-only normalization rule.

The rule under test: tree-sitter's extent INCLUDES leading doc comments, LSP's
range does not, so the cross-check must compare tree-sitter's *declaration-only*
extent against lsp_range. Derived from Go's AST; never checked over the wire.
"""

import json
import os
import subprocess
import sys
import time

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "mod"))
FILE = os.path.join(ROOT, "auth.go")


class Client:
    def __init__(self, proc):
        self.p = proc
        self.id = 0

    def _send(self, obj):
        body = json.dumps(obj).encode()
        self.p.stdin.write(b"Content-Length: %d\r\n\r\n" % len(body) + body)
        self.p.stdin.flush()

    def _read(self):
        headers = {}
        while True:
            line = self.p.stdout.readline()
            if not line:
                raise RuntimeError("gopls closed the connection")
            line = line.strip()
            if not line:
                break
            k, _, v = line.decode().partition(":")
            headers[k.strip().lower()] = v.strip()
        n = int(headers["content-length"])
        return json.loads(self.p.stdout.read(n))

    def request(self, method, params, timeout=60):
        self.id += 1
        rid = self.id
        self._send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        deadline = time.time() + timeout
        while time.time() < deadline:
            msg = self._read()
            if msg.get("id") == rid:          # our response (skip notifications)
                if "error" in msg:
                    raise RuntimeError(msg["error"])
                return msg.get("result")
        raise TimeoutError(method)

    def notify(self, method, params):
        self._send({"jsonrpc": "2.0", "method": method, "params": params})


def flatten(syms, out=None, container=None):
    """DocumentSymbol[] is a tree; SymbolInformation[] is flat. Handle both."""
    out = [] if out is None else out
    for s in syms:
        if "location" in s:                    # SymbolInformation
            out.append({
                "name": s["name"],
                "container": s.get("containerName"),
                "range": s["location"]["range"],
                "selection": None,
            })
        else:                                  # DocumentSymbol
            out.append({
                "name": s["name"],
                "container": container,
                "range": s["range"],
                "selection": s.get("selectionRange"),
            })
            if s.get("children"):
                flatten(s["children"], out, s["name"])
    return out


def main():
    src = open(FILE, "rb").read()
    proc = subprocess.Popen(
        ["gopls", "-mode=stdio"],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
    )
    c = Client(proc)
    uri = "file://" + FILE
    c.request("initialize", {
        "processId": os.getpid(),
        "rootUri": "file://" + ROOT,
        "capabilities": {"textDocument": {"documentSymbol": {
            "hierarchicalDocumentSymbolSupport": True}}},
    })
    c.notify("initialized", {})
    c.notify("textDocument/didOpen", {"textDocument": {
        "uri": uri, "languageId": "go", "version": 1, "text": src.decode()}})

    syms = flatten(c.request("textDocument/documentSymbol", {
        "textDocument": {"uri": uri}}))

    lines = src.decode().splitlines()
    print("gopls documentSymbol results (1-indexed lines):\n")
    for s in syms:
        r = s["range"]
        sl, el = r["start"]["line"] + 1, r["end"]["line"] + 1
        cont = f"  container={s['container']}" if s["container"] else ""
        print(f"  {s['name']:16} range L{sl}..L{el}{cont}")
        print(f"      first line of range: {lines[sl-1]!r}")

    try:
        c.request("shutdown", None, timeout=5)
        c.notify("exit", {})
    except Exception:
        pass
    proc.terminate()
    return syms, lines


if __name__ == "__main__":
    syms, lines = main()
    json.dump(
        [{"name": s["name"], "container": s["container"],
          "startLine": s["range"]["start"]["line"] + 1,
          "endLine": s["range"]["end"]["line"] + 1} for s in syms],
        open(os.path.join(os.path.dirname(__file__), "gopls.json"), "w"), indent=1)
    print("\nwrote gopls.json")
