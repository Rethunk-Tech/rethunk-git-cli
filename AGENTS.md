# AGENTS.md

Internals for changing this repo. Usage: [HUMANS.md](HUMANS.md). Process: [CONTRIBUTING.md](CONTRIBUTING.md).

## The one invariant

**`rgit` is `git add <pathspec> && git commit` at symbol granularity.** Match git wherever git has an opinion. Divergence needs an explicit PR argument.

Inherited behaviour: [docs/USAGE.md § Behaviour inherited from git](docs/USAGE.md#behaviour-inherited-from-git). Decisions not built, and the measurements that closed them: [TODO.md](TODO.md).

## File map

| Path | Holds |
| --- | --- |
| [README.md](README.md) | Orientation and doc index |
| [HUMANS.md](HUMANS.md) | Run and use |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Commits, tests, deps, docs policy |
| [CHANGELOG.md](CHANGELOG.md) | Release notes |
| [SECURITY.md](SECURITY.md) | Vulnerability reporting, hooks/LSP trust |
| [docs/USAGE.md](docs/USAGE.md) | Commands, flags, grammar |
| [docs/ANCHORS.md](docs/ANCHORS.md) | Anchor syntax, extents, special paths |
| [docs/CODES.md](docs/CODES.md) | Exit codes, `--porcelain` formats |
| [docs/INSTALL.md](docs/INSTALL.md) | Build, installer, env vars, verify |
| [docs/LIMITATIONS.md](docs/LIMITATIONS.md) | Non-goals |

`docs/` ships with the tool; `TODO.md` does not.

## Delegation boundary

`rgit` shells out to `git` for everything git already does. It owns:

1. **Anchor resolution** — `FILE:NAME` → byte extent.
2. **Blob synthesis** — blob as if only named symbols changed.
3. **Argument precedence** — pathspec vs revision vs anchor.

Hooks, filters, pathspecs, trailers, amend — git's. No `go-git`. One deliberate divergence: a hook rejecting the commit rolls staging back to its pre-commit state, index entries only, no worktree file written (docs/USAGE.md § Behaviour inherited from git).

## Synthesis invariants

Breaking one is silent. Mechanism: `internal/synth` (splice order, EOF newline) and `internal/resolve` (extent scope).

| Invariant | Why |
| --- | --- |
| `hash-object` **must** carry `--path` | Skips `.gitattributes`/LFS filters |
| EOF newline inherited, never normalized | Git tracks a missing EOF newline |
| Multiple extents apply in **reverse byte-offset order** | Earlier replacements invalidate later offsets |
| Resolve every target before staging any | Failure must leave index untouched |
| Resolver indexes bare **and** qualified names | Bare absence → "did you mean" not "qualify it" |
| `@imports` spans N nodes | Go: one `import_declaration`; TS/Python: one `import_statement` per import |
| `commit` refuses `FILE:SYMBOL` on JSON/YAML/TOML | Spliced extent may disagree with grammar |

## Resolution model

Tree-sitter resolves; LSP **verifies** declaration-only extent (doc comment stripped). No daemon or cold index → `[ts-only]` on stderr; never block on a cold server.

## State

No persistent state. Creates only `rgit-commit.lock` in the git directory (`commit --only`'s OS lock), a `rgit-only-*` system-temp directory holding `commit --only`'s private index, a short-lived `rgit-index-*` copy beside the index while staging, and, for LSP, the socket, its spawn lock, and the spawned daemon's pidfile under UID-scoped `rgit-<uid>` in `$XDG_RUNTIME_DIR` (or system temp), `0700`, owner-verified before dial (`internal/lsp/dial.go`). Repository state is git's alone.

## Gate budget

`.gate.toml` chains build, lint, test and vuln because the cgo tree-sitter grammars cost about 115 CPU-s to compile cold and concurrent gates each compiled them. Measured 2026-10-09 with `gate --profile` at load 38 to 54 (throwaway Go and lint caches): cold was 39 s wall and 497 CPU-s concurrent, and is 32 s wall and 156 CPU-s chained, with `make build` 20 s of it. Warm went from 1.0 s to 2.0 s wall (6.7 to 7.3 CPU-s). Wall is not a verdict until a quiet-machine re-measure.
