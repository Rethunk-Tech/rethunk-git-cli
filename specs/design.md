# rgit — design record

Why `rgit` is shaped the way it is, and what was measured to establish each
decision. Behaviour itself is documented in [`../docs/USAGE.md`](../docs/USAGE.md)
and [`../docs/ANCHORS.md`](../docs/ANCHORS.md); this record explains the
reasoning and holds the evidence.

## Governing principle

**`rgit` is `git add <pathspec> && git commit` at symbol granularity.** Where
git has an opinion, `rgit` matches it exactly rather than inventing semantics.

This is load-bearing. Apply it before adding any behaviour; a proposal to
diverge needs to argue against it explicitly. Each consequence was established
by measurement, not assertion:

| Consequence | What was measured |
| --- | --- |
| Pre-staged work comes along | `git add target && git commit` includes a separately staged file; `git commit -- target` does not. The former is what `rgit` replaces |
| Staging uses the real `.git/index` | Removing the private index removed the reconcile step, rollback, and post-commit repair along with it |
| A rejected commit leaves staging in place | A `pre-commit` hook exiting non-zero leaves staged entries untouched — there is nothing to roll back |
| Hooks are not policed | `git add -A` in a hook was measured sweeping an unrelated file into a commit. Plain `git commit` behaves identically; filtering would break formatter and codegen hooks |
| Merges are not special-cased | `git commit` mid-merge reads `MERGE_HEAD` and writes a correct two-parent commit unaided |

**A private index is rejected.** Seeding a temporary index from `HEAD` to
*exclude* pre-staged work would force index snapshots, restores, and rollback —
roughly a third as much mechanism again — to arrive at semantics that diverge
from the `add && commit` `rgit` replaces.

## Blob synthesis

Staging a symbol means constructing the blob that *would* exist if only that
symbol had changed:

1. **Read sources** — HEAD blob `B_head` (`git cat-file -p HEAD:<path>`, empty
   if untracked) and worktree content `W`.
2. **Resolve extents** — the symbol's extent in `W`, and the corresponding
   extent in `B_head`, both via tree-sitter on the same qualified anchor.
3. **Synthesize**
   - Present in `B_head`: replace that extent with `W`'s.
   - New: insert at the **nearest existing sibling** — walk backwards through
     `W`'s siblings to the first also in `B_head` and insert after it; else walk
     forwards and insert before; else append at end of scope. Determined even
     when immediate neighbours are themselves new (`W = [A, X_new, Y_new, C]`,
     staging `Y_new` → after `A`), and order is preserved.
   - Multiple extents in one file: apply in **reverse byte-offset order** so
     earlier replacements do not invalidate later offsets.
   - **Boundary padding** normalizes newlines *between* spliced regions only.
     It must not touch end-of-file: git tracks no-newline-at-EOF as real content
     (`\ No newline at end of file`), so blanket normalization would commit a
     byte the caller never changed.
   - **Appending at end-of-file inherits `B_head`'s own convention.** When the
     insertion point is the end of the file — the common case, since appending
     after the last existing symbol lands there — the synthesized blob ends with
     a newline if and only if `B_head` did. This is the one place where
     "insert between two regions" has only one region: forcing a trailing
     newline would
     add a byte to a file that never had one, and forcing its absence would
     strip one from a file that did.
4. **Write** — `git hash-object -w --path <path> --stdin`. **`--path` is
   mandatory**: without it, `.gitattributes` clean filters and LFS
   normalization are bypassed. Measured writing `synthesized` where `git add`
   wrote `SYNTHESIZED` under an active filter.
5. **Stage** — `git update-index --add --cacheinfo <mode>,$SHA,<path>`.

**Mode** comes from `os.Stat()` (`mode & 0111`). When `W` is absent — staging a
symbol deletion from a deleted file — it falls back to `git ls-tree HEAD`.

**Resolve everything before staging anything.** Resolution is a pure read, so a
failure leaves the index exactly as found. The caller never ran `git add`;
nothing should have moved.

**A mid-`apply` I/O failure can leave a mixed-target commit half-staged, and
that is inherited, not invented.** Once resolution succeeds, `apply` stages
pathspecs through one `git add --` call, then stages each synthesized blob
through its own `hash-object` + `update-index` pair, one file at a time. The
pathspec call is atomic — measured against real git: `git add -- a b c`
staged nothing at all, not even the two readable files, when the third
pathspec named a `chmod 000` file git could not open. But the per-file synth
loop is not one call; it is N independent git invocations, so a later
file's I/O failure (a full disk, a permission race) does not roll back an
earlier file's already-durable `update-index`. Measured the same way: two
sequential `git add` calls, the second naming a path that does not exist,
left the first file's staged entry untouched afterward. That is exactly the
shape `apply`'s own loop has — nothing here fabricates a transaction git
itself does not offer; a caller who wants the index back exactly as it was
already has `git reset` for it.

**An all-`Unchanged` file is still re-staged, deliberately.** `PlanStage`
never drops an op just because its target turned out byte-identical between
`HEAD` and the worktree; `apply` still runs its hash-object + update-index
pair even when every op in that file is one. Stripping those ops looked
like a free win — same synthesized blob either way, one fewer round trip —
but `git add path` has no "skip if identical" case of its own: naming a
path re-stages its current bytes unconditionally, whether or not anything
about it changed. Skipping only the all-`Unchanged` file would special-case
rgit away from that behaviour rather than toward it, and would change what
happens to a path that already has different content staged from outside
the invocation (a manual `git add` run before `rgit commit`): today, naming
any anchor in a path collapses its index entry back to `HEAD` plus the
named anchors regardless of outcome, the same as every other target
combination; skipping the all-`Unchanged` case would carve out the one
content-dependent exception to that. Verified before deciding, not assumed:
`Plan.Results()` — which the exit-11 "every named target is unchanged"
rule and `--dry-run`'s own preview both read — is built once per target in
`planStage` and never touched by `apply`'s loop, so this was never a choice
between correctness and speed; it was purely whether to diverge from git's
own re-stage-unconditionally rule for one specific case, and the answer is
no.

**Concurrency** needs no handling: two `rgit` runs contend on `.git/index`
exactly as two `git add` runs do, and git's `index.lock` arbitrates.

## Symbol resolution

Tree-sitter is the primary resolver: it computes extents immediately with no
process-spawn latency. A language server, when reachable, cross-checks.

1. **Probe** an existing daemon socket — `$RGIT_LSP_SOCKET`, then the managed
   default at `rgit-<uid>/rgit-<server>.sock` inside `$XDG_RUNTIME_DIR`. Dial
   budget **150ms**, query deadline **2s**.
2. **Spawn on demand** if no socket is live, completing the *current*
   invocation in `[ts-only]` mode rather than blocking on a cold index.
   Guarded by an `O_EXCL` lock beside the socket.
3. **Cross-check** tree-sitter's **declaration-only** extent (the doc-comment
   prefix stripped; a decorator or an `export` keyword is not) against the LSP
   `range`. Any mismatch is a hard fail — print both ranges and stage nothing.
4. **Degrade** to tree-sitter alone on timeout or a still-indexing server,
   announced with `[ts-only]` on stderr.

**The managed default path is trusted only after its directory is verified,
not because of its name.** `$XDG_RUNTIME_DIR` falls back to a world-writable
`os.TempDir()` when unset, and `rgit-<server>.sock` is itself a predictable
name — a path with the right shape is not evidence it is safe to dial or
spawn into. Before every dial or spawn against the managed default, `rgit`
creates (or re-verifies) a `0700` subdirectory scoped to the caller's UID
(`rgit-<uid>/`) and `Lstat`s it fresh: still owned by the current user, still
an actual directory rather than a symlink, and carrying no group/other
permission bits. Any check failing degrades to `[ts-only]` rather than
trusting the path — the alternative is a predictable path in a shared,
world-writable temp directory that another user on the same multi-user host
could pre-create, planting a listener that then reads every file `rgit`
sends it over `textDocument/didOpen`. A caller-supplied `$RGIT_LSP_SOCKET` is
exempt from this check: it is the caller's own path to manage, not one
`rgit` need vouch for.

**Spawn-on-demand is load-bearing.** A socket-only design was measured finding
**no sockets and no running language servers**: editors spawn `gopls` over
stdio, so nothing ever creates one. Without spawning, the cross-check is
permanently dead code and the accuracy argument for symbol anchors collapses.

**Normalization is required, not defensive.** LSP excludes doc comments from
symbol ranges. Verified against a live `gopls`:

