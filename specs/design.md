# rgit — design record

Why `rgit` is shaped the way it is, and what was measured to establish each
decision. Behaviour itself is documented in [`../docs/USAGE.md`](../docs/USAGE.md)
and [`../docs/ANCHORS.md`](../docs/ANCHORS.md); this record explains the
reasoning and holds the evidence.

## Origin

The `rethunk-git` MCP surface (v7, 24 tools) was audited against plain
`bash git`. Only two operations justified their context cost:

| Measurement | Result |
| --- | --- |
| Full schema | 8,220 tokens (cl100k) across 24 tools |
| `batch_commit` schema alone, on materialization | 696 tokens |
| Non-deferrable `CLAUDE.md` routing prose | 304 tokens/session |
| `git_status` output vs bash, 1 repo | 7.0× worse |
| `git_status` output vs bash, 3 repos | 2.6× worse |
| `git_log -5` vs bash, same information | 1.8× worse |

`batch_commit` earned its place on hunk-level staging; `git_diff_summary` was
the only tool that *reduced* context. Everything else was slower, more verbose,
or both, than the `git` already installed.

Two documented claims did not survive verification: that MCP commits landed as
"Bastion Agent" (false — ambient git config, verified across 20 commits), and
that multi-root routing was worth a tool (a `for` loop is also one call, at 2.6×
less output).

## Governing principle

**`rgit` is `git add <pathspec> && git commit` at symbol granularity.** Where
git has an opinion, `rgit` matches it exactly rather than inventing semantics.

This is load-bearing and has repeatedly overturned earlier drafts. Apply it
before adding any behaviour; a proposal to diverge needs to argue against it
explicitly. Each consequence was established by measurement, not assertion:

| Consequence | What was measured |
| --- | --- |
| Pre-staged work comes along | `git add target && git commit` includes a separately staged file; `git commit -- target` does not. The former is what `rgit` replaces |
| Staging uses the real `.git/index` | Removing the private index removed the reconcile step, rollback, and post-commit repair along with it |
| A rejected commit leaves staging in place | A `pre-commit` hook exiting non-zero leaves staged entries untouched — there is nothing to roll back |
| Hooks are not policed | `git add -A` in a hook was measured sweeping an unrelated file into a commit. Plain `git commit` behaves identically; filtering would break formatter and codegen hooks |
| Merges are not special-cased | `git commit` mid-merge reads `MERGE_HEAD` and writes a correct two-parent commit unaided |

An earlier design used a temporary index seeded from `HEAD` to *exclude*
pre-staged work. That was the legacy tool's mistake reproduced: it forced index
snapshots, restores, and rollback, and it diverged from the `add && commit`
semantics `rgit` replaces. Removing it deleted roughly a third of the mechanism.

## What the legacy tool got wrong

`batch_commit` was the most-patched component in the MCP repo — **14** commits
with `fix(batch*)` in the subject, **17** `fix*` commits touching the legacy repo's
`batch-commit-tool.ts` out of **40** touching it at all. The clusters say what
to design away:

| Legacy failure cluster | Legacy patch pattern | `rgit` |
| --- | --- | --- |
| Index restore / rollback | Unstaged unrelated paths around each commit, then restored them | Does not exclude pre-staged work; nothing to restore |
| Line-range fragility | Hunk overlap via unified-diff line numbers | AST symbol anchors, byte-extent synthesis |
| Path canonicalization | Fragmented across commands | One canonicalizer at the entry boundary |
| Output noise | Verbose JSON payloads | Terse text default, `--porcelain` for machines |

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
     a newline if and only if `B_head` did. Neither the spike nor its
     adversarial round covered this, and it is the one place where "insert
     between two regions" has only one region: forcing a trailing newline would
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

**Concurrency** needs no handling: two `rgit` runs contend on `.git/index`
exactly as two `git add` runs do, and git's `index.lock` arbitrates.

### Validation

