# Limitations

Where a workaround exists: name the path instead of a symbol anchor.

## Worktree bytes are never rewritten or discarded

No command rewrites or discards a worktree byte. Permanent: `rgit` is safe
as a fleet commit path with nothing to undo. `rgit show FILE:SYMBOL --source
REV` reads; that is not a write.

## Unsupported languages

No grammar in this binary → exit 9. `rgit languages` / `rgit doctor` list
what the running binary supports. C and C++ have none. **`.sass`** (indented
syntax) has no grammar here; `.scss` is supported.

## Constructs no anchor reaches

Stage the containing declaration or the path.

- **Go** — multi-name field (`A, B int`); embedded/anonymous field.
- **TypeScript** — anonymous default export; destructuring (`const {a, b}`,
  `const [x, y]`) — several names, one extent.
- **YAML** — sequence item; anything in a flow `{...}`/`[...]`; a
  `---`-separated multi-document stream; a comment between a nested value
  and the next shallower key (tree-sitter-yaml attaches it to whichever
  block was still open). `@toplevel` or the path still reach it.
- **Markdown/MDX** — MDX `import`/`export`, `<Component />`, `{expression}`
  are not headings. An import above the first heading is `@toplevel`.
- **JSON** — an array at any depth; the containing key owns the whole array.
- **TOML** — inline table or array: a leaf.
- **CSS** — a rule inside `@media`/`@supports`/`@keyframes` (the at-rule is
  addressable). Native nesting (`.parent { .child { } }`) stays addressable.
  An at-rule spelled literally `@header` is unreachable: the resolver tests for
  that pseudo-anchor first.
- **SQL** — `DROP`, `ALTER`, `INSERT`, `SELECT`, `CREATE SCHEMA`; unnamed
  `CREATE INDEX ON t (c)`.
- **HTML** — anything beyond an element's own id (class, attribute,
  pseudo, combinator). An element with no id is unaddressable even when
  unique: the index has no per-parent scoping, so a bare `div` would
  collide everywhere. Duplicate ids are exit 4. Qualification:
  [`ANCHORS.md`](ANCHORS.md).
- **Whitespace** changed between two unmodified tracked declarations is
  attributed as `(unanchorable)`: no declaration owns it.

## Build and platform limits

- **No darwin binaries.** cgo + zig without a macOS SDK (`-lresolv`,
  CoreFoundation). Releases: linux/amd64, linux/arm64, windows/amd64.
  `scripts/install.sh` errors on macOS. [`INSTALL.md`](INSTALL.md#cross-builds).
- **SQL is `-tags rgit_sql` only.** A plain build works; a `.sql` anchor is
  exit 9. [`INSTALL.md`](INSTALL.md#sql-support).
- **`make cross` needs the tree-sitter CLI.** Without it, generation is
  non-zero and `dist/` is not written. `make install` still falls back to an
  SQL-less host binary. [`INSTALL.md`](INSTALL.md#cross-builds).

## Symlinks, submodules, renames, and content filters

A symbol anchor in `commit` refuses symlink, submodule, binary, unmerged,
skip-worktree, and assume-unchanged paths with exit 10. Name the path:
`git add` gets the gitlink SHA or symlink target
(`internal/synth/stage.go` `Pathspec` vs `Symbol`). See
`TestStage_SubmoduleAndSymlinkPathStaging`.

An uninitialized submodule (`git submodule deinit`: directory remains,
`.git` gone) is the same exit 10 — `classifyPath` (`internal/synth/special.go`)
checks HEAD's `160000` entry, not local shape. A nested submodule is
invisible from this index.

Skip-worktree / assume-unchanged: `git diff --numstat` never reports them
as changed. An explicit `FILE:SYMBOL` is exit 10; the bit is unchanged. A
pathspec still reaches `git add`. Absent from the index is ordinary exit 3.

A rename staged by symbol is a new file at the new path; git's tree diff
notices similarity after. Pathspec renames go through `git add`
(`TestStage_RenameStagedAsTwoPathsYieldsR100`).

Every synthesized blob is `git hash-object -w --path <path>`
(`internal/gitx.HashObject`) so `.gitattributes` clean filters run
(`TestStage_GitattributesCleanFilterRequiresPath`).

## Partial clones without a reachable promisor remote

An unfetched blob is git's problem. Unreachable promisor → git exit 128;
`rgit` does not treat the path as absent or synthesize a deletion. `rgit`
does not fetch.

## History across renames

`git log -L` follows a rename by line-range diff, so a rename that also
moves the symbol can lose it. `--follow-rename` re-resolves at each
rename boundary — [`USAGE.md`](USAGE.md#log-across-renames). Query the
**current** name. `--source REV` without `--follow-rename` does not
follow; with both flags the walk starts at `REV` and still re-resolves
at each later rename.

`blame --follow-rename` and `show --follow-rename` use that walk. Combined
with `--source`, they start at that revision too.

## Language-server coverage

Cross-check is live for Go, TypeScript/TSX, Python, Rust, Shell, YAML,
JSON, JSONC, CSS, SCSS, Zsh, Markdown, and HTML. TOML, SQL, and JSON5 are
permanently `[ts-only]`:

- **TOML** — `taplo` handshakes, but its ranges disagree on a nested table.
- **SQL** — no maintained `documentSymbol` server.
- **JSON5** — no server reports symbols; `vscode-json-language-server` is
  JSON/JSONC only.

**HTML** is wired with two carve-outs (`vscode-html-language-server`):

- The server appends `.class…` after `tag#id`. An id may contain a dot
  (`a#toc-mod.io`); only a further `.class` suffix is the class list.
  `<a id="toc-mod" class="io">` and `<a id="toc-mod.io">` are both reported
  as `a#toc-mod.io`. The cross-check refuses both — pairing would guess.
  Staged extents stay the grammar's boundary.
- A void element (`<input>`, `<img>`, `<br>`) can absorb trailing
  whitespace into its node. The staged extent keeps that boundary; the
  compared extent trims to the tag (`trimDeclOnlyEnd` in
  `internal/resolve/lang_html.go`).

Servers: [`INSTALL.md`](INSTALL.md#language-servers).