```text
anchor           ts_full   ts_declOnly   lsp_range   match
ValidateToken    L5..L14       L9..L14     L9..L14   yes
A.Get           L19..L20      L20..L20    L20..L20   yes
B.Get           L22..L23      L23..L23    L23..L23   yes
```

The raw tree-sitter extent starts at L5 where gopls starts at L9, so comparing
unnormalized extents would hard-fail **every documented symbol**.

gopls also names methods `(*A).Get` with no `containerName` field, hence the
input normalization described in [`../docs/ANCHORS.md`](../docs/ANCHORS.md).

**Daemon mechanism verified:** `gopls -listen unix;<sock>` brought the socket up
in ~0.5s and a client dialled it in **0.0ms**. A second instance on the same
socket exited from the bind conflict unaided, so the `O_EXCL` lock is not
required for correctness — it is kept to avoid launching doomed processes and
their stderr noise under concurrent invocation.

### Transport support per server

The daemon design above holds for `gopls` alone. Each of the others was
measured directly rather than taken from its own `--help`:

| Server | `--help` claim | Measured behaviour | Verdict |
| --- | --- | --- | --- |
| `gopls` | `-listen=string`, prefixable `unix;` | Creates a real unix-domain socket file; other processes dial in | Listen-mode daemon |
| `vtsls` | `--socket=<number>` | With nothing listening on that TCP port, exits immediately (code 0, no output). Given a pre-bound TCP listener, connects to it as a client | Dials **out**, not a daemon |
| `pyright-langserver` | `--socket=<number>` | Same shape as `vtsls`: exits immediately with nothing listening; given a pre-bound TCP listener, connects out and streams `window/logMessage` over it | Dials **out**, not a daemon |
| `bash-language-server` | `start` — "listening on stdin/stdout" | No socket or listen option of any kind: `--help` offers `start`, `--help` and `--version` and nothing else | stdio only, no transport choice to make |

Verified with `vtsls --socket=<port>` / `pyright-langserver --socket=<port>`
against an empty port (immediate exit) and then against a port with `nc -l`
already bound (successful outbound connection, confirmed via `ss -tn` and by
observing `pyright-langserver` write real JSON-RPC frames to the accepting
listener). Neither tool's `--socket` takes a path, so even the outbound mode
has no unix-socket form to standardize on with `gopls`.

**Consequence: two transports behind one `Dial` interface, not one.** `gopls`
alone gets the probe → spawn → degrade sequence above, at the managed default
`rgit-<uid>/rgit-gopls.sock`. Every other wired server — `vtsls`,
`pyright-langserver`, `bash-language-server`, `yaml-language-server`,
`vscode-json-language-server`, `vscode-css-language-server`, and `marksman` —
gets a one-shot stdio subprocess (each one's own default and only
listen-free mode: `--stdio`, `start`, or `server` depending on the binary)
spawned fresh per query, bounded by dial budget + query deadline end to end,
and killed on close rather than left running — there is no persistent daemon
for any of them to reuse, so pretending otherwise would just be a subprocess
rgit forgets to clean up.

**The query deadline is 2s, and a warm `gopls` alone would justify far less.**
That daemon answers in single-digit milliseconds; the stdio servers do not.
Measured end to end through `Dial` + `DocumentSymbols`, single-declaration
fixtures, warm binaries:

| Server | Transport | Dial | First `documentSymbol` after `didOpen` |
| --- | --- | --- | --- |
| `gopls` | unix socket, warm daemon | 1ms | 23ms |
| `pyright-langserver` | one-shot stdio | 102ms | 135ms |
| `vtsls` | one-shot stdio | 84ms | **259ms** |

`vtsls` misses a 250ms deadline by single-digit milliseconds: under that
budget the TypeScript cross-check would degrade to `[ts-only]` on every run —
present in the code and absent in effect. A deadline that only ever fires is
not a budget, it is a disabled feature, and the accuracy argument for symbol
anchors depends on the cross-check actually executing.

2s clears all three with room for larger files. It does not weaken "never
block on a cold server": that rule is about a server still building its index,
which is handled by degrading, not by the deadline. The deadline exists to
bound a server that has already answered the handshake and is now merely slow.

### Cross-check exemptions