A Python + tree-sitter spike executed this algorithm against real Go sources —
**38 assertions**, all passing, every synthesized blob re-parsing with no ERROR
nodes. Coverage included doc-comment attribution in both directions (contiguous
comments attach; a blank line breaks it), single and multi-symbol splicing,
reverse-offset ordering, EOF-newline preservation, nearest-sibling insertion
where the neighbour is itself new, deletion synthesis, receiver-qualified method
disambiguation, adjacent symbols with no separating blank line, and nested
function literals.

The spike also found a defect in this record: keying only on qualified names
made a bare `Get` *absent* rather than *ambiguous*, yielding exit 3 ("did you
mean…") where exit 4 ("qualify it") is correct. **The resolver must index bare
names alongside qualified ones** — the remediations differ.

The spike has since been retired: every assertion above is covered by
`index_test.go` and `resolver_test.go`, which additionally parse each
synthesized blob rather than only inspecting its text. Comments in those files
still cite the prototype they came from — the sources are in git history, under
`spike/`, up to the commit that removed them.

## Symbol resolution

Tree-sitter is the primary resolver: it computes extents immediately with no
process-spawn latency. A language server, when reachable, cross-checks.

1. **Probe** an existing daemon socket — `$RGIT_LSP_SOCKET`, then
   `$XDG_RUNTIME_DIR/rgit-<server>.sock`. Dial budget **150ms**, query deadline
   **2s**.
2. **Spawn on demand** if no socket is live, completing the *current*
   invocation in `[ts-only]` mode rather than blocking on a cold index.
   Guarded by an `O_EXCL` lock beside the socket.
3. **Cross-check** tree-sitter's **declaration-only** extent (doc-comment and
   attribute prefix stripped) against the LSP `range`. Any mismatch is a hard
   fail — print both ranges and stage nothing.
4. **Degrade** to tree-sitter alone on timeout or a still-indexing server,
   announced with `[ts-only]` on stderr.

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

The daemon design above was verified only for `gopls` when it was written.
Measured directly for the other two before implementing against them:

| Server | `--help` claim | Measured behaviour | Verdict |
| --- | --- | --- | --- |
| `gopls` | `-listen=string`, prefixable `unix;` | Creates a real unix-domain socket file; other processes dial in | Listen-mode daemon |
| `vtsls` | `--socket=<number>` | With nothing listening on that TCP port, exits immediately (code 0, no output). Given a pre-bound TCP listener, connects to it as a client | Dials **out**, not a daemon |
| `pyright-langserver` | `--socket=<number>` | Same shape as `vtsls`: exits immediately with nothing listening; given a pre-bound TCP listener, connects out and streams `window/logMessage` over it | Dials **out**, not a daemon |

Verified with `vtsls --socket=<port>` / `pyright-langserver --socket=<port>`
against an empty port (immediate exit) and then against a port with `nc -l`
already bound (successful outbound connection, confirmed via `ss -tn` and by
observing `pyright-langserver` write real JSON-RPC frames to the accepting
listener). Neither tool's `--socket` takes a path, so even the outbound mode
has no unix-socket form to standardize on with `gopls`.

**Consequence: two transports behind one `Dial` interface, not one.** `gopls`
alone gets the probe → spawn → degrade sequence above, at
`$XDG_RUNTIME_DIR/rgit-gopls.sock`. `vtsls` and `pyright-langserver` get a
one-shot stdio subprocess (`--stdio`, their default and only listen-free mode)
spawned fresh per query, bounded by dial budget + query deadline end to end,
and killed on close rather than left running — there is no persistent daemon
for either to reuse, so pretending otherwise would just be a subprocess rgit
forgets to clean up.

**The query deadline is 2s, not the 250ms first specified.** That figure came
from a warm `gopls` daemon, which answers in single-digit milliseconds, and it
did not survive contact with the stdio servers. Measured end to end through
`Dial` + `DocumentSymbols`, single-declaration fixtures, warm binaries:

| Server | Transport | Dial | First `documentSymbol` after `didOpen` |
| --- | --- | --- | --- |
| `gopls` | unix socket, warm daemon | 1ms | 23ms |
| `pyright-langserver` | one-shot stdio | 102ms | 135ms |
| `vtsls` | one-shot stdio | 84ms | **259ms** |

