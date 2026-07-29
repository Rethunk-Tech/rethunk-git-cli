# AGENTS.md

Internals for anyone — human or model — changing this repository. To *use*
`rgit`, read [HUMANS.md](HUMANS.md). To submit changes, read @CONTRIBUTING.md —
the only file pulled in eagerly, because its test layout and coverage rules bind
changes that would not otherwise think to consult it.

## The one invariant

**`rgit` is `git add <pathspec> && git commit` at symbol granularity.**

Where git has an opinion, match it exactly. Do not invent semantics git already
defines. A change that diverges from git must argue against it explicitly in
the PR, not quietly.

The consequences are listed in
[docs/USAGE.md § Behaviour inherited from git](docs/USAGE.md#behaviour-inherited-from-git),
and several of them read like bugs worth fixing. None is — each is git's own
behaviour, reproduced on purpose. Do not exclude, roll back, or police any of
them.

Reasoning and the measurement behind each: [specs/design.md](specs/design.md)

## File map

Read whichever one the change touches; none is loaded for you.

| Path | Holds |
| --- | --- |
| [README.md](README.md) | Orientation and the documentation index |
| [HUMANS.md](HUMANS.md) | Running and using `rgit` — what it does, inherited behaviour, degraded mode |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Process — commit style, test layout, dependency and documentation policy |
| [CHANGELOG.md](CHANGELOG.md) | Release notes — one entry per tagged version |
| [SECURITY.md](SECURITY.md) | Vulnerability reporting, and the hooks/LSP trust boundary |
| [specs/design.md](specs/design.md) | Design record — why, mechanisms, and every measurement |
| [docs/USAGE.md](docs/USAGE.md) | Command surface, argument grammar, flags |
| [docs/ANCHORS.md](docs/ANCHORS.md) | Anchor syntax, extents, pseudo-anchors, special paths |
| [docs/CODES.md](docs/CODES.md) | Exit codes and `--porcelain` record formats — the machine contract |
| [docs/INSTALL.md](docs/INSTALL.md) | Build, installer, cross builds, language servers, env vars, verify |
| [docs/LIMITATIONS.md](docs/LIMITATIONS.md) | What `rgit` does not do, and why |
| [TODO.md](TODO.md) | Backlog — v2 grammars, deferrals |

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

Breaking one of these is silent. The mechanism and the measurement behind
each is in
[specs/design.md § Blob synthesis](specs/design.md#blob-synthesis) and
[§ Grammar scope](specs/design.md#grammar-scope).

| Invariant | Why |
| --- | --- |
| `hash-object` **must** carry `--path` | Skips `.gitattributes`/LFS filters otherwise |
| EOF newline is inherited, never normalized | Git tracks a missing EOF newline as real content |
| Multiple extents apply in **reverse byte-offset order** | Earlier replacements would invalidate later offsets |
| Resolve every target before staging any | A failure must leave the index untouched |
| The resolver indexes bare **and** qualified names | A bare name that is merely absent yields "did you mean" where "qualify it" is correct |
| `@imports` spans N nodes | Go emits one `import_declaration`; TS and Python emit one `import_statement` per import |

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