Tree-sitter alone, no LSP comparison: pseudo-anchors (servers do not report
import blocks as document symbols), deletions (the symbol exists only in HEAD,
outside the server's worktree view), and any degraded or absent daemon.

A fourth case is degraded rather than exempt by category: the daemon answers,
but its own `documentSymbol` outline simply does not name the anchor being
checked (a symbol kind the server
doesn't surface, or a container shape rgit's name normalization doesn't
recognize). That is not the same claim as "the extents disagree" — there is
nothing to compare — so it degrades to `[ts-only]` rather than hard-failing.
Treating it as exit 6 would mean an incomplete server outline could block a
commit for a symbol tree-sitter resolved correctly.

### Grammar scope

Grammars are chosen by measured demand, never by popularity. Go, TypeScript and
Python set the bar: measured against 60 real commits in a live repo, **78%** of
touched files were one of the three and **91%** of added lines fell inside a
symbol body, so those three cover the dominant case, with `@toplevel` /
`@imports` handling the 8% at module scope. Median churn per touched file was
**4%** (p90 20%), which is precisely where symbol staging beats whole-file
staging; if commits typically rewrote most of a file, the tool would add nothing.

Markdown and Shell clear the same bar, surveyed across 51
repositories: Markdown appears in every one of them, Shell in 45%. Shell earns
a caveat the others do not — fewer than half of its lines sit inside a function
and most shell files define none at all, so its value concentrates in
library-style scripts rather than spreading across the language.

**Named nested declarations do not clear that bar, so they get no anchor of
their own.** Measured across 51 repositories: 0.00% of Go functions contain a
named nested declaration (structurally impossible — the grammar permits only
an anonymous `func_literal` in a function body, and Go has no nested classes),
versus 1.98% of TypeScript functions (6.32% of files), 3.36% of TSX (9.95% of
files), and 9.26% of Python (25.61% of files). TSX and Python are
repository-concentrated rather than general — one repository accounts for 55%
of the TSX hits, another for 60% of Python's — and anonymous nesting
outnumbers named nesting 6–30× everywhere but Python. None of this clears the
78%/91% bar above: not worth building rather than deferred.

**Container members are addressable, and the parse is held open.** Go's methods
are file-scope, so `A.Get` resolves without descending anywhere; TypeScript and
Python keep theirs in a class body, and without descending into it the finest
unit in a one-class-per-file module is the class — which for staging is the
same thing as naming the path.

Descending multiplies the declaration count, and both hot paths resolved every
declaration by re-parsing the whole file each time: attributing a diff does it
once per declaration per side, and synthesis walks declarations looking for the
nearest one `HEAD` also has. Measured on a 200-member class, `rgit diff` took
**0.78s** re-parsing and **0.02s** against a parse held open for the file's
lifetime — a ~39× difference on one file, which is why `resolve.File` exists
rather than the one-shot `Resolve` alone.

**`@imports` node shape differs by language.** Go exposes a single
`import_declaration` block; TypeScript and Python emit a separate
`import_statement` per import. The pseudo-anchor must span a contiguous run of
nodes — a Go-only implementation would silently stage just the first import in
a TS file.

**Go's struct fields and interface methods are addressable one level in, the
same as a TS/Python class's methods.** Measured against a compiled parse tree:
a `type_spec`'s `"type"` field holds `struct_type` or `interface_type` directly;
a `field_declaration`'s `"name"` field is itself multiple (`A, B int` is one
node sharing a type between two names), while a `method_elem` carries exactly
one. A shared-name field line and an embedded/anonymous field are left
unaddressable rather than resolved to a byte extent that silently drags a
sibling name's text along with it. TypeScript's destructuring declarators
(`const {a, b} = obj`, `const [x, y] = arr`) are unaddressable by the same
reasoning — the binding names share one pattern node — documented alongside
the rest of TypeScript's addressable and unaddressable shapes in
[`../docs/ANCHORS.md`](../docs/ANCHORS.md#qualification).

**TypeScript's `declarationFor` reaches every declaration kind the grammar
names, not a subset that falls through to `(unanchorable)`** — including
`enum_declaration`, `abstract_class_declaration`,
`generator_function_declaration`, `variable_declaration` (`var`, the same
declarator shape as `let`/`const`'s `lexical_declaration` under a different
grammar node), and the namespace forms `internal_module`/`module`, all
addressable by bare name; the two class kinds and namespaces are also descended
into for container-qualified members. One shape measured, not assumed: a bare (non-`export`ed) top-level
`namespace N {}` parses as an `expression_statement` wrapping the
`internal_module`, not the `internal_module` directly — `export namespace N {}`
wraps it in `export_statement` instead, the same shape every other exported
declaration already uses. `export default function () {}`'s wrapped node is a
nameless `function_expression` (`export_statement`'s `"value"` field, not
`"declaration"`) with no name field to read; it stays unaddressable rather than
invent a spelling that could someday collide with a real identifier.

**Shell's case is weaker than Go/TypeScript/Python's, and this record says so
rather than implying parity.** Shell appears in roughly 45% of surveyed
repositories — more than any single one of those three — but fewer than half
of surveyed shell *lines* sit inside a function, and most shell files define
none at all: a typical script is a flat sequence of top-level commands, not a
library of callable units. That is well under the 91%-of-added-lines-inside-a-
symbol-body figure that justified Go/TS/Python. The value shell staging
delivers is real but concentrated in library-style scripts (`lib.sh`,
`functions.sh`) that define several functions each, not spread evenly across
every `.sh` file the way that figure was.

`function_definition` covers both `foo() {}` and `function foo {}` — one
grammar node for both surface forms, measured against a compiled parse tree
built from a fixture using each spelling. `variable_assignment`'s `"name"`
field is `variable_name` for a bare `X=1` (addressable) or `subscript` for an
indexed `arr[0]=1` (left unaddressable, the same reasoning as Go's shared-name
field line and Python's subscripted-target skip). There is no shebang node:
`#!/usr/bin/env bash` parses as an ordinary `comment`, so `HeaderKinds` is
`["comment"]`, identical to Python, with nothing new to specify. `heredoc_body`
and `heredoc_content` are real, distinct nodes — measured directly by parsing a
heredoc whose body text looks like a function definition and confirming it
never surfaces as a sibling `function_definition` of `program`, so a false
positive there is structurally impossible rather than merely unobserved.
Shell's function namespace is flat (no classes, no modules to nest under), so
`Container` is always empty and a redefined function disambiguates with the
existing `#N` ordinal, the same as two same-named Go package-level functions
would.

`@imports` needed the `ImportMatcher` seam (`internal/resolve/lang.go`) before
shell could describe one at all: `source f.sh` and `. f.sh` both parse as a
plain `command` node, the same kind used by every other command in the
script, distinguished only by the text of its own `command_name` field.
`ImportKinds` has no way to express "a command node whose name is exactly
`source` or `.`" — returning `"command"` would make `@imports` swallow nearly
the whole script. `ImportMatcher.IsImport` is consulted first when a Language
implements it; Go, TypeScript and Python do not, so they take the plain
`ImportKinds` path, verified byte-for-byte by their own pseudo-anchor tests.

Deliberately excluded: `.zsh`. tree-sitter-bash is a POSIX/Bash grammar, not a
zsh grammar, and zsh-only syntax produces `ERROR` nodes under it — the same
reason TSX and TypeScript stay two separate grammars rather than one stretched
to cover both (above). Shebang-sniffing an extensionless script (`#!/bin/sh`
with no `.sh` suffix) is deferred, not solved: `ForExtension` is keyed on file
extension alone, and changing that is a registry-contract change every grammar
shares, not a shell-specific one.

**YAML earns its place on measured demand, not popularity: 49% of the 51
surveyed repositories, second only to Markdown.** The unit that matters is one
CI job, one service in a compose file, one section of config — a
container-qualified key path
(`ci.yml:jobs.build`), not a whole-file grammar the way Shell's function
namespace is flat.

**Node kinds were measured against tree-sitter-yaml v0.7.2's own
`src/node-types.json` and a compiled parse tree, not assumed.** `stream` is the
root, holding one or more `document` children; a `block_mapping_pair` carries
`"key"` and `"value"` fields, each typed `block_node | flow_node`. The ordinary
key is a `flow_node` wrapping exactly one `plain_scalar` (or a quoted-scalar
kind for `'...'`/`"..."`); a value nests further only through
`block_node → block_mapping`, which is why `lang_yaml.go` only ever needs to
look one level for a `block_mapping` among a `block_node`'s own children, never
recurse to find one — an anchored mapping (`&x\n  a: 1`) parses as a
`block_node` whose named children are the `anchor` node and the `block_mapping`
node as siblings, never one wrapping the other.

**Qualification follows Markdown's nearest-ancestor-only rule, for the same
reason: `Declaration` carries one `Container` field, not a path.**
`jobs.build`'s own `runs-on` key qualifies as `build.runs-on`, never
`jobs.build.runs-on` — verified this collides exactly the way two Markdown
`## Options` headings under different parents do: two keys named `port`, each
nested under a mapping named `common`, but under different grandparents,
disambiguate as `common.port#1` / `common.port#2` rather than merging, since
the ordinal rule already counts container-qualified names generically
(`index.go`).

**Sequence items are not addressable, on the same reasoning Go's shared `A, B
int` field line and TypeScript's destructuring declarators already are:**
`block_sequence_item` has no name field to key a `Declaration` by.
`build.steps` addresses the whole list; no single step does. Flow-style values
(`{ a: 1 }`, `[1, 2]`) are leaves too, at any depth — there is no measured
demand for descending into one in the CI/compose files this grammar targets,
and "flow style is never descended into" is one rule rather than a second
recursion maintained in parallel with the block-style one. A multi-document
stream (more than one `---`-separated `document` under `stream`) has nothing
addressable by key at all, rather than inventing a document-index qualifier
nothing else in this resolver has syntax for.

**A real defect surfaced only once fixture-tested against a realistic
multi-job workflow, not from reading the grammar's docs: tree-sitter-yaml's
own external scanner can graft a comment onto the wrong node's trailing
edge.** A comment sitting between the end of a nested job and the next, more
shallowly indented one does not attach as that job's leading trivia the way
every other grammar's comment reliably does — it becomes the trailing child of
whatever block was still structurally open when the scanner consumed the
comment token, regardless of the comment's own written column, because
dedent-token emission depends on the next *real* line, which the scanner has
not looked ahead to yet when it emits the comment. Left alone, a
container-qualified extent ending right before such a comment would silently
absorb content written to describe its successor. `extent.go`'s
`trailingCommentTrimmer` seam (implemented only by `lang_yaml.go`) walks a
declaration's own "last named child" spine and excludes any comment run found
there, along with the whitespace before it — deliberately in the safe
direction: it can end a single-key anchor one comment short of the raw parse
(still reachable via `@toplevel` or the whole file), never graft one key's
edit onto its neighbour's extent. Go, TypeScript, Python, Markdown, and Shell
are unaffected — the seam is optional and only `lang_yaml.go` implements it.

**The byte-identical round trip is verified, not assumed.** A `block_scalar` (`|`, `>`) is one opaque leaf node whose byte
range already includes every line of its body verbatim, so staging the pair
that contains one never requires reasoning about the scalar's own internal
indentation. A `block_mapping_pair`'s own extent starts at its key's first
byte, never at the line's indentation — the same convention every other
adapter's container members already use — so `internal/synth`'s
`lineStart`/`insertionText` machinery (`classify.go`) handles a YAML member's
indentation with no YAML-specific code in that package at all.

**YAML cross-checks against `yaml-language-server`** (§ Cross-check coverage),
which reports `documentSymbol` ranges matching `declOnlyExtent` byte-for-byte,
including the doc-comment-exclusion case `gopls` is held to. One normalization
sits on top: its range for a nested container consistently extends one line
past its own last real content, through a blank line separating it from the
next sibling at the same level, where tree-sitter-yaml's own node never does.
`trimTrailingBlankLines` narrows every wired server's reported range back to
its own last non-blank line uniformly, not special-cased to YAML, so a genuine
content disagreement still fails.

**CSS is next in demand order after YAML (TODO.md), not re-surveyed
independently.** Every node shape below was measured against a compiled parse tree and cross-checked
against `tree-sitter-css` v0.25.0's own `src/node-types.json`, not assumed
from `grammar.js`.

**tree-sitter-css declares no fields at all.** Measured: `rule_set`,
`at_rule`, `media_statement`, `declaration`, `import_statement`, and every
other statement kind report an empty `"fields"` object in
`node-types.json` — unlike Go, Python, or JSON, every shape `lang_css.go`
reads is by node kind and position, the same positional discipline
`lang_yaml.go`'s field-less `block_mapping_pair` children already required.

**A selector's bare name is its own text, exactly as written.** `rule_set`'s
own children are `selectors` (holding the full, possibly comma-joined,
selector list as one node — `.a, .b` is one `selectors` node, not two) and
`block`; `.button-primary`, `#app`, `div`, and `.a, .b` all stage as their own
literal text with no decomposition. Nested rule sets inside an
`@media`/`@supports`/`@keyframes` block are not descended into and get no
anchor of their own — the same "named nested declarations do not clear the
bar" reasoning already applied above to Go/TypeScript/Python's anonymous
function literals, not worth building rather than deferred.

**At-rules are named by their full prelude, not the bare keyword.** Measured
across every top-level statement kind this grammar defines
(`media_statement`, `supports_statement`, `keyframes_statement`, generic
`at_rule`, `charset_statement`, `namespace_statement`, `scope_statement`): a
bare keyword collides on every at-rule of the same kind in a file with more
than one — two `@media` blocks are the ordinary case, not a corner — where
the prelude (`@media (max-width: 600px)`) is what actually distinguishes
them. The name is the statement's own text from its start up to whichever
comes first: its body's own start byte (`block` for four of the seven kinds
above, measured as each one's own last named child; `keyframe_block_list` for
`keyframes_statement`, a distinctly-named body the same rule still catches)
or the statement's own end byte for the three body-less kinds
(`charset_statement`, `import_statement`, `namespace_statement`, none of
which ever have a block, measured), trimmed of trailing whitespace and the
`;` a body-less statement ends with. A generic at-rule with no prelude at all
(`@font-face { ... }`) degrades cleanly to the bare keyword, which is exactly
what a caller would expect to type for it.

**`@import` is a real, unambiguous node kind, so `@imports` is meaningful
here** — unlike shell's `source`, which shares its node kind with every other
command, `import_statement` needs no `ImportMatcher` seam. This also means an
`@import` is reachable only through `@imports`, never as a bare anchor of its
own: `import_statement` is deliberately excluded from `Declarations()`, the
same exclusion Go's `import_declaration` already gets, and for the same
reason — `toplevelExtent`'s own contract is that a `Language`'s
`Declarations` never returns entries for import material, or `@header`/
`@imports`/`@toplevel` would claim overlapping bytes. `@toplevel`'s
first-through-last-declaration formula additionally assumes imports are
contiguous ahead of every declaration, the same assumption Go/TypeScript/
Python's own grammars enforce structurally; CSS's grammar does not enforce
`@import`'s position, so a caller who writes one after other rules (invalid
per the CSS spec, which requires `@import` before any other rule besides
`@charset`, but not rejected by this grammar) would see it fall inside
`@toplevel`'s span rather than being excluded — left alone, since standard
tooling like `stylelint` already enforces the spec-conformant position
upstream of `rgit`.

