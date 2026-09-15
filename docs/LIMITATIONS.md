# Limitations

What `rgit` does not do, and why. Where a workaround exists, it's the same one
throughout: name the path instead of a symbol anchor.

## Unsupported languages

Any language with no tree-sitter grammar in this binary refuses a symbol
anchor with exit 9. Run `rgit languages` (or `rgit doctor`) for the exact
list the running binary supports; it drifts as grammars are added, which is
why this file doesn't restate it. C and C++ are the common examples with
none, and stay unsupported on the same demand survey that ordered every
shipped grammar: no repository surveyed contained any; config and data files
in those ecosystems still stage by path in the meantime.

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
- **Markdown/MDX** — in an `.mdx` file, MDX's own constructs: an ESM
  `import`/`export` line, a `<Component />` element, and a `{expression}`.
  None declares a heading, so none is addressable on its own; each parses as
  ordinary paragraph or `html_block` content inside whatever heading contains
  it, and a heading extent is byte-identical to the plain-Markdown one. An
  import above the first heading is reachable via `@toplevel`.
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
- **HTML** — everything a CSS selector can express beyond an element's own
  id: a class (`.widget`), an attribute selector (`[data-foo]`), `nth-of-type`
  and every other pseudo-class/pseudo-element (`:hover`, `::before`), and a
  descendant/child/sibling combinator (`.parent .child`, `>`, `+`, `~`). This
  is deliberate scope, not an oversight — `rgit` resolves anchors, it is not
  a CSS selector engine, and each of those is a step toward reimplementing
  one. An element with **no** id is also
  unaddressable, even a lone, unambiguous `<button>` with no sibling to
  confuse it with — HTML's own demand signal is the mount-point/component-root
  case (`div#app`), where an id already exists; falling back to a bare tag
  name would make "div" (or any common tag) collide across nearly every real
  document, since this resolver's index has no per-parent scoping to keep two
  same-named siblings apart the way a real DOM's `getElementById` uniqueness
  does. An id shared by two elements — the same tag twice, or two different
  tags — is exit 4 (ambiguous), the same as any other language's collision;
  name the qualified `tag#id` form, or the path, instead.

Full anchor and qualification rules: [`ANCHORS.md`](ANCHORS.md).

## Build and platform limits

