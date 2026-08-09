# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave landed optional
cosign verification in `scripts/install.sh`, Windows CI dry-run for
`install.ps1`, `rgit log FILE:SYMBOL` with `--since`/`--until`, `rgit
context` `W` diagnostic records, HTML ordinal `Resolve` coverage for
`div#app#2`, and a `[1.2.0]` CHANGELOG errata for class-bearing HTML.
Deliberately not queued: `rgit restore` (designed and held back —
[`specs/design.md`](specs/design.md#rgit-restore-filesymbol-an-accepted-design-deliberately-not-built)),
context staged/unstaged split, TOML taplo / SQL LSP cross-checks, Rust/C/C++/
Vue/Svelte grammars, SCSS/zsh, and orphan-gopls handshake cleanup.

## Release / install

- [ ] **Optional cosign verification in `scripts/install.ps1`.** The POSIX
      installer now auto-verifies `SHA256SUMS` with cosign when present;
      Windows still SHA256-only. Pair the same optional bundle path
      (`SHA256SUMS.sigstore.json`, identity regexp + OIDC issuer from
      `docs/INSTALL.md`) without hard-failing when cosign is absent.

      **Packages / files:** `scripts/install.ps1`, `docs/INSTALL.md`,
      optionally CI dry-run notes.

      **Traps:** Absence of cosign must keep today's SHA256-only path. Do not
      invent a second identity string. `-DryRun` stays network-free (no
      bundle fetch).

      **Acceptance criteria:** With cosign on `PATH`, install.ps1 verifies
      the checksum file before installing; without cosign, behaviour matches
      today. Docs describe both paths.

- [ ] **CI exercise for the cosign install path.** The Linux `install-script`
      job only dry-runs; nothing asserts the live `cosign verify-blob` branch
      (mock cosign on PATH + fixture bundle, or equivalent). Optional polish:
      dry-run could mention the sigstore plan when cosign is present without
      fetching.

      **Packages / files:** `.github/workflows/ci.yml`, possibly
      `scripts/install.sh` dry-run messaging.

## Completion / tooling

- [ ] **Complete every declared symbol in a file, not only dirty ones.**
      Dynamic `FILE:SYMBOL` completion shells out to `rgit diff --porcelain`
      and cuts the `SYMBOL` column (`internal/app/completion.go`'s
      `_rgit_symbols` / fish equivalent). On a clean file, tab after
      `auth.go:` yields nothing — agents picking a symbol to edit or commit
      before any diff exists get an empty list.

      **Packages / files:** `internal/app/completion.go` (bash/zsh/fish
      scripts), likely a small read-only enumerator (new `rgit symbols FILE`
      or a resolve-against-HEAD helper under `internal/resolve/` /
      `internal/app/`), `docs/USAGE.md` § Shell completion, `docs/CODES.md`
      if a new porcelain shape appears.

      **Traps:** HEAD vs worktree (log resolves HEAD; blame/commit use the
      worktree — `internal/app/shared.go`). Huge files must not stall the
      prompt; failures stay silent (`2>/dev/null` precedent). Only list
      symbols the running binary's grammars can resolve. Reuse shared flag
      constants; do not hand-copy per shell a third time. Keep “never suggest
      an anchor `commit` would refuse” as the invariant.

      **Acceptance criteria:** Clean repo, unmodified tracked `auth.go`:
      bash/zsh/fish completion after `auth.go:` lists every declared symbol.
      Dirty symbols still appear. Completer never prints errors on the
      prompt. Drift guard in `completion_test.go` still passes for all
      shells.

- [ ] **Add `rgit completion pwsh` (PowerShell).** Completion ships for
      bash, zsh, and fish only (`internal/app/completion.go`); windows/amd64
      is a published target with no shell UX. PowerShell's native path is
      `Register-ArgumentCompleter -Native`, not a `compgen` port.

      **Packages / files:** `internal/app/completion.go` (new script +
      switch arm), `internal/app/completion_test.go` (flag-list drift guard),
      `docs/USAGE.md`, `docs/INSTALL.md`.

      **Traps:** Native completer scriptblock receives
      `$wordToComplete`, `$commandAst`, `$cursorPosition` — parse the AST for
      subcommand / `-C` / `FILE:` the way bash walks `COMP_WORDS`, do not
      assume argv-style splitting. Emit
      `System.Management.Automation.CompletionResult` objects. Symbol
      completion should call the same enumerator the Unix shells use once
      that exists (prior item), not a fourth copy of `rgit diff --porcelain`
      filtering. Windows PowerShell 5.1 vs PowerShell 7 differences in
      completer registration.

      **Acceptance criteria:** `rgit completion pwsh` prints a script that,
      registered in PowerShell 7, offers subcommands, per-subcommand flags,
      and `FILE:SYMBOL` candidates for `diff`/`commit`/`blame`/`log`.
      Flag-list drift test covers pwsh the same as bash/zsh/fish. Unknown
      shell error text lists `pwsh` among supported shells.

## Blame / log

- [ ] **`rgit blame` falls back to HEAD when the worktree file is gone.**
      Today blame reads the worktree only and refuses with “no longer exists
      in the worktree” (`internal/app/blame.go`). Log resolves against HEAD,
      so a symbol deleted from disk but still in `HEAD` keeps history via
      `rgit log` but not blame (`docs/USAGE.md` § Blame / § Log).

      **Packages / files:** `internal/app/blame.go`, `internal/app/shared.go`
      (`resolveAnchorExtent` content loader), `internal/gitx/gitx.go`
      (`Blame` / `CatFile`), `docs/USAGE.md`, `docs/LIMITATIONS.md` if the
      asymmetry is currently documented as permanent.

      **Traps:** `git blame -L` needs a path git can see — typically
      `git blame HEAD -- path` (or equivalent) once the extent comes from the
      HEAD blob. Line range must come from the **same** blob blamed, not a
      stale worktree parse. Do not silently blame the wrong revision when the
      worktree file exists but differs. LSP cross-check on a HEAD-only blob
      is out of scope for blame (read-only git delegation). Absorbs the
      shebang-from-HEAD work on `shared.go` if both land in one wave — one
      owner for that file.

      **Acceptance criteria:** Fixture: delete `auth.go` from the worktree
      while `ValidateToken` remains in `HEAD` — `rgit blame
      auth.go:ValidateToken` succeeds with git blame output for that
      extent. Existing worktree blame behaviour unchanged when the file is
      present.

- [ ] **`--follow-rename` on `rgit blame`.** Log gained rename-boundary
      re-resolution (`internal/app/log.go`, `gitx.FindRename`); blame has no
      equivalent flag surface (`rgitBlameFlags` is only `-p/--porcelain`).
      Same rename+reorder failure mode `docs/LIMITATIONS.md` § History
      across renames documents for `git log -L`.

      **Packages / files:** `internal/app/blame.go`, `internal/app/completion.go`
      (`rgitBlameFlags`), `internal/gitx/gitx.go`, `docs/USAGE.md`,
      `docs/LIMITATIONS.md`.

      **Traps:** Blame is worktree-scoped today — combine carefully with the
      HEAD-fallback item above; do not invent a second attribution path.
      Porcelain format must stay git's own. Prefer reusing `FindRename` +
      per-boundary resolve, matching log's “one parse per rename, not per
      commit” trade-off (`specs/design.md`).

      **Acceptance criteria:** Fixture with rename + in-file reorder:
      `rgit blame new.go:Foo --follow-rename` attributes across the rename
      where plain blame stops. Without the flag, behaviour matches today.
      Completion / help list the new flag; drift test updated.

- [ ] **Document or tighten `--max-count` under `--follow-rename`.** With
      `--follow-rename`, `-n`/`--max-count` applies per `git log -L` segment,
      not as a global cap across rename boundaries. Docs already say bounds
      apply per segment; call out the surprise explicitly, or change to a
      global remaining budget if that is preferred.

      **Packages / files:** `internal/app/log.go`, `docs/USAGE.md`.

- [ ] **Clearer error for lone `--` as log anchor positional.**
      `parseLogAnchorArgs` can leave positional=`--`, which fails later at
      resolve with an opaque message.

      **Packages / files:** `internal/app/log.go`.

## Resolution / LSP

- [ ] **Shebang sniff from the HEAD blob for extensionless paths.**
      `PeekShebangLine` / `LanguageForWorktreePath` read the worktree only
      (`internal/resolve/lang.go`); a deleted extensionless hook/script loses
      grammar routing and collapses to exit 9 or whole-file attribution.
      `CatFileSample` already exists for bounded blob peeks
      (`internal/gitx/gitx.go`).

      **Packages / files:** `internal/resolve/lang.go`, callers in
      `internal/diff/run.go`, `internal/synth/stage.go`,
      `internal/app/shared.go`, `docs/ANCHORS.md`, `specs/design.md` §
      Grammar scope (shebang subsection).

      **Traps:** Bound the HEAD peek to `shebangPeekBytes` (256). Extension
      still wins over shebang. Do not change mapped interpreters or re-admit
      `zsh`. Binary / missing-blob handling must match `CatFileSample`'s
      exists convention. Worktree present → keep today's worktree peek.
      Shares `shared.go` with blame HEAD-fallback — serialize or one owner.

      **Acceptance criteria:** Extensionless `#!/usr/bin/env bash` (or
      `node`) file deleted from the worktree but present in `HEAD`:
      `rgit diff --sym` / `rgit log hooks/pre-commit:fn` still route via the
      correct grammar. Extensionless unmapped shebang still refuses exit 9.

## Docs

- [ ] **`HUMANS.md` mention of context `W` records and anchor log date
      bounds.** Machine contract (`USAGE`/`CODES`/`CHANGELOG` Unreleased) is
      current; the human-facing tier is silent on both.