**JSON and TOML follow CSS in demand order (TODO.md), not re-surveyed
independently.** Both reuse the existing
`Container`/`Bare` machinery unchanged — `Bare` is the leaf key, `Container`
is the immediate parent, and `index.go`'s `containerQualified` does the
`Container + "." + Bare` join with no new naming code — but the two grammars
told two different comment stories, measured separately rather than assumed
symmetric.

**JSON has no comment syntax at all.** Measured directly against
`tree-sitter-json`'s own `node-types.json`: no "comment" node kind is
declared anywhere in the grammar. `IsComment` is unconditionally `false`,
and `HeaderKinds`/`ImportKinds` both return `nil` for the same underlying
reason — there is nothing a leading comment run or an import statement could
ever be, not merely nothing observed in a fixture.

**JSON's grammar declares real fields, unusually among this resolver's
adapters.** Measured: a `"pair"` node's `"key"` and `"value"` children are
each reachable by `ChildByFieldName`, unlike CSS, TOML, or YAML's
field-less, positional grammars. The key field is always a `"string"` node
wrapping a single `"string_content"` child holding the unquoted text
directly (`"name"` parses as `string` → `string_content` spanning exactly
`name`) — no manual quote-stripping is needed the way YAML's quoted keys
require, an artifact of JSON's stricter, simpler string grammar. An empty
string key (`""`) has no `string_content` child at all and is left
unaddressable rather than resolved to an empty `Bare`.

**A JSON document whose root is not an object has nothing to address.** A
bare top-level array or scalar are both legal JSON; `Declarations` returns
nil for either, the same refusal `lang_yaml.go` gives a document with no
top-level mapping. An array value at any depth is a leaf, never descended
into — no measured demand for per-index addressing, and the JSON files
`rgit` actually runs against in practice — `package.json`, tsconfig,
lockfiles — mostly want whole-path staging regardless (TODO.md's own
caveat, carried into `docs/ANCHORS.md`); this adapter earns its keep on the
config-file case where one nested key is the unit that changes, not by
making every JSON file's full breadth addressable.

**TOML's grammar declares no fields at all, the same as CSS.** Measured
against `tree-sitter-toml`'s own `node-types.json`: `"document"`, `"pair"`,
`"table"`, and `"table_array_element"` all report an empty `"fields"`
object — every shape `lang_toml.go` reads is by node kind and position.

**A `[table]` header and a document's own top-level pairs are siblings, not
nested by bracket path.** Measured: `document`'s only allowed children are
`pair`, `table`, and `table_array_element` — a second header sharing a
dotted prefix (`[server.tls]` after `[server]`) is its own separate
top-level `table` node, not nested inside the first. `Container` for a
table's members is therefore the header's own written text, taken verbatim
(`"server.tls"` for a `dotted_key` header, `"server"` for a `bare_key` one)
— the grammar already hands over exactly the dotted parent path, with no
segment-joining code needed.
A `table`/`table_array_element` is itself reported as its own Declaration
(Bare = its header text, Container empty, Node = the whole table) in
addition to its members, the same "the container is also addressable by
its own name" convention `lang_yaml.go`'s mapping keys already follow.

**A dotted pair key (`a.b = 1`, legal directly at the document root or
inside a table body) is deliberately not decomposed.** Its `Bare` is read
as the full dotted spelling, one string, rather than split into its own
container/leaf pair — the same simplification `lang_css.go`'s comma-joined
`.a, .b` selector list makes: one predictable rule, not a second
qualification scheme layered in parallel with the `[table]`-header one, for
a shape with no measured demand for finer addressing.

**A `table`/`table_array_element` node's own extent absorbs the blank line
before the next section header.** Measured directly: a `table` node's own
`EndByte()` reaches the byte immediately before the next `[table]`/
`[[array]]` header begins, not the end of its last member's own line —
staging a whole table by name therefore carries that trailing blank line
along. Left as the grammar's own honest boundary rather than trimmed: there
is no neighbor it could belong to instead, the table is genuinely the last
thing before that gap.

**Two `[[servers]]` elements sharing one header spelling collide the same
way two same-named Go functions do, both for the table's own anchor and its
members'.** No TOML-specific disambiguation exists or was built — the
existing `#N` ordinal machinery (`index.go`) already counts
`containerQualified` names project-wide, so `servers#1`/`servers#2` and
`servers.name#1`/`servers.name#2` fall out unchanged, correctly interleaved
in source order since both the table's own Declaration and its members are
appended to the same list in parse order. This is a real, documented sharp
edge (`docs/ANCHORS.md`): the ordinal says nothing about array position, so
staging "the second `servers` entry" by ordinal requires already knowing
source order.

**Pseudo-anchors shadow a same-named at-rule unconditionally.** A generic
at-rule can be spelled anything, including `@header` — CSS's own at-rule
keyword grammar has no reserved-word list — so `auth.css:@header` genuinely
collides with the pseudo-anchor `@header`. Verified directly against
`resolver.go`: `File.Resolve` calls `isPseudoAnchor` before it ever consults
the symbol index, so the pseudo-anchor always wins; the colliding at-rule
still appears in `DeclOrder`'s source-order listing under its own qualified
name, but no spelling of `Resolve` can ever reach it. Documented as a sharp
edge in `docs/ANCHORS.md` rather than worked around, since resolving it would
mean either renaming the caller's pseudo-anchors (a breaking change to every
other language) or teaching the resolver to fall back from a failed
pseudo-anchor lookup to the symbol index (a general behaviour change, not a
CSS-specific fix).

