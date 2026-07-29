# Changelog

Notable changes to `rgit`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- HTML element and id anchors (`div#app`), via a new tree-sitter-html
  adapter. Deliberately narrow: element **+ id only** — no class selectors,
  no `nth-of-type`, no combinators — and an element with no id gets no
  anchor at all, since indexing bare tag names would collide across nearly
  every real document (this resolver has no per-parent scoping). A
  duplicate id is exit 4 through the existing ambiguity machinery, the
  same as two same-named Go functions. The LSP cross-check is not wired,
  matching TOML and SQL's existing `[ts-only]` verdict. See
  [`docs/ANCHORS.md`](docs/ANCHORS.md).

- `rgit context`, one-call repository orientation for an agent's first
  turn: recent commit subjects, then the same per-file, per-symbol
  diffstat `rgit diff` itself reports for everything committable, as a
  single fixed-shape `C`/`F`/`X` record stream. Pure read composition over
  `internal/gitx` and `internal/diff` — no new resolution or attribution
  machinery, and no flags beyond `--help`: the shape is fixed and capped
  at 16 KiB, truncated with a trailing `X` record rather than growing
  without bound. See [`docs/USAGE.md`](docs/USAGE.md#context).

- `rgit log FILE:SYMBOL`, patch-free history of one symbol: one
  `HASH<TAB>SUBJECT`-shaped record per commit that touched its current
  extent, newest first. Reuses anchor resolution and `git log -L`, which
  re-derives the touched range at each ancestor commit itself — no
  per-commit re-parse. Patches are opt-in (`-p`/`--patch`), never default,
  since plain `git log -L` always prints the full patch body. A file
  renamed since a commit loses its history under the old name. See
  [`docs/USAGE.md`](docs/USAGE.md#log).

- `rgit blame FILE:SYMBOL`, bounding `git blame` to one symbol's own extent
  instead of the whole file. Reuses anchor resolution and nothing else: an
  unresolvable anchor is exit 3 (or 4/9), never a silently widened
  whole-file blame. `--porcelain` passes straight through to git's own
  `git blame --porcelain` format. See
  [`docs/USAGE.md`](docs/USAGE.md#blame).

- `SECURITY.md`, a tag-driven release workflow that publishes cross-built
  binaries with their `SHA256SUMS`, and grouped Dependabot updates for Go
  modules and Actions.

- `make cross` now builds SQL support into every cross target when the build
  host has the tree-sitter CLI, rather than always producing SQL-less
  binaries. Generation runs once via `cmd/rgit-install -generate-only`; the
  per-target cost is only compiling the generated C, ~6–7s each.

- Shell completion now offers flags for `languages`, `doctor` and
  `completion`, which previously fell through to nothing in both bash and
  zsh even though all three were offered as subcommands.

### Changed

- `rgit-install -with-servers` installs `gopls@v0.23.0` instead of
  `gopls@latest`, matching the deliberate `tree-sitter-sql` pin: install
  reproducibility no longer drifts under a fixed rgit release. See
  [`docs/INSTALL.md`](docs/INSTALL.md#language-servers).

### Security

- The managed `gopls` socket and its spawn lock now live in a private,
  UID-scoped `0700` directory that is verified — owner, mode, and not a
  symlink — before every dial or spawn, rather than sitting at a predictable
  path in a world-writable temp directory where another user on the host
  could pre-create it and receive file contents via `didOpen`. A directory
  that fails the check degrades to `[ts-only]`. A caller-supplied
  `$RGIT_LSP_SOCKET` is untouched.

### Fixed

- `rgit diff` no longer silently shrinks a file's region set when a name
  `resolve.DeclOrder` emits fails to resolve — a condition that only ever
  signals an internal resolver inconsistency, never a legitimate input.
  It now fails loudly instead of mis-reporting that file's rows with
  nothing to say why.
- `rgit diff` no longer claims a language server verified extents it never
  compared. A file whose only cross-checked resolutions are pseudo-anchors
  — one whose sole anchor is `@imports`, say — reported a completed
  cross-check and suppressed `[ts-only]`, while `rgit commit`'s per-anchor
  form already degraded on the identical input. Both now answer through one
  shared verdict, so the two cannot disagree. See
  [`docs/CODES.md`](docs/CODES.md).
- A stale or incompatible language-server socket no longer pins every later
  invocation to `[ts-only]`: a managed socket that fails the handshake is
  unlinked and the next candidate tried. Spawn-on-demand also unlinks a dead
  daemon's leftover socket instead of failing its own bind forever, and a
  stale spawn lock is reclaimed and retried inside the same invocation.
- `DocumentSymbols` closes each document it opens, so a long-lived `gopls`
  no longer accumulates open documents across an invocation's anchors.
- A nil `*lsp.Session` degrades explicitly instead of dialling and leaking a
  client nobody closes.
- Binary detection samples a bounded prefix instead of reading a whole file:
  classifying an anchor no longer slurps a large worktree file, nor
  materializes an entire HEAD blob for a path that is only being sniffed.
- The exit-9 message names the path and whether shebang sniffing ran. An
  extensionless file produced a dangling "no grammar registered for " that
  hid the fallback entirely.
- `rgit diff --sym` on an anchor whose file has no grammar printed
  `resolve: "NAME": unresolved` — the same label exit 3 uses — instead of
  naming what docs/CODES.md's exit-9 row actually means. It now says
  `unsupported language`.
- `rgit commit --dry-run` warns about an untracked file it cannot read
  rather than silently reporting low line counts.
- `make clean` removes `./rgit-install`, which it built and `.gitignore`
  already listed.
- The unresolved-argument diagnostic says "rules considered" — it listed
  pathspec magic as tried even when a leading colon had ruled it out.
- SQL parser generation no longer deletes the previous `csrc/` before the
  replacement is safely in place; a failed finalize restores it rather than
  leaving neither.
- `rgit-install -with-servers` exits non-zero when a managed server fails,
  instead of reporting `FAILED` and exiting 0, and each install is bounded by
  a timeout rather than running unbounded against a hung registry.
- TypeScript and TSX no longer rebuild their tree-sitter language on every
  parse; every other adapter already cached it once at registration.
- `PeekShebangLine` no longer reports success for an empty file or a genuine
  read error, where any successful open used to be treated as sufficient.

- The install docs named `~/.local/bin/rgit` as the uninstall target, which
  the default install never writes to — it targets `$GOBIN`, else
  `$(go env GOPATH)/bin`. The stripped-size figure was also stale by six
  grammars.
- `rgit doctor` aligns its detail column across both report sections. A
  language-server label longer than the old fixed 24-character field pushed
  its own path out of line, and `[ok]` / `MISSING` being different widths
  shifted the name column by status. Both are padded now.

## [1.0.0] — 2026-07-28

First public release.

`rgit` is `git add <pathspec> && git commit` at symbol granularity. Naming
`auth.go:ValidateToken` commits that one function — doc comment, attributes,
body — and leaves every other edit in the file uncommitted. Two commands,
`diff` and `commit`; everything else stays plain `git`.

What ships:

- **Symbol anchors** — `FILE:NAME` addresses a declaration or a container
  member, with `@header`, `@imports`, and `@toplevel` reaching the regions no
  symbol owns ([`docs/ANCHORS.md`](docs/ANCHORS.md)).
- **Bare positionals** — pathspecs, revisions, and anchors mix freely in one
  invocation under a six-rule precedence table, with no escape syntax
  ([`docs/USAGE.md`](docs/USAGE.md#argument-shape)).
- **Ten grammars unconditionally** — Go, TypeScript, TSX/JavaScript, Python,
  Markdown, Shell, YAML, CSS, JSON, and TOML — plus SQL behind the `rgit_sql`
  build tag ([`docs/INSTALL.md`](docs/INSTALL.md#sql-support)).
- **Language-server cross-check** for 9 of those 11 grammars, with tree-sitter
  always producing the extent that gets staged. A missing or cold server
  degrades to `[ts-only]` rather than blocking
  ([`docs/LIMITATIONS.md`](docs/LIMITATIONS.md#language-server-coverage)).
- **A machine contract** — stable exit codes and `--porcelain` records, with
  `rgit completion bash|zsh` as an in-repo consumer of the latter
  ([`docs/CODES.md`](docs/CODES.md)).
- **Git semantics throughout** — git's exit codes, pathspecs, hooks, filters,
  trailers, and signing, because git does all of it. Divergence is a bug
  ([`docs/USAGE.md`](docs/USAGE.md#behaviour-inherited-from-git)).

Known limitations are catalogued in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md); the reasoning and the
measurements behind every decision are in
[`specs/design.md`](specs/design.md).
