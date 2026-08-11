# Changelog

Notable changes to `rgit`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `rgit context` now emits `W` records for degraded `[ts-only]` resolution
  and non-fatal diff warnings, while retaining the matching stderr
  diagnostics. See [`docs/CODES.md`](docs/CODES.md#rgit-context).

- `rgit languages --porcelain` now includes a fourth `CROSS-CHECK` column:
  `wired` for compile-time language-server wiring and `ts-only` for TOML and
  SQL. This reports design-time coverage, not server reachability.

- `rgit doctor` now checks git's resolved version against the floor
  `rgit` relies on (`git commit --trailer`, git 2.32+), not just its
  presence on `PATH`. Informational, like every other doctor check — a
  below-floor git is reported, not refused. See
  [`docs/INSTALL.md`](docs/INSTALL.md#prerequisites).

- `rgit completion fish`, alongside the existing bash and zsh scripts —
  subcommands, per-subcommand flags, and live `FILE:SYMBOL` completion
  through fish's own `complete`-driven model. See
  [`docs/USAGE.md`](docs/USAGE.md#shell-completion).

- Release artifacts now include darwin/amd64 and darwin/arm64, built
  natively on a `macos-latest` runner (`make cross-darwin`) rather than
  cross-compiled — zig, what the linux/windows matrix uses, cannot supply a
  macOS SDK. Covered by the same `SHA256SUMS` and cosign signature as the
  other three artifacts. See
  [`docs/INSTALL.md`](docs/INSTALL.md#cross-builds).

- `scripts/install.sh` now downloads and verifies the darwin/amd64 and
  darwin/arm64 release artifacts on macOS, using `shasum` for checksum
  verification. See [`docs/INSTALL.md`](docs/INSTALL.md#install-script).

- `scripts/install.sh` automatically verifies `SHA256SUMS` with cosign when
  cosign is available, while retaining the SHA256-only fallback when it is
  absent. Dry runs never fetch the signature bundle. See
  [`docs/INSTALL.md`](docs/INSTALL.md#install-script).

- `scripts/install.ps1` adds a checksum-verified Windows/amd64 release
  installer. `-DryRun` previews the plan without network access and requires
  an explicit `VERSION` tag. See
  [`docs/INSTALL.md`](docs/INSTALL.md#install-script).

- `scripts/install.ps1` optionally verifies `SHA256SUMS` with cosign when
  cosign is on `PATH`, using the same Sigstore bundle and release-workflow
  identity as `scripts/install.sh`. Without cosign, the SHA256-only path is
  unchanged. `-DryRun` stays network-free. See
  [`docs/INSTALL.md`](docs/INSTALL.md#install-script).

- `rgit symbols FILE` lists every declared symbol the worktree resolver can
  see; bash/zsh/fish `FILE:SYMBOL` completion uses it so clean files still
  complete. `commit` completion calls `rgit symbols --for-commit` so
  structured-data anchors commit would refuse are never suggested. See
  [`docs/USAGE.md`](docs/USAGE.md#shell-completion).

- `rgit blame FILE:SYMBOL` falls back to the `HEAD` blob when the worktree
  file is gone, matching `rgit log`'s HEAD resolution for deleted paths. See
  [`docs/USAGE.md`](docs/USAGE.md#blame).

- `rgit blame --follow-rename` re-resolves the symbol at each rename boundary
  (same one-parse-per-rename trade-off as `log --follow-rename`) and always
  reads the `HEAD` blob under the flag. See
  [`docs/USAGE.md`](docs/USAGE.md#blame).

- `rgit completion pwsh` emits a PowerShell 7 native completer
  (`Register-ArgumentCompleter -Native`) with the same subcommand, flag, and
  `FILE:SYMBOL` surface as bash/zsh/fish. See
  [`docs/USAGE.md`](docs/USAGE.md#shell-completion) and
  [`docs/INSTALL.md`](docs/INSTALL.md#shell-completion).

- `rgit symbols` has a dedicated command section in
  [`docs/USAGE.md`](docs/USAGE.md#symbols) (usage, `--for-commit`, exit
  behaviour), separate from the shell-completion notes.

- Windows CI now exercises the optional cosign verify path for
  `scripts/install.ps1` with mocked downloads (parity with the Linux
  install-script job). Dry-run plan wording matches the Linux
  `cosign/Sigstore bundle` phrase.

- Extensionless shebang sniffing can peek the `HEAD` blob when the worktree
  file is absent, so deleted hooks/scripts keep grammar routing. See
  [`docs/ANCHORS.md`](docs/ANCHORS.md).

- `rgit log --since=DATE [--until=DATE] [PATH...]` now accepts
  `-n`/`--max-count` and forwards git's own commit-count limit; without it,
  the path-scoped history remains unbounded. See
  [`docs/USAGE.md`](docs/USAGE.md#log-by-date-and-path).

- `rgit log FILE:SYMBOL` now accepts `--since`/`--until` and
  `-n`/`--max-count` without losing symbol scope. The same date bounds apply
  to every segment of `--follow-rename`. See
  [`docs/USAGE.md`](docs/USAGE.md#log).

- Successful `rgit commit --porcelain` output now starts with an
  `H<TAB>SHA` record containing the full commit object id, before any target
  rows. Dry runs remain target-only, while `--allow-empty` still reports the
  created commit. See [`docs/CODES.md`](docs/CODES.md#rgit-commit---porcelain).

### Fixed

- HTML language-server cross-checks now match class-bearing elements by
  stripping the server's `.class` suffix before comparing its `tag#id` name;
  staged HTML extents remain unchanged. See
  [`docs/LIMITATIONS.md`](docs/LIMITATIONS.md#language-server-coverage).

- `rgit log` rejects a lone `--` positional with a clear usage error instead
  of failing later during anchor resolve.

- `rgit diff`/`rgit commit` no longer error with "malformed response header"
  in a repo with submodules whose pointer changed. `git cat-file --batch`
  answers a gitlink path with a third response shape (`<sha> submodule`,
  no content) the batched blob reader didn't recognize; it is now treated
  as `Exists=false`, matching the non-batch `CatFile` path's existing
  behaviour for the same case.

## [1.2.0] — 2026-08-05

### Added

- Release artifacts now carry a keyless cosign signature over `SHA256SUMS`
  (`SHA256SUMS.sigstore.json`), verifiable against this repo's own release
  workflow identity. See [`docs/INSTALL.md`](docs/INSTALL.md#verify).

- The LSP extent cross-check now covers **HTML**, via
  `vscode-html-language-server`: an id-bearing element (`div#app`) verifies
  against a live server the same way Go, TypeScript, Python, and five other
  grammars already do. See
  [`docs/LIMITATIONS.md`](docs/LIMITATIONS.md#language-server-coverage).

  **Errata:** the original note claimed class-bearing HTML elements degrade to
  `[ts-only]`; they do not — the cross-check strips the server's `.class`
  suffix before comparing `tag#id` names (see Unreleased Fixed).

- `--follow-rename` on `rgit log FILE:SYMBOL`, continuing a symbol's history
  past a rename `git log -L`'s own line-range tracking loses (a rename that
  also reorders the symbol within the file): the anchor is re-resolved with
  tree-sitter at each rename boundary instead of trusting that tracking, one
  parse per rename rather than per commit. See
  [`docs/USAGE.md`](docs/USAGE.md#log-across-renames).

- `-p`/`--patch` on `rgit diff`, appending git's own real patch body after
  the aligned/porcelain report — unmodified, the same "not a second format
  `rgit` invents" convention `log -p` and `blame --porcelain` already use.
  Purely additive: the default output with neither flag is unchanged, and
  the patch covers the identical scope and pathspec filter as the report
  above it. Mutually exclusive with `--porcelain`; closes the
  `diff <(git show HEAD:file) file` workaround. See
  [`docs/USAGE.md`](docs/USAGE.md#output).

- `rgit log --since=DATE [--until=DATE] [PATH...]`, a second, unanchored
  invocation shape selected by the presence of either flag: ordinary git
  history bounded by date and, optionally, one or more paths, with no
  `FILE:SYMBOL` at all. Closes the one `git log --since=... -- <paths>`
  carve-out that otherwise had no `rgit` equivalent. `--porcelain` and
  `-p`/`--patch` work exactly as they do on the `FILE:SYMBOL` form. See
  [`docs/USAGE.md`](docs/USAGE.md#log-by-date-and-path).

- `rgit commit` refuses a `FILE:SYMBOL` anchor into a structured-data file
  (JSON, YAML, TOML), a new exit code 12: a spliced extent is not
  guaranteed to agree with one of these formats' own grammar, and nothing
  downstream would catch the resulting malformed blob before it reached
  `HEAD`. `rgit diff`, `rgit blame`, and `rgit log` are unaffected — none of
  them writes a blob — and committing the same file by path still works.
  See [`docs/CODES.md`](docs/CODES.md#exit-12-is-commits-alone).

- `rgit diff A:f.go B:f.go` — exactly two `rev:path` positionals naming the
  identical path at two revisions — compares that file across revisions,
  attributed by symbol like any other scope. A lone (unpaired) `rev:path`,
  or two naming different paths, still refuses (exit 129) as before. See
  [`docs/USAGE.md`](docs/USAGE.md#argument-shape).

- `rgit diff` batches every changed file's git-backed blob read into one
  `git cat-file --batch` process instead of one `git cat-file` subprocess
  per file per side — a large rename or vendor bump no longer pays linear
  git-exec overhead. Porcelain output is unaffected. See
  [`specs/design.md`](specs/design.md#grammar-scope).

- `scripts/install.sh`: a checksum-verified install path for a machine with
  only git — no Go toolchain, no zig. linux/amd64 and linux/arm64 only;
  language servers stay opt-in, as everywhere else. See
  [`docs/INSTALL.md`](docs/INSTALL.md#install-script).

- `rgit doctor --deep` dials each on-PATH language server for real (the
  identical handshake `rgit diff`/`rgit commit` already perform) and reports
  `(reachable)` or `(degraded — handshake timed out or unanswered)` instead
  of the default's bare "on PATH" answer. Not the default: a cold CI host
  with nothing installed should stay instant. See
  [`docs/USAGE.md`](docs/USAGE.md#doctor).

- `rgit context` gains a `B` record — current branch, upstream, and
  ahead/behind counts — sorting first, ahead of `F` and `C`. Absent on an
  unborn branch. See
  [`docs/CODES.md`](docs/CODES.md#rgit-context).

- `rgit diff --sym FILE:member`, when the container is new to `HEAD`, warns
  that `rgit commit` would stage the whole container rather than just the
  named member — the same case `rgit commit` itself already announces, now
  visible before staging too. Fires only under `--sym`; the unfiltered
  listing already shows every sibling as its own row. See
  [`docs/ANCHORS.md`](docs/ANCHORS.md#qualification).

- Extensionless shebang scripts naming `node`, `nodejs`, `tsx`, `ts-node`,
  or `bun` now route to the TypeScript adapter, including via `env -S` and
  through an `npx`/`bunx` package-runner wrapper (`#!/usr/bin/env npx
  tsx`). No new grammar — these already resolved with a `.ts`/etc.
  extension; only the extensionless case was gapped. See
  [`docs/ANCHORS.md`](docs/ANCHORS.md#language-support).

- `rgit diff` now attributes an untracked file by symbol — `MOD` rows for
  each declared symbol, exactly like a brand-new tracked file — instead of
  collapsing it to a single `(untracked)` row. `--sym` filters it the same
  way. The collapsed `UNTRACKED` row survives only for a binary file or one
  whose language has no grammar. See
  [`docs/CODES.md`](docs/CODES.md#rgit-diff---porcelain).

- `rgit doctor --porcelain` lists environment and language-server checks as
  stable `KIND<TAB>NAME<TAB>STATUS<TAB>DETAIL` records instead of the
  aligned human report, matching `rgit languages --porcelain`'s own
  convention. Grammars stay excluded; `rgit languages --porcelain` already
  covers them. See [`docs/CODES.md`](docs/CODES.md#rgit-doctor---porcelain).

- `rgit diff --sym`, `rgit blame`, and `rgit log` now print the ordinal-anchor
  advisory (`[warning] anchor '...' is positional; ...`) that `rgit commit`
  already prints, for the identical anchor form on any command instead of
  only at commit time. See
  [`docs/USAGE.md`](docs/USAGE.md#ordinal-anchor-warnings).

- `-p` as an alias for `--porcelain` on `rgit blame`, matching git blame's
  own flag exactly. See [`docs/USAGE.md`](docs/USAGE.md#blame).

- `rgit languages --in-repo` narrows the grammar listing to adapters with at
  least one matching tracked file in the current repository, advisory only
  (the binary still contains every compiled-in grammar). Requires a git
  repo, unlike the plain form. See [`docs/USAGE.md`](docs/USAGE.md#languages).

- `RGIT_LSP_DIAL_TIMEOUT` and `RGIT_LSP_QUERY_TIMEOUT` env vars override the
  language-server dial and query timeouts (defaults `150ms`/`2s`) without a
  rebuild, for slow hosts or a cold `gopls` index. Unset, malformed, zero, or
  negative values keep the default. See
  [`docs/INSTALL.md`](docs/INSTALL.md#environment-variables).

### Changed

- `rgit context` now emits `F` (diff) records before `C` (commit) records,
  reversing the original order. On a busy branch the bounded 20-commit
  history could crowd out the unbounded, actionable diff section before the
  16 KiB budget was reached; diff rows now survive truncation first. See
  [`docs/USAGE.md`](docs/USAGE.md#context).

### Fixed

- A symbol anchor into an **uninitialized submodule** (`git submodule
  deinit`'s own shape: the directory survives, emptied of its own `.git`)
  now refuses with exit 10, the same "submodule; name the path instead"
  every initialized submodule already gets — previously it fell through to
  a misleading exit 9 ("no grammar registered") once the empty directory's
  contents turned out unparseable. See
  [`docs/LIMITATIONS.md`](docs/LIMITATIONS.md#symlinks-submodules-renames-and-content-filters).

- Exit 3's "did you mean" suggestion now finds a close typo on a name that
  exists only inside a container (e.g. `Gett` → `A.Get`): distance was
  measured against the container-qualified string, which inflated it past
  the match threshold for exactly the case that needed it most. Exit 4's
  message also now says "qualify with one of: ..." instead of "did you
  mean" for candidates that already resolve, just ambiguously. See
  [`docs/CODES.md`](docs/CODES.md#exit-codes).

## [1.1.0] — 2026-07-29

### Added

- A global `-C <path>` option, given before the command, matching
  `git -C <path>`: the repository is discovered from there and relative
  pathspecs and anchors resolve against it, repeats accumulate, and `-C ""`
  is a no-op. Validated before dispatch, so a missing directory argument
  (129) or one that cannot be entered (128) fails whatever command
  followed. See [`docs/USAGE.md`](docs/USAGE.md#global-flags).

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
  turn. As shipped in 1.1.0, recent commit subjects came first, followed by
  the same per-file, per-symbol diffstat `rgit diff` itself reports for
  everything committable, as a single fixed-shape `C`/`F`/`X` record stream.
  Later, 1.2.0 added `B` and moved `F` before `C`. Pure read composition over
  `internal/gitx` and `internal/diff` —
  no new resolution or attribution machinery, and no flags beyond `--help`:
  the shape is fixed and capped at 16 KiB, truncated with a trailing `X`
  record rather than growing without bound. See
  [`docs/USAGE.md`](docs/USAGE.md#context).

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

- Advisory output uses one prefix everywhere: the non-conventional commit
  message warning now reads `[warning] …` like every other advisory, instead
  of `rgit: warning: …`.
- `--help`/`-h` wins wherever it appears on `context`, `doctor`, and
  `completion`, matching `blame`, `log`, and `languages` — previously those
  three accepted it only as their sole argument, so `rgit context --porcelain
  --help` was a usage error rather than help. `rgit completion <unknown>`
  also prints the command's help, as a missing shell argument already did.
- `rgit-install -generate-only` combined with `-with-servers` installs the
  managed language servers instead of silently skipping them, and an install
  whose manager bin directory cannot be located now warns that PATH
  reachability could not be verified rather than saying nothing.
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
- The spawn lock's own staleness check now `Lstat`s the lock path and
  requires a regular file before trusting its mtime, rather than `Stat`ing
  it (following a symlink). A symlink planted at the lock path in the same
  world-writable-by-default runtime directory could otherwise point at an
  old file of another user's choosing, aging the lock artificially and
  convincing an invocation to clear one still legitimately held. A lock
  that fails the check is left in place untouched, same as the lock is
  already treated as best-effort elsewhere.

### Fixed

- Every name in a Go inline multi-name declaration resolves, not just the
  first: `const a, b = 1, 2` indexed only `a`, leaving `b` unresolvable
  where the identical TypeScript shape already worked. Both names share the
  spec's whole extent, since no sub-range names one without the other's
  bytes. See [`docs/ANCHORS.md`](docs/ANCHORS.md).
- `rgit log FILE:SYMBOL` refuses a path containing `:` instead of embedding
  it into `git log`'s own `-L<range>:<path>` argument, which joins the two
  with `:` and has no way to escape one inside `path` -- and, measured
  directly, `git log` refuses the one alternative shape that would avoid
  it (`-L<range>:<path> -- <pathspec>` is a hard error, unlike `blame`'s own
  `-L`, which keeps the range and the path separate).
- Staging a brand new member of a container whose own name is ambiguous no
  longer falls back to an ordinary insertion. Blob synthesis's container-
  widening step used to treat every resolution failure alike, so a member
  of a container name that collides — two duplicate TOML `[[servers]]`
  headers is the measured shape — produced malformed output instead of the
  same exit-4 ambiguity a direct anchor resolve already gives. HTML element
  anchors no longer risk widening a brand new nested element into an
  unrelated, coincidentally same-named element elsewhere in the document;
  a fresh HTML insert now always lands at its own sibling position instead.
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
- `rgit blame --` and `rgit log --` no longer panic. A bare `--` is consumed
  whole by argument classification's own rule 1 ("everything after `--` is
  a pathspec, always") and yields nothing to classify; both commands now
  report the same "requires a `FILE:SYMBOL` anchor" refusal, exit 129, that
  a missing positional already gets.
- `rgit blame` and `rgit log` reject an unrecognized `-`-prefixed flag
  (`rgit blame --nope`) before positional classification instead of
  silently treating it as the `FILE:SYMBOL` anchor. The usage-error wording
  for an unrecognized argument is now the same shape across `blame`, `log`,
  `languages`, and `doctor` — `rgit: <command>: unrecognized argument
  %q` — where each previously used its own phrasing.
- `rgit context`'s output stream can no longer exceed its documented 16 KiB
  cap. The trailing `X\tTRUNCATED\t<n>` record was appended unconditionally
  after the budget-bounded loop, so its own bytes could push the total past
  budget; the budget check now reserves room for that record up front.
- Shell completion's symbol lookup after `FILE:` no longer fires for
  `rgit context`, `languages`, `doctor`, or `completion` — none of which
  takes a `FILE:SYMBOL` target — and is limited to `diff`, `commit`,
  `blame`, and `log`, the commands that actually accept one.

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