**SQL is last in demand order (TODO.md), and the only grammar this repo
generates its own C for rather than consuming a published binding.**
`github.com/DerekStride/tree-sitter-sql` is the only SQL grammar with Go
bindings at all, but its published module cannot compile as fetched: `src/
parser.c` is generated and gitignored, absent from every tag v0.1.0–v0.3.11,
so its own `bindings/go`'s `#include "../../src/parser.c"` fails — verified
directly against every one of those tags' own file trees, the same check
that catches markdown's `v0.5.2` Go-bindings regression (§ Dependencies).
The module does ship `grammar.js` and `tree-sitter.json` at its
root, and `src/scanner.c` — everything `tree-sitter generate` needs except
the one file it produces.

**Generation at build time, not vendoring — a genuine divergence from how
every other grammar in this table is consumed.** Vendoring
`parser.c` would mean carrying a 17.4 MB, 674,655-line generated file (`wc
-l`, measured against this exact module/version) in the repository, entirely
unreviewable by a human, and re-vendored by hand on every upstream grammar
bump — an unversioned copy exactly as unwelcome as the one `AGENTS.md`
already rejects `go-git` for creating. Generating it from the module's own
`grammar.js` at build/install time instead means the repository never holds
that file at all; `cmd/rgit-install`'s `generateSQLParser` reproduces it on
demand, and `.gitignore` refuses it a second time in case one ever lands on
disk. The cost is a `tree-sitter` CLI dependency and ~27s of generation time
per fresh build (measured: `tree-sitter generate` alone, warm module cache,
this machine) — paid once per checkout, not per invocation, and never paid
at all by a plain `go build ./...`, which the `rgit_sql` tag keeps this
entirely out of.

**Two build-time facts were measured, not assumed, because getting either
wrong fails silently.** First: `tree-sitter.json` must be copied alongside
`grammar.js` before generating, or the CLI silently emits ABI 14 instead of
ABI 15 (a warning, not an error) — `runSQLGeneration` copies both files into
its scratch directory for exactly this reason. Second: the generated C must
land in a subdirectory of the package that consumes it
(`internal/resolve/sqlgrammar/csrc/`), never beside the `.go` file directly
— measured directly: a `.c` file placed in the package directory itself gets
compiled once by cgo's own file-globbing and a second time by the binding's
own `#include`, producing `multiple definition of 'tree_sitter_sql'` at link
time. Neither defect announces itself as a generation failure; both were
caught by actually linking the result, not by reading `tree-sitter
generate`'s own success/failure exit code.

**No Node.js install is needed despite `grammar.js` being JavaScript** —
measured by running `tree-sitter generate` against this exact module with
every `node`/`nodejs` binary removed from `PATH`; the CLI (0.26.9 here)
evaluates grammar files with its own embedded JS engine. `cmd/rgit-install`'s
prerequisite checks accordingly probe for the `tree-sitter` CLI alone, not a
JS runtime.

**The binding imports neither the grammar module nor its own `bindings/go`
package — deliberately, since the latter is exactly the one that cannot
compile.** `internal/resolve/sqlgrammar`'s binding imports only `"C"` and
`"unsafe"`; the generated C it `#include`s is reached through a relative
path, not a Go import. One consequence threads through this record and the
installer: nothing in this repository's build graph ever names
`github.com/DerekStride/tree-sitter-sql` in an import statement, so `go mod
tidy` has no dependency edge to hold a `go.mod` requirement open with and
would drop a bare `require` line the moment it ran (verified: `go mod tidy`
leaves `go.mod`/`go.sum` byte-for-byte unchanged after this grammar's own
commit). The module is instead resolved by explicit `module@version` —
`go mod download` and `go list -m` both accept that form as an ad-hoc query
against the proxy/cache with no `go.mod` entry at all, verified directly —
which is also why `cmd/rgit-install`'s own package-discovery for the SQL
adapter had to change: the seam it replaced searched `go list`'s `Imports`
field for a `tree-sitter-sql` substring, a check this binding was never
going to satisfy. It now matches by package name (`sqlgrammar`) and the
presence of at least one `CgoFiles` entry, both readable from `go list
-tags rgit_sql -json ./...` even before generation has ever run — verified:
cgo's own `#include` line is a C-preprocessor directive inside a Go source
comment, invisible to `go list`, which only needs the `.go` file itself to
parse.

**Every node shape `lang_sql.go` reads was measured against a compiled
parse tree**, the same discipline every other adapter here follows. The
root node kind is `program`; its named children are `statement` wrappers —
one per statement, each with exactly one named child — not the inner
`create_table`/`create_view`/etc. node directly, measured across every
statement kind this adapter addresses and every one it does not. A `;`
terminator is its own unnamed sibling of `statement` under `program`, not a
child of `statement` itself — measured directly by walking `program`'s
children including unnamed ones — so a symbol's extent never includes it,
the same "the grammar's own honest boundary" reasoning already applied to
TOML's trailing-blank-line case above.

Five of the six addressed statement kinds (`CREATE TABLE`/`VIEW`/
`FUNCTION`/`TRIGGER`/`TYPE`) name themselves through an `object_reference`
child holding a `"name"` field, and — only when the source wrote one — a
`"schema"` field, read as `Container` the same one-level-qualification way a
Go receiver or a TOML table header already is. That child is positional
(tree-sitter-sql declares no field naming it on the parent statement), found
by scanning for the first `object_reference`-kind child — measured to
always be the statement's own name even on `CREATE TRIGGER`, whose statement
carries three `object_reference` children in total (its own name, the table
it fires on, the function it calls). `CREATE INDEX` is the exception: its
own name is a field directly on `create_index` itself, confusingly named
`"column"` — measured, and distinct from the same-named `"column"` field
each entry inside its own `index_fields` carries for the columns actually
being indexed; an anonymous index (`CREATE INDEX ON t (c)`, legal SQL) has
no `"column"` field on `create_index` at all and is left unaddressable
rather than guessing at the name the database would assign. `CREATE DOMAIN`
does not parse under this grammar version at all — measured: it produces an
`ERROR` node — so it is not a candidate regardless of demand.

A line comment (`-- ...`) and a block comment (`/* ... */`) are two
distinct node kinds, `"comment"` and `"marginalia"` respectively — measured
by parsing one of each — so `IsComment` and `HeaderKinds` both name both
kinds, or a file leading with a block comment would silently get no
`@header`. Both sit as `program`-level siblings of `statement` nodes, not
nested inside one, so the core resolver's ordinary `docStart` sibling-walk
attributes a leading doc comment to a `CREATE TABLE` the same way it would
in any other language, with no SQL-specific extension. `ImportKinds`
returns `nil`: this grammar has no include/import-shaped statement of any
kind, the same degraded-but-not-an-error answer TOML, JSON, and Markdown
already give.

SQL is the most expensive grammar here by binary size (§ Binary size),
consistent with its being by far the largest generated parser (674,655 lines
of C against bash's much smaller hand-maintained scanner). `go tool nm` on an
unstripped `-tags rgit_sql` build shows exactly one grammar's worth of
`tree_sitter_sql*` symbols — the entry point and its external scanner's five
functions, plus the cgo glue — no second, unreferenced grammar riding along,
the same check every other grammar passes. That cost is paid only by a caller
who opts into `-tags rgit_sql`; the default, untagged binary is unaffected.

Measured **parse time**, warmed and averaged over 200 parses, in-process
(no process-spawn cost, the same reason tree-sitter is the primary resolver
at all): a single `CREATE TABLE users (id INT);` statement parses in
**5.4µs**; a synthetic 50-statement, 4040-byte schema file (`CREATE TABLE
t0`..`t49`, three columns each) parses in **513µs**, both on this machine.
Neither figure is close to mattering next to the ~27s one-time generation
cost or even the ~7.2s cgo compile of the generated C (`go build -tags
rgit_sql ./internal/resolve/sqlgrammar/...`, this machine) — both paid once
per checkout, not per `rgit` invocation.

**No language-server cross-check for SQL:** there is no single dominant SQL
language server the way `gopls`/`vtsls`/`pyright` are for their languages, and
no measured need strong enough to justify probing for a fifth stdio process
sight unseen. `.sql` resolves in `[ts-only]` mode (§ Cross-check coverage).

### Cross-check coverage

Markdown, YAML, CSS, JSON, TOML, and SQL were each checked for a real
candidate server on the same terms as the gopls/vtsls/pyright/bash-language-
server table above: is one installed, what does it speak, and — the
load-bearing question — do its ranges land on the same declaration-only basis
`internal/resolve`'s `declOnlyExtent` produces (`node.StartByte()`..
`node.EndByte()`, no doc-comment prefix), so a real disagreement means a real
extent bug rather than a transport artifact.