- **No darwin binaries.** `rgit` links tree-sitter through cgo, and darwin
  fails at link time through zig without an actual macOS SDK (`-lresolv`,
  `-framework CoreFoundation`). Every build and release job runs on Linux,
  so releases ship linux/amd64, linux/arm64 and windows/amd64 only, and
  `scripts/install.sh` exits with an error on macOS; build from source
  there. Detail: [`INSTALL.md`](INSTALL.md#cross-builds).
- **SQL ships only behind the `rgit_sql` build tag.** A plain build works
  identically without it; a `.sql` anchor then exits 9 like any other
  unsupported language. Detail: [`INSTALL.md`](INSTALL.md#sql-support).
- **`make cross` carries SQL only when the build host can generate the
  parser** — that needs the tree-sitter CLI. Without it, every `dist/` binary
  is SQL-less, the same fallback `make install` makes. Detail:
  [`INSTALL.md`](INSTALL.md#cross-builds).

## Symlinks, submodules, renames, and content filters

In `commit`, a symbol anchor never addresses a symlink or a submodule
directory — both refuse with exit 10 (`SpecialPathRefused`), the same code,
rather than being misresolved as an ordinary file or silently producing an
empty symbol set. Name the path instead: `rgit commit link.txt` and `rgit
commit vendor/lib` stage them exactly as `git add` would, gitlink SHA and
symlink target included — a pathspec target is delegated straight to `git add`
and never touches `rgit`'s own byte synthesis at all
(`internal/synth/stage.go`'s `Pathspec` targets vs. `Symbol` targets). See
[`cmd/rgit/index_test.go`](../cmd/rgit/index_test.go)'s
`TestStage_SubmoduleAndSymlinkPathStaging` for both the pathspec staging and
the anchor refusal, on each of the two kinds.

An **unmerged path** likewise refuses a symbol anchor with exit 10
(`SpecialPathRefused`) in `commit`. Name the path instead so Git can stage the
conflict entries and their conflict-marker content without rgit attempting a
symbol splice.

**An uninitialized submodule refuses the identical anchor the same way** —
`git submodule deinit` leaves the directory in place, emptied of its own
`.git`, and `classifyPath` (`internal/synth/special.go`) cross-checks HEAD's
own `160000` tree entry rather than trusting local shape (".git present")
alone, so this is not silently misread as an ordinary directory. A **nested**
submodule (one submodule's own `.gitmodules` naming another) is invisible to
this repository's index either way — only the immediate gitlink entry at
its own path is ever visible from here, nested or not, so it needs no
different treatment.

**A sparse-checkout-excluded path** is handled by Git's skip-worktree bit.
The default diff scope is a real `git diff --numstat`
(`internal/gitx.DiffNumstat`), and git itself never reports a skip-worktree
path as changed, so in the ordinary case it never reaches `rgit`'s own
attribution. An explicit `FILE:SYMBOL` anchor on a path marked
skip-worktree refuses with exit 10 before blob synthesis; the bit is left
unchanged. The same refusal applies to an assume-unchanged path, in `commit`.
A pathspec target still reaches real `git add`, which handles Git's own
sparse-path advice (`git config advice.updateSparsePath`) without rgit
special-casing it.
If a path is absent from the index rather than marked with either bit, anchor
resolution retains its ordinary exit 3 (unresolvable) behavior.

A **rename** staged by symbol anchor is not detected as one: `HEAD` simply
has no blob at the new path, so an anchor into it stages as an ordinary new
file, and git's own tree diff is what notices the rename after the fact
(rename at full similarity in `git status`/`git diff`, the same as any rename
staged by hand). Renames staged by pathspec go through `git add` unmodified,
proven by `TestStage_RenameStagedAsTwoPathsYieldsR100`.

Every synthesized blob is written via `git hash-object -w --path <path>`
(`internal/gitx.HashObject`), never with `--path` omitted — the function
signature requires a path, and its one call site (`internal/synth/stage.go`)
always has one, so a `.gitattributes` clean filter always runs, whatever it
is: `TestStage_GitattributesCleanFilterRequiresPath` proves it with a
synthetic filter rather than requiring a real filter binary (`git-lfs`
included) in the test environment — Git LFS's own clean filter is invoked
through the identical mechanism, an ordinary `.gitattributes` `filter=`
entry, with nothing rgit-specific to special-case for it. A binary file is
refused for a symbol anchor in `commit` the same way a symlink or submodule
is (exit 10) — there is no content to attribute a symbol's bytes within.

## Partial clones without a reachable promisor remote

A blobless or otherwise promisor-backed clone needs Git to obtain an
unfetched blob before `rgit` can resolve or synthesize an anchor against it.
When the promisor remote is unreachable, Git reports the missing object with
exit 128; `rgit` preserves that error rather than treating the tracked path as
absent or synthesizing a full-file deletion. `rgit` does not fetch blobs itself.

## History across renames

`rgit log FILE:SYMBOL` runs `git log -L` bounded to the named file. `-L`
already tracks a line range across a rename on its own, the same content
similarity detection `git log --follow` uses for a whole file — but it does
so by following the diff, not by re-parsing the renamed file, so a rename
that also moves the symbol within the file (reordering, a surrounding
refactor) can lose the thread partway. `--follow-rename` covers that case by
re-resolving the anchor with tree-sitter at each rename boundary instead —
see [`USAGE.md`](USAGE.md#log-across-renames) — one parse per rename, not
per commit, which is the trade-off that made it worth building.

Either way, a symbol's history is only reachable starting from the file's
**current** name: querying it under a prior name directly fails at argument
classification, the same as naming any other path that exists under neither
the worktree, the index, nor `HEAD`.

`rgit blame FILE:SYMBOL` stops at the current file name by default. With
`--follow-rename`, it reads HEAD history, re-resolves the symbol at each rename
boundary, and emits git's bounded blame for each path segment.

## Language-server coverage

The extent cross-check is live for Go, TypeScript/TSX, Python, Rust, Shell,
YAML, JSON, CSS, Markdown, and HTML. TOML and SQL resolve with tree-sitter alone,
permanently in `[ts-only]` mode — a supported result, not a degraded one:

- **TOML** — `taplo` completes the LSP handshake, but its own ranges
  disagree with the extent `rgit` stages on an ordinary nested table, so
  installing it does not enable a cross-check.
- **SQL** — no maintained tool speaks `documentSymbol` for SQL at all.

**HTML wires with two narrower, safe carve-outs, not a full unwiring.**
`vscode-html-language-server` names and ranges an ordinary id-bearing
element exactly the way `rgit` does (`div#app`) once the points below are
accounted for:

- The server appends `.class…` selectors to the same `tag#id` name when the
  element carries a class. Flat matching strips those server-only suffixes
  before comparing, so class-bearing elements cross-check; staged extents
  stay the grammar's own boundary and never grow to include class text.
- A void element (`<input>`, `<img>`, `<br>`, and similarly self-closing-
  by-tag-name elements) measurably absorbs trailing whitespace or text up
  to its next real sibling boundary into its own node's range when one
  isn't immediately adjacent. That absorption is left alone in the extent
  that actually gets staged — the grammar's own honest boundary, matching
  TOML's own trailing-blank-line precedent — but trimmed back to the tag's
  own end in the declaration-only extent the cross-check compares against
  (`internal/resolve/lang_html.go`'s `trimDeclOnlyEnd`, an optional seam
  scoped to this one adapter, not a change to every grammar's own extent).

Install instructions and the full server table:
[`INSTALL.md`](INSTALL.md#language-servers).
