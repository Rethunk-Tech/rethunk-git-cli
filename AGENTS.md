# AGENTS.md

Internals for anyone — human or model — changing this repository. To *use*
`rgit`, read @HUMANS.md. To submit changes, read @CONTRIBUTING.md.

## The one invariant

**`rgit` is `git add <pathspec> && git commit` at symbol granularity.**

Where git has an opinion, match it exactly. Do not invent semantics git already
defines. This rule has overturned several drafts of this project; a change that
diverges from git must argue against it explicitly in the PR, not quietly.

The consequences are listed in @docs/USAGE.md § Behaviour inherited from git,
and several of them read like bugs worth fixing. None is. Pre-staged work
coming along, staging surviving a rejected commit, and a hook sweeping paths
the caller never named are git's own behaviour, reproduced on purpose — do not
exclude, roll back, or police them.

Reasoning and the measurement behind each: @specs/design.md

## File map

| Path | Holds |
| --- | --- |
| @README.md | Orientation and the documentation index |
| @HUMANS.md | Running and using `rgit` — what it does, inherited behaviour, degraded mode |
| @CONTRIBUTING.md | Process — commit style, test layout, dependency and documentation policy |
| @specs/design.md | Design record — why, mechanisms, and every measurement |
| @docs/USAGE.md | Command surface, argument grammar, flags, exit codes |
| @docs/ANCHORS.md | Anchor syntax, extents, pseudo-anchors, special paths |
| @docs/INSTALL.md | Build, language servers, env vars, verify |
| @TODO.md | Backlog — known limitations, v2 grammars, deferrals |

`docs/` ships with the tool. `specs/` does not — the design record lives there.

## Delegation boundary

`rgit` shells out to `git` for everything git already does. It owns exactly
three things:

1. **Anchor resolution** — mapping `FILE:NAME` to a byte extent.
2. **Blob synthesis** — constructing the blob that would exist if only the named
   symbols had changed.
3. **Argument precedence** — deciding whether a token is a pathspec, a revision,
   or an anchor.

Everything else — hooks, filters, pathspec matching, trailers, amend semantics,
credential prompting, index bookkeeping — is git's. `go-git` is rejected for
this reason; it would create a second, divergent source of truth.

## Invariants in the synthesis path

Each of these was a bug at some point in design. Breaking one is silent.

| Invariant | Why |
| --- | --- |
| `hash-object` **must** carry `--path` | Without it, `.gitattributes` clean filters and LFS normalization are bypassed |
| EOF newline is inherited, never normalized | Git tracks no-newline-at-EOF as real content; adding one commits a byte nobody changed |
| Multiple extents apply in **reverse byte-offset order** | Earlier replacements otherwise invalidate later offsets |
| Resolve every target before staging any | Resolution is a pure read; a failure must leave the index as found |
| The resolver indexes bare **and** qualified names | A bare name that is merely absent yields "did you mean" where "qualify it" is correct |
| `@imports` spans N nodes | Go has one `import_declaration`; TS and Python emit one `import_statement` per import |

## Resolution model

Tree-sitter is the primary resolver and always produces the extent that gets
staged. A language server, when reachable, only *verifies* it — comparing
against the **declaration-only** extent, with the doc-comment prefix stripped,
because LSP ranges exclude doc comments.

Degraded resolution is normal, not an error: no daemon, a cold index, or an
unsupported language all fall back to tree-sitter with `[ts-only]` on stderr.
Never block on a cold server.

## State

`rgit` holds no persistent state of its own. The only files it creates are the
language-server socket and its spawn lock under `$XDG_RUNTIME_DIR`, both
disposable. Repository state lives entirely in git.