Every measurement below is against a real, installed binary —
`yaml-language-server`, `vscode-json-language-server`,
`vscode-css-language-server` (all three via the `vscode-langservers-
extracted`/bun toolchain), `marksman`, `vscode-markdown-language-server`, and
a `taplo` built with its `lsp` feature enabled. Nothing here is inferred from
a server's own docs.

| Grammar | Server | Transport | Cold dial+handshake | Cold `documentSymbol` | Warm `documentSymbol` | Ranges vs `declOnlyExtent` | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- |
| YAML | `yaml-language-server` | stdio | 145ms | 5.1ms | 0.4ms | Exact match, every case measured | **Wired** |
| JSON | `vscode-json-language-server` | stdio | 79ms | 2.4ms | 0.3ms | Exact match | **Wired** |
| CSS | `vscode-css-language-server` | stdio | 104ms | 4.6ms | 0.5ms | Exact match | **Wired** |
| Markdown | `marksman` | stdio | 570ms | 78–115ms | 2.5–4.1ms | Exact match | **Wired** |
| Markdown | `vscode-markdown-language-server` | stdio | — | — | — | Crashes on startup (measured) | Not wired |
| TOML | `taplo` 0.10.0 | stdio | 3.9ms | 0.5–1.0ms | 0.2–0.4ms | Real disagreement on nested tables (measured) | Not wired |
| SQL | none maintained | — | — | — | — | `sqlfluff` installed, no LSP surface | Not wired |

Method for the four that passed: a fixture per grammar exercising a nested
container (so both a leaf declaration and a declaration whose own extent
encloses another get compared) and, where the grammar has comment syntax, a
leading comment with **no** blank line before the symbol — the doc-comment-
exclusion case gopls is held to (`ValidateToken` above). Each grammar's own
tree was parsed directly with the same `go-tree-sitter` + grammar-binding
packages `internal/resolve` imports, at the same module versions, and each
`Declaration.Node`'s `StartByte()`/`EndByte()` was converted to a 0-based line
the same way `crosscheck.go`'s own `lineOf` does. Comparing against a hand
count instead is not reliable at this precision.

**YAML: exact match on every case, including the container that encloses
another.** `yaml-language-server` on

```yaml
# comment
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
```

reported `jobs` L1..L6, `build` L2..L6 (its own value nests `runs-on` and
`steps`), `runs-on` L3..L3, `steps` L4..L6 — identical to
`declOnlyExtent` on tree-sitter-yaml's own parse of the same fixture in
every case. The leading `# comment` (no blank line before `jobs:`) was
correctly excluded from `jobs`'s own range, the same doc-comment-exclusion
behaviour already verified for gopls. `steps`'s single list item surfaced
as its own numbered symbol (`"0"`) with `run` nested under it — extra
symbols `rgit` never queries, since sequence items are not addressable
(`docs/ANCHORS.md`), and harmless: `matchLSPSymbol` only ever looks up
names `rgit`'s own resolver produced.

