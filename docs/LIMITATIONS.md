# Limitations

What `rgit` does not do, and why. Where a workaround exists, it's the same one
throughout: name the path instead of a symbol anchor.

## Unsupported languages

Any language with no tree-sitter grammar in this binary refuses a symbol
anchor with exit 9 — Rust, C, C++, and HTML are common examples with none.
Run `rgit languages` (or `rgit doctor`) for the exact list the running
binary supports; it drifts as grammars are added, which is why this file
doesn't restate it. What's planned beyond the shipped set is tracked in
[`../TODO.md`](../TODO.md#v2--grammars).

Two exclusions inside otherwise-supported languages are deliberate, not gaps
waiting to close:

- **`.scss`/`.sass`** — no SCSS/SASS tree-sitter grammar publishes Go
  bindings, so CSS support stops at plain CSS.
- **`.zsh`** — `.sh`/`.bash` resolve via tree-sitter-bash, a POSIX/Bash
  grammar that mis-parses zsh-only syntax. A wrong extent is worse than an
  honest refusal, so `zsh` gets neither.

## Constructs no anchor reaches

Even inside a supported language, some shapes have no name of their own to
address. Stage the containing declaration or the path instead.

- **Go** — a field line naming several identifiers at once (`A, B int`); an
  embedded/anonymous field.
- **TypeScript** — an anonymous default export (`export default function ()
  {}`); a destructuring declarator (`const {a, b} = obj`, `const [x, y] =
  arr`) — several names bind off one pattern node, so none owns its own
  extent.
- **YAML** — a sequence item; anything inside a flow-style `{...}`/`[...]`
  value, at any nesting depth; a `---`-separated multi-document stream
  (nothing is addressable by key across documents); a comment sitting
  between the end of a nested value and the next, more shallowly indented
  key — tree-sitter-yaml's own scanner attaches it to whichever block was
  still open when it read the comment token, not to either neighboring key
  (still reachable via `@toplevel` or the whole file).
- **JSON** — an array, at any depth: the containing key addresses the whole
  array, never one element.
- **TOML** — an inline table (`{ a = 1 }`) or array: a leaf, never descended
  into.
- **CSS** — a rule nested inside an `@media`/`@supports`/`@keyframes` block;
  the enclosing at-rule is addressable, the rules inside it are not. Native
  CSS Nesting (`.parent { .child { ... } }`) is unaffected by this and stays
  addressable — the exclusion is only for at-rule bodies.
- **SQL** — `DROP`, `ALTER`, `INSERT`, `SELECT`, and `CREATE SCHEMA` declare
  no persistent named object, so none is addressable; a `CREATE INDEX` with
  no name (`CREATE INDEX ON t (c)`) has nothing to read one from either.

Full anchor and qualification rules: [`ANCHORS.md`](ANCHORS.md).

## Build and platform limits

- **darwin/amd64 and darwin/arm64 are not cross-built.** `rgit` links
  tree-sitter through cgo, and darwin fails at link time without an actual
  macOS SDK (`-lresolv`, `-framework CoreFoundation`). Build on a Mac, or in
  CI with a macOS runner. Detail: [`INSTALL.md`](INSTALL.md#cross-builds).
- **SQL ships only behind the `rgit_sql` build tag.** A plain build works
  identically without it; a `.sql` anchor then exits 9 like any other
  unsupported language. Detail: [`INSTALL.md`](INSTALL.md#sql-support).
- **`make cross` never carries SQL support**, even for targets that
  otherwise build cleanly — generating the SQL parser needs the tree-sitter
  CLI on the build host plus a per-target C compile that doesn't fit a cross
  matrix. Detail: [`INSTALL.md`](INSTALL.md#cross-builds).

## Language-server coverage

The extent cross-check is live for Go, TypeScript/TSX, Python, Shell, YAML,
JSON, CSS, and Markdown. TOML and SQL resolve with tree-sitter alone,
permanently in `[ts-only]` mode — a supported result, not a degraded one:

- **TOML** — `taplo` completes the LSP handshake, but its own ranges
  disagree with the extent `rgit` stages on an ordinary nested table, so
  installing it does not enable a cross-check.
- **SQL** — no maintained tool speaks `documentSymbol` for SQL at all.

Install instructions and the full server table:
[`INSTALL.md`](INSTALL.md#language-servers).