`vtsls` misses a 250ms deadline by single-digit milliseconds, so under the
original budget the TypeScript cross-check degraded to `[ts-only]` on every
run — present in the code and absent in effect. A deadline that only ever
fires is not a budget, it is a disabled feature, and the accuracy argument for
symbol anchors depends on the cross-check actually executing.

2s clears all three with room for larger files. It does not weaken "never
block on a cold server": that rule is about a server still building its index,
which is handled by degrading, not by the deadline. The deadline exists to
bound a server that has already answered the handshake and is now merely slow.

### Cross-check exemptions

Tree-sitter alone, no LSP comparison: pseudo-anchors (servers do not report
import blocks as document symbols), deletions (the symbol exists only in HEAD,
outside the server's worktree view), and any degraded or absent daemon.

A fourth case surfaced during implementation, not anticipated when the three
above were written: the daemon answers, but its own `documentSymbol` outline
simply does not name the anchor being checked (a symbol kind the server
doesn't surface, or a container shape rgit's name normalization doesn't
recognize). That is not the same claim as "the extents disagree" — there is
nothing to compare — so it degrades to `[ts-only]` rather than hard-failing.
Treating it as exit 6 would mean an incomplete server outline could block a
commit for a symbol tree-sitter resolved correctly.

### Grammar scope

Three grammars in v1. Measured against 60 real commits in a live repo, **78%**
of touched files were Go/TS/Python and **91%** of added lines fell inside a
symbol body — so three grammars cover the dominant case, and `@toplevel` /
`@imports` handle the 8% at module scope. Median churn per touched file was
**4%** (p90 20%), which is precisely where symbol staging beats whole-file
staging; if commits typically rewrote most of a file, the tool would add nothing.

**`@imports` node shape differs by language.** Go exposes a single
`import_declaration` block; TypeScript and Python emit a separate
`import_statement` per import. The pseudo-anchor must span a contiguous run of
nodes — a Go-only implementation would silently stage just the first import in
a TS file.

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

No shell completion in v1: `rgit` is primarily agent-invoked, and the genuinely
useful completion (symbols after `auth.go:`) is a dynamic function shelling out
to `rgit diff --porcelain`, hand-written under any option.

## Dependencies

Binary size and dependency count are not constraints; each entry earns its place
against a specific, measured need. All verified against the module proxy and
built.

| Dependency | Version | Earns its place by |
| --- | --- | --- |
| `github.com/spf13/pflag` | v1.0.10 | Interspersed flag parsing |
| `github.com/tree-sitter/go-tree-sitter` | v0.25.0 | Core extent resolution |
| `tree-sitter-go` / `-typescript` / `-python` | v0.25.0 / v0.23.2 / v0.25.0 | The v1 grammars; import path is `<module>/bindings/go` |
| `go.lsp.dev/protocol` + `jsonrpc2` | v1.0.1 | Typed LSP 3.18; models `DocumentSymbolResult` as a sealed union over `SymbolInformationSlice \| DocumentSymbolSlice` — the case a hand-rolled client decodes wrongly |
| `github.com/aymanbagabas/go-udiff` | v0.4.1 | Per-symbol `+N/-M` counts in-process, no fork/exec per anchor |
| `golang.org/x/term` | v0.45.0 | `IsTerminal`, gating the `GIT_TERMINAL_PROMPT=0` rule |
| `github.com/go-quicktest/qt` | v1.102.0 | Tests; matches `claude-format-hooks` |

**`go-git` is rejected.** It reimplements git in pure Go and provides none of
what this design delegates: hook execution, `.gitattributes` filters, git's
pathspec matching, credential and GPG prompting. Shelling out is the design, not
a shortcut — using it even for reads would create a second, subtly divergent
source of truth about repository state.

Measured binary size: **5284 KB** with all three grammars linked, against a
1644 KB no-dependency baseline. The grammars dominate; every other dependency
is noise beside them.