**One convention difference is real and normalized: a nested key whose
subtree is directly followed by a blank-line-separated sibling at the same
level.** `cmd/rgit/index_test.go`'s `TestStage_YAMLNestedKeyByteIdentical
RoundTrip` stages `jobs.build` in a file where `build`'s own steps end with a
block scalar (`run: |`) and a blank line separates `build` from its sibling
`test`. Measured directly, both by parsing that fixture with the same
`go-tree-sitter` + `tree-sitter-yaml` packages used above and by querying
`yaml-language-server` for it: `build`'s own `block_mapping_pair` node ends at
line 8 (0-based), the last real line of its own content, where the server
reports line 9 — the blank separator line before `test:` begins. Unnormalized,
that is a hard fail (`tree-sitter L4..L9, language-server L4..L10`, 1-based) on
a correct extent.

**It is not block-scalar-specific — the same fixture with plain scalars in
place of the block scalar reproduces the identical one-line-past-content
difference**, `build` at tree-sitter line 7 vs. server line 8. That rules out
"a defensible different idea of where a block scalar ends" in favour of a
general, mechanical convention difference: `yaml-language-server`'s own range
for a nested container consistently extends through **one** trailing blank
line separating it from a following sibling at the same level; tree-sitter-
yaml's own node never does, stopping at its own last real content line. This
is the same *shape* of difference doc-comment stripping already normalizes
(one side includes a well-defined, content-free byte span the other does not)
— not the same *category* as taplo's dotted-table mismatch, which claims a
neighbour's real, addressable content rather than blank padding.

**The normalization lives in `internal/lsp`, never in `internal/resolve`'s
extents, which stay authoritative.** `client.go`'s `trimTrailingBlankLines`
pulls every symbol's own `EndLine` back past wholly-blank trailing lines
within its own range, applied uniformly to every symbol from every wired
server inside `DocumentSymbols` itself, not special-cased to YAML. It can only
ever narrow a reported range toward its own `StartLine`, never grow one, and
it stops the instant it reaches a non-blank line — a genuine content-level
disagreement (a server claiming real neighbouring content) is untouched and
still fails. One case is guarded explicitly: the very last line `bytes.Split`
produces is never trimmed, because a symbol with no following sibling was
measured (the `jobs`-only fixture above) extending through the file's own
trailing newline all the way to that final element on **both** sides.
Trimming it would undo an already-correct match and manufacture a new
mismatch on every last declaration in a file.

**JSON, CSS, and Markdown are checked against this exact hazard, not assumed
safe by association.** A JSON/CSS fixture with a blank-line-
separated sibling container (`{"server": {...}},\n\n"other": 1}` /
`.a {...}\n\n.b {...}`) produced no mismatch in either grammar, because
both are brace-delimited: a container's own node closes on its own `}`,
strictly before any blank line that might follow it, so the ambiguity
YAML's indentation-based nesting creates never arises structurally. A
Markdown fixture with two sibling `##` sections separated by a blank line
(`## Setup` / blank / body / blank / `## Next`) showed `marksman`
reporting `Setup`'s own range as extending through that same blank
separator — but tree-sitter-markdown's own `section` node was measured
doing exactly the same thing (a section's own `EndByte()` reaches the next
section's own start byte, blank separator included), so the two already
agreed before `trimTrailingBlankLines` ever ran, and the normalization was
a no-op for Markdown, not a rescue.

**JSON and CSS: exact match, and CSS's own doc-comment case passed too.**
`vscode-json-language-server` on a nested `{"server": {"host": ..., "port":
...}}` fixture reported `server` L1..L4, `host` L2..L2, `port` L3..L3,
matching `declOnlyExtent` exactly (JSON has no comment syntax, so there is
no doc-comment case to check). `vscode-css-language-server` on

```css
/* comment */
.button {
  color: red;
}

@media (max-width: 600px) {
  .button { color: blue; }
}
```

reported `.button` L1..L3 (the leading, blank-line-less `/* comment */`
correctly excluded) and `@media (max-width: 600px)` L5..L7, both exact
matches; the nested `.button` inside the `@media` block is not addressable
by `rgit` at all (`docs/ANCHORS.md`), so its own reported range was not
compared against anything.

**Markdown: `marksman` matches exactly, because tree-sitter-markdown's own
`section` nodes are already hierarchical the same way `marksman`'s outline
is.** On

```markdown
# Title

## Setup

Some text.

### Options

More text.
```

`marksman` reported `Title` L0..L9, `Setup` L2..L9, `Options` L6..L9 —
exact matches, each parent's range correctly enclosing its subsections, the
same nesting `docs/ANCHORS.md` already documents ("a Markdown heading's
extent is the whole section it opens ... subsections included"). This is
why Markdown's own cross-check needed no normalization the way TOML's does
below: tree-sitter's own node already nests the way the server's own range
does, rather than modelling siblings the server reports as parent/child.

**`vscode-markdown-language-server` crashes on startup on this machine —
measured, not assumed.** `vscode-markdown-language-server --stdio` exits
immediately with `SyntaxError: The requested module 'vscode-uri' does not
provide an export named 'default'`, an ESM/CJS interop break between its
own bundled `vscode-markdown-languageservice` dependency and the installed
`vscode-uri` version under Node.js v26.5.0. Not wired — not because
Markdown lacks a compatible server (`marksman` fills that role), but
because this specific binary does not run here at all.

**TOML: `taplo` completes the LSP handshake, but its ranges genuinely
disagree with `declOnlyExtent` on the ordinary case of a nested table — the
exact false-positive risk this comparison exists to catch, not a
normalization gap.** On

```toml
# leading comment for server table
[server]
host = "localhost"
port = 8080

[server.tls]
enabled = true
cert = "a.pem"
```

`taplo` reports `server` as L1..L7 — the *entire* file from `[server]`
through `cert`'s own line — because `taplo` understands TOML's dotted-table
semantics and treats `[server.tls]` as a logical child of `server`.
tree-sitter-toml does not: measured directly against a compiled parse tree,
`document`'s only allowed children are `pair`/`table`/`table_array_element`
as flat siblings (§ Grammar scope), so `server`'s own node spans only L1..L5 — the header through the blank
line before the next header begins, not through the next table's own
content. `taplo`'s `server` range is objectively wider than the anchor
`rgit` would ever stage for it. Cross-checking `server` against `taplo`
would hard-fail (exit 6) on a correct, unmodified extent, on every TOML file
with a dotted-nested table — the ordinary organizing pattern the format
exists to support, not a corner case.

A second, independent disagreement lands on `server.tls` itself (the *last*
declaration in the file): `taplo` reports it as L5..L7, ending at the last
real character of `cert = "a.pem"`; tree-sitter's own node reaches L5..L8,
one line further, because `table`'s `EndByte()` measured as reaching all the
way to the file's own trailing newline when nothing follows it (the same
"table absorbs the trailing blank line before the next header" behaviour in
§ Grammar scope — with no next header, it absorbs through EOF instead). Two
independent, measured mismatches, not one; TOML stays `[ts-only]`.

**Answering `workspace/configuration` is load-bearing, and `taplo` is the
server that proves it.** It sends that server-initiated request immediately
after `initialized` and waits on it. Against
`protocol.UnimplementedClient{}`, whose own `Configuration` method returns an
error, `taplo` reads the error as "no configuration available" and silently
excludes every document from then on — `DocumentSymbols` returns
successfully, with zero symbols and no error at all, and a raw-protocol probe
is the only thing that surfaces the `"this document has been excluded"`
diagnostic behind it. `client.go`'s `configClient` answers with one empty
settings object per requested item: `rgit` has no configuration to report, so
an empty object is not a guess, just the minimum reply a server that insists
on an answer needs to stop excluding the file it was just told to open.
`gopls`/`vtsls`/`pyright`/`bash-language-server` never send this request, so
the reply is inert for all four.

**SQL: nothing installed speaks `documentSymbol`.** `sqlfluff` is the only
SQL tool present; its own `--help` lists `dialects`, `fix`, `format`, `lint`,
`parse`, `render`, `rules`, `version` and no `lsp` subcommand, and `pip show
sqlfluff-lsp` reports no such package. `sqls` and `sql-language-server` are
absent from every location checked. `.sql` stays `[ts-only]`.

**Net: YAML, JSON, CSS, and Markdown (via `marksman`) are wired; TOML and
SQL are not, on measured range disagreement and measured unavailability
respectively, not on a documentation assumption either way.** 9 of 11
grammars cross-check against a live server (Go, TypeScript, TSX, Python,
Shell, YAML, JSON, CSS, Markdown), leaving TOML and SQL permanently
`[ts-only]`.

## Argument grammar

Symbol anchors need no flag because **all git pathspec magic is leading-colon**
(`:(exclude)`, `:(glob)`, `:/`, and `:(attr:text)` — whose interior colon still
sits inside a leading-colon form). An interior colon is therefore unclaimed.

Two complications, both measured: colons are legal in filenames (git tracked
`src/notes:draft.md` fine), and `rev:path` is valid `git diff` syntax
(`git diff HEAD~1:f.go HEAD:f.go` works). The precedence order in
[`../docs/USAGE.md`](../docs/USAGE.md#argument-shape) resolves both without any
escape syntax — an existing-path check beats a `\:` escape, and rule 3 keeps
git's blob-reference form working.

## CLI handling

`spf13/pflag` with hand-rolled subcommand dispatch. Five options were built with
rgit's real flag surface and measured:

| Option | Binary | vs base | `--help` tokens | Deps | git's exit 129 | Interspersed |
| --- | --- | --- | --- | --- | --- | --- |
| stdlib `flag` | 1644 KB | +0% | 85 | 0 | free | **no** |
| **pflag** | **1844 KB** | **+12%** | **83** | **1** | **free** | **yes** |
| ff/ffcli | 1812 KB | +10% | 97 | 1 | free | **no** |
| Cobra | 2500 KB | +52% | 110 | 2 | needs override | yes |
| Kong | 3656 KB | +122% | 133 | 1 | needs override | yes |

**Interspersed parsing decided it, and it is a correctness issue.** Git accepts
flags after positionals — `git commit f.txt -m "msg"` was measured working.
stdlib `flag` and ff/ffcli stop parsing at the first positional, so
`rgit commit auth.go:Foo -m msg` does not error: it silently yields *zero*
messages and three targets. An LLM writing git-shaped commands produces that
ordering routinely.

pflag is the only option with both interspersed parsing and free control of the
exit code (Cobra hardcodes 1, Kong exits 80). A full framework stays rejected:
`rgit` must own its positional precedence regardless, and two subcommands sits
below the bar set in `claude-format-hooks`. pflag is a flag parser, not a
framework — one dependency bought for a measured, specific defect.

**Shell completion is hand-written, and a framework would not have shortened
it.** The genuinely useful completion — symbols after `auth.go:` — is a dynamic
function shelling out to `rgit diff --porcelain`, which every framework leaves
hand-written anyway. `rgit completion bash|zsh` ships as exactly that: a script
whose symbol completion parses `--porcelain` output, with no framework and no
new dependency. The porcelain format is a machine contract with a shipped
in-repo consumer as a result ([`docs/CODES.md`](../docs/CODES.md)).

## Dependencies

Binary size and dependency count are not constraints; each entry earns its place
against a specific, measured need. All verified against the module proxy and
built.

| Dependency | Version | Earns its place by |
| --- | --- | --- |
| `github.com/spf13/pflag` | v1.0.10 | Interspersed flag parsing |
| `github.com/tree-sitter/go-tree-sitter` | v0.25.0 | Core extent resolution |
| `tree-sitter-go` / `-typescript` / `-python` | v0.25.0 / v0.23.2 / v0.25.0 | The three grammars the demand survey ranked first; import path is `<module>/bindings/go` |
| `go.lsp.dev/protocol` + `jsonrpc2` | v1.0.1 | Typed LSP 3.18; models `DocumentSymbolResult` as a sealed union over `SymbolInformationSlice \| DocumentSymbolSlice` — the case a hand-rolled client decodes wrongly |
| `go.lsp.dev/uri` | v1.0.1 | `uri.File(path)`, the only path-to-`file://`-URI constructor in this dependency graph, for `rootURI` and `textDocument.uri` (`internal/lsp/client.go`); `protocol.URI` is a thin wrapper (`type URI uri.URI`) whose own doc defers path construction to this package rather than duplicating it. Its `Platform` handling matters directly for this repo's windows/amd64 target: drive-letter and slash conversion, not just POSIX paths |
| `github.com/aymanbagabas/go-udiff` | v0.4.1 | Per-symbol `+N/-M` counts in-process, no fork/exec per anchor |
| `golang.org/x/term` | v0.45.0 | `IsTerminal`, gating the `GIT_TERMINAL_PROMPT=0` rule |
| `github.com/go-quicktest/qt` | v1.102.0 | Tests; matches `claude-format-hooks` |
| `tree-sitter-grammars/tree-sitter-markdown` | v0.5.1, pinned | Markdown sections; import path is `<module>/bindings/go`, block grammar only — pinned because `v0.5.2` dropped Go bindings from the module entirely (below, "YAML's grammar is checked the same way"), so nothing newer is even installable |
| `github.com/tree-sitter/tree-sitter-bash` | v0.25.1 | Shell function/variable anchors; import path is `<module>/bindings/go` |
| `github.com/tree-sitter-grammars/tree-sitter-yaml` | v0.7.2 | YAML key-path anchors; import path is `<module>/bindings/go` |
| `github.com/tree-sitter/tree-sitter-css` | v0.25.0 | CSS selector/at-rule anchors; import path is `<module>/bindings/go` |
| `github.com/tree-sitter/tree-sitter-json` | v0.24.8 | JSON key-path anchors; import path is `<module>/bindings/go`; was already an indirect requirement, promoted to direct |
| `github.com/tree-sitter-grammars/tree-sitter-toml` | v0.7.0 | TOML key-path anchors; import path is `<module>/bindings/go` |
| `github.com/DerekStride/tree-sitter-sql` | v0.3.11, pinned | SQL schema/function anchors; **not** a `go.mod` requirement — resolved by explicit `module@version` at build time, since nothing imports it (§ Grammar scope, SQL) |

**`gopls` is pinned too, outside this table.** `cmd/rgit-install`'s
`-with-servers` installed it via `go install golang.org/x/tools/gopls@latest`
until this release; that float meant two checkouts of the same rgit tag could
install different `gopls` binaries months apart, purely from when each one
ran the installer — the same reproducibility argument that pins
`tree-sitter-sql` above, not a `go.mod` entry because `gopls` is a
separately-installed binary `rgit` shells out to, never imported. Pinned to
`v0.23.0`, the tagged release current at the time of this pin
(`cmd/rgit-install/servers.go`); bumping it is a deliberate edit, not
something `go get -u` or a bare `@latest` ever does silently.

**Markdown earns its place two ways, both verified against the grammar's own
`node-types.json`, not assumed.** It is the one language present in every
repository `rgit` runs against, so a symbol anchor that only ever worked in
code left the single most common file kind staged whole-file-or-nothing.
`fenced_code_block` is a real node kind — a `#` inside a ` ```bash ` fence
(this repo's own docs are full of console fences) parses as
`code_fence_content`, never `atx_heading`, so a regex heading-splitter's
false-positive is structurally impossible here rather than merely rare.
`section` nests — a section's own named children include further `section`
nodes for its subsections — so container qualification (`install.options`)
falls out of the parse tree the same way a Go struct's fields or a
TypeScript class's methods already do, instead of a hand-maintained
heading-level counter.

**Only the block grammar is used, not tree-sitter-markdown's separate inline
grammar** (emphasis, links, code spans) — headings and sections are decided
entirely at the block level, so the inline grammar buys nothing here.
**Latest available is v0.5.3, not v0.5.1** — v0.5.2 dropped the Go bindings
(`bindings/go`) from the module entirely, leaving only Node/Python/Rust/Swift;
v0.5.1 is the newest release that still ships one, measured directly against
each tag's own file tree rather than assumed from a changelog. Taking v0.5.3
would mean hand-writing a cgo binding for a grammar the upstream project no
longer supports in Go, which is a worse position than pinning one version
behind latest.

**The inline grammar's binary cost could not actually be avoided by simply
not calling it.** `bindings/go` compiles both grammars' C sources
(`markdown.go` and `markdown_inline.go`) into one Go package; there is no
second, inline-only package to skip importing. Measured with `go tool nm` on
an unstripped build: `tree_sitter_markdown_inline` and its four external-
scanner symbols are present in the final binary even though nothing in this
codebase ever calls `InlineLanguage()` — cgo objects link at the object-file
level, not per-function, so the Go linker's dead-code elimination cannot drop
an unreferenced C function sitting in the same translation unit as one that
is referenced. Avoiding that cost for real would mean vendoring the block
grammar's own C sources directly rather than depending on the upstream
module's Go bindings package — a materially bigger commitment (an unversioned
copy to track by hand, diverging from how every other grammar in this repo is
consumed) than the measured 768 KB it would save.

**Shell's grammar earns its place the same two ways markdown's did, verified
the same way.** v0.25.1 is both the latest tag on the module proxy and the
newest one that still ships `bindings/go` — checked directly against that
tag's own file tree (`bindings/go/binding.go`, package `tree_sitter_bash`,
exporting `Language()`), not assumed from the version number the way
markdown's `v0.5.2` regression showed a "latest" tag cannot be trusted to
mean "still has Go bindings."

**Unlike markdown, there is no unused second grammar bundled in.** Markdown's
`bindings/go` compiles both the block grammar and a separate, never-called
inline grammar into one Go package, so `tree_sitter_markdown_inline` and its
scanner symbols ship regardless (measured with `go tool nm`).
tree-sitter-bash has only one grammar: `bindings/go/binding.go` compiles
exactly `src/parser.c` and `src/scanner.c`, and `go tool nm` on the built
binary shows exactly one grammar's worth of `tree_sitter_bash*` symbols, no
second unreferenced set. The size this dependency adds is the bash grammar
itself, not waste alongside it — bash's own grammar is simply larger, driven
by its heredoc/expansion/quoting state machine (`scanner.c`'s external
scanner), not by anything avoidable — which is why it is the second most
expensive grammar in the size table below.

**YAML's grammar is checked the same way, on the same organisation's own
precedent for the exact failure mode that matters here.**
`tree-sitter-grammars/tree-sitter-yaml` is the same maintaining organisation as
`tree-sitter-markdown`, whose own `v0.5.2` was measured dropping Go
bindings — so every candidate tag's own `bindings/go` directory is checked
directly against the module proxy rather than assumed current from the
version number. v0.7.2 is both the latest tag and still ships one:
`bindings/go/binding.go`, package `tree_sitter_yaml`, exporting `Language()`,
with no `go.mod` of its own — it is an ordinary subpackage of the repository's
single root module (`go-tree-sitter v0.24.0` there, compatible with this
project's v0.25.0 via ordinary minimum-version selection), the same
single-root shape `tree-sitter-markdown` and `tree-sitter-bash` already use.
**No unused second grammar rides along, the same property `tree-sitter-bash`
has and `tree-sitter-markdown` does not.** `go tool nm` on an
unstripped build shows exactly `tree_sitter_yaml`, its external scanner's four
entry points (`_create`, `_destroy`, `_scan`, `_(de)serialize`), and the cgo
glue calling them — no second, uncalled grammar's symbols the way
`tree_sitter_markdown_inline` rides along unused. YAML's own grammar and
external scanner (indentation/flow-context tracking) are small, which the
size table below reflects.

**`go-git` is rejected.** It reimplements git in pure Go and provides none of
what this design delegates: hook execution, `.gitattributes` filters, git's
pathspec matching, credential and GPG prompting. Shelling out is the design, not
a shortcut — using it even for reads would create a second, subtly divergent
source of truth about repository state.

**CSS's grammar earns its place the same two ways as every other grammar in
this table, verified the same way.** v0.25.0 is both the latest tag on the
module proxy and the newest one that still ships `bindings/go` — checked
directly against that tag's own file tree (`bindings/go/binding.go`, package
`tree_sitter_css`, exporting `Language()`), the same check markdown's
`v0.5.2` regression showed is never safe to skip. `go tool nm` on an
unstripped build shows exactly one grammar's worth of `tree_sitter_css*`
symbols (`tree_sitter_css`, its external scanner's four entry points, and the
cgo glue) — no second, unreferenced grammar rides along the way
`tree_sitter_markdown_inline` does.

**JSON and TOML are each measured in isolation, not only combined**, by
building the binary with one adapter's import and grammar constructor removed
— the same `go tool nm` check as every grammar above confirms neither pulls
in an unused second grammar (JSON: exactly `tree_sitter_json` and its cgo
glue, no external scanner at all; TOML: `tree_sitter_toml`, its external
scanner's five entry points, and the cgo glue). Their combined cost tracks
the sum of the two isolated deltas (12 + 32 = 44 KB), so neither pulls in
anything the other did not already need on its own. JSON's grammar has no
external scanner at all, which is why it is the cheapest grammar here.

### Binary size

**13740 KB** stripped for the default build, against a **1644 KB**
no-dependency baseline. The grammars are the largest single contributor;
every other dependency is noise beside them. A `-tags rgit_sql` build is
**16156 KB**.

Each grammar's cost is measured with `go build -ldflags="-s -w"` and `stat`'s
byte count, against a baseline built the same way immediately beforehand with
that one dependency removed. Baselines differ by a few tens of KB across
measurements — ordinary toolchain and dependency drift, not attributable to
any grammar:

| Grammar | Measured addition | Baseline it was measured against |
| --- | --- | --- |
| Go, TypeScript, Python | — | 11236 KB with all three |
| Markdown | +768 KB, +6.8% | 11236 KB |
| Shell | +1332 KB, +11.1% | 12004 KB |
| YAML | +196 KB, +1.5% | 13360 KB |
| CSS | +128 KB, +0.9% | 13568 KB |
| JSON | +12 KB, +0.1% | 13696 KB |
| TOML | +32 KB, +0.2% | 13696 KB |
| SQL (`-tags rgit_sql`) | +2416 KB, +17.6% | 13740 KB |

Shell and SQL are the two expensive entries, both for grammar size alone
rather than anything unused riding along: bash's heredoc/expansion/quoting
state machine, and SQL's 674,655-line generated parser (§ Grammar scope).
