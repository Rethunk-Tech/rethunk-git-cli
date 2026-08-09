# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below come from a multi-round gap survey after the prior TODO queue
drained (darwin release artifacts, fish completion, doctor git-version check,
Vue/Svelte survey, HTML LSP wiring, `--follow-rename`, `rev:path` diff, per-file
cross-check parallelization). Deliberately not queued: `rgit restore` (designed
and held back — [`specs/design.md`](specs/design.md#rgit-restore-filesymbol-an-accepted-design-deliberately-not-built)),
context staged/unstaged split, TOML taplo / SQL LSP cross-checks, Rust/C/C++/
Vue/Svelte grammars, SCSS/zsh, and orphan-gopls handshake cleanup.

## Release / install

- [ ] **Wire darwin download into `scripts/install.sh`.** Darwin
      `rgit-v*-darwin-{amd64,arm64}` artifacts ship from the macOS release job
      (`.github/workflows/release.yml`, `make cross-darwin`), but the install
      script still hard-fails any non-Linux `uname` (`scripts/install.sh`
      lines 23–30). Docs already name the gap
      (`docs/INSTALL.md` § Install script; `docs/LIMITATIONS.md` § Build and
      platform limits).

      **Packages / files:** `scripts/install.sh`, `docs/INSTALL.md`,
      `docs/LIMITATIONS.md`, `.github/workflows/ci.yml` (dry-run / shellcheck
      coverage for the Darwin branch).

      **Traps:** macOS has `shasum -a 256`, not always `sha256sum` — the
      checksum step must be OS-aware or the Darwin path fails after a successful
      download. Asset names must match the release job exactly
      (`rgit-$tag-darwin-arm64`, no `.tar.gz`). Keep Linux behaviour byte-
      identical. Do **not** pretend this POSIX script covers Windows — that is
      a separate `install.ps1` item below.

      **Acceptance criteria:** On a Darwin host (or under a mocked
      `uname`/`arch` in CI), `VERSION=<tag> sh scripts/install.sh --dry-run`
      prints the correct darwin URL and `SHA256SUMS` path; a real install
      lands an executable whose `--version` matches the tag. Linux dry-run and
      install paths are unchanged. `docs/LIMITATIONS.md` / `docs/INSTALL.md`
      no longer say darwin has no download path.

- [ ] **Add `scripts/install.ps1` for windows/amd64 release artifacts.**
      Releases publish `rgit-*-windows-amd64.exe`; there is no first-class
      Windows installer — `install.sh` is Linux-only, and `cmd/rgit-install`
      always builds from source. Distinct from the Darwin `install.sh` work:
      PowerShell, `.exe` naming, and Windows PATH conventions.

      **Packages / files:** `scripts/install.ps1` (new), `docs/INSTALL.md`,
      `docs/LIMITATIONS.md`, CI lint/dry-run for the script if feasible on
      `windows-latest`.

      **Traps:** Asset is `rgit-$tag-windows-amd64.exe` — install must rename
      or symlink to `rgit.exe` on PATH. Checksum verification needs a native
      equivalent of `sha256sum -c` (`Get-FileHash` + compare against the
      matching `SHA256SUMS` line). Do not force users through WSL just to run
      the POSIX script. Keep language-server install out of scope (same as
      `install.sh`).

      **Acceptance criteria:** `.\scripts\install.ps1 -DryRun` prints the
      windows/amd64 download URL and checksum source; a real run installs a
      binary that answers `rgit --version`. Documented beside `install.sh` in
      `docs/INSTALL.md`.

- [ ] **Optional cosign verification in `scripts/install.sh`.** Releases
      already publish `SHA256SUMS.sigstore.json` (keyless cosign over
      `SHA256SUMS`); the script verifies SHA256 integrity only
      (`scripts/install.sh` lines 67–72). `docs/INSTALL.md` documents a
      manual `cosign verify-blob` path that never runs during install.

      **Packages / files:** `scripts/install.sh`, `docs/INSTALL.md`, CI
      dry-run coverage.

      **Traps:** `cosign` is often absent — absence must keep today's
      SHA256-only path (no hard fail). When present, verify the **checksum
      file** with `--bundle SHA256SUMS.sigstore.json`,
      `--certificate-identity-regexp` matching the release workflow, and
      `--certificate-oidc-issuer https://token.actions.githubusercontent.com`
      (same args `docs/INSTALL.md` already prints). Do not invent a second
      identity string that drifts from the workflow path. Network: fetch the
      bundle alongside `SHA256SUMS`.

      **Acceptance criteria:** With `cosign` on `PATH`, install downloads the
      bundle and refuses a tampered `SHA256SUMS` before installing. Without
      `cosign`, behaviour matches today's SHA256-only install. Docs describe
      both paths.

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
      is out of scope for blame (read-only git delegation).

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

- [ ] **Combine `rgit log FILE:SYMBOL` with `--since` / `--until`.** The two
      log shapes are mutually exclusive today: an anchor form, or a
      date-bounded path form selected by `--since`/`--until`
      (`internal/app/log.go`, `docs/USAGE.md` § Log by date and path). Agents
      often want “commits that touched this symbol in the last month” and
      otherwise fall back to plain `git log`.

      **Packages / files:** `internal/app/log.go` (`hasTimeRangeFlag` /
      `runLog` dispatch), `internal/gitx/gitx.go` (`LogLineRange` extra
      args), `docs/USAGE.md`, `docs/CODES.md`.

      **Traps:** `--since` currently *switches* shapes — combining must not
      reclassify positionals as pathspecs when an anchor is present. Forward
      date flags through to `git log -L` rather than inventing a filter.
      Do not reintroduce per-commit re-parse cost. `--porcelain` /
      `-p` exclusivity unchanged. Interaction with `--follow-rename` must be
      defined (both allowed, or documented refusal).

      **Acceptance criteria:** `rgit log auth.go:ValidateToken --since="2
      weeks ago"` returns only touching commits inside the window;
      `--porcelain` shape unchanged. `rgit log --since=… -- path` (no
      anchor) still works. Usage error when the combination is impossible
      stays exit 129 with a clear message.

- [ ] **`-n` / `--max-count` on path-scoped `rgit log --since`.**
      Path-scoped log forwards `since`/`until` only (`runLogPathScoped`);
      unbounded history is a token trap. `rgit context` already bounds
      commits via `RecentCommits` (`-n` 20).

      **Packages / files:** `internal/app/log.go`, `internal/gitx/gitx.go`
      (`Log`), `docs/USAGE.md`, completion flag lists.

      **Traps:** Forward git's own `-n`/`--max-count`; do not truncate after
      the fact in Go. Decide whether the flag also applies to the
      `FILE:SYMBOL` form (likely yes, same forwarding). Keep `--porcelain`
      / `-p` mutual exclusion.

      **Acceptance criteria:** `rgit log --since=2024-01-01 -n 5 -- src`
      emits at most five commits. Help and completion advertise the flag.
      Default with no `-n` remains unbounded (git's own default).

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

      **Acceptance criteria:** Extensionless `#!/usr/bin/env bash` (or
      `node`) file deleted from the worktree but present in `HEAD`:
      `rgit diff --sym` / `rgit log hooks/pre-commit:fn` still route via the
      correct grammar. Extensionless unmapped shebang still refuses exit 9.

- [ ] **Normalize HTML LSP names for class-bearing `tag#id` elements.**
      HTML cross-check is wired, but an element with a `class` attribute is
      named `tag#id.class1.class2` by `vscode-html-language-server` and
      never matches `rgit`'s `tag#id`, degrading that symbol alone to
      `[ts-only]` (`docs/LIMITATIONS.md` § Language-server coverage;
      `specs/design.md` § Grammar scope). Staging extents stay `tag#id` by
      design — this is match normalization only.

      **Packages / files:** `internal/resolve/crosscheck.go`
      (`matchLSPSymbol` / HTML flat path), `internal/resolve/lang_html.go`,
      `internal/lsp/wiring_test.go`, `docs/LIMITATIONS.md`.

      **Traps:** Do **not** widen staged HTML anchors to class selectors
      (`docs/LIMITATIONS.md` / design survey). Match on id-bearing prefix /
      strip `.class…` from the **server** name only. Duplicate ids remain
      exit 4. Void-element `declOnlyEndTrimmer` seam stays independent.

      **Acceptance criteria:** Fixture `<div id="app" class="widget">`
      cross-checks live (no `[ts-only]` for that symbol) with the server
      reachable. Classless `div#app` unchanged. No change to the extent
      `commit` stages.

## Doctor / machine contract

- [ ] **Add a cross-check column to `rgit languages --porcelain`.** Porcelain
      today is `NAME<TAB>EXTENSIONS<TAB>GATED` only (`docs/CODES.md`);
      agents must correlate `doctor`, `LIMITATIONS.md`, and design tables to
      learn which grammars are permanently `[ts-only]` (TOML, SQL) vs wired.
      `doctor --porcelain` deliberately omits grammars.

      **Packages / files:** `internal/app/languages.go`, `internal/lsp/`
      (wired-server table), `docs/CODES.md`, `docs/USAGE.md`,
      `specs/design.md` cross-check coverage table as the source of truth.

      **Traps:** Column values must be stable machine tokens (`wired` /
      `ts-only` / maybe `gated`), not prose. Do not claim a server is
      reachable — that is `doctor`'s job; this column is compile-time /
      design-time wiring, not dial status. Keep human `languages` output
      readable; porcelain is the contract change.

      **Acceptance criteria:** Every porcelain row gains a fourth column
      matching the design record (TOML/SQL → `ts-only`; Go/TS/…/HTML →
      `wired`). Docs updated. Existing three columns unchanged in meaning.

- [ ] **Emit the new commit SHA from `rgit commit --porcelain`.** On
      success, porcelain writes only per-target
      `FILE<TAB>SYMBOL<TAB>ADDED<TAB>DELETED` records and explicitly omits
      the SHA (“one `git rev-parse HEAD` away” — `internal/app/commit.go`).
      Agent pipelines pay an extra subprocess for a fact the human summary
      already prints.

      **Packages / files:** `internal/app/commit.go`, `docs/CODES.md` §
      `rgit commit --porcelain`, `docs/USAGE.md`.

      **Traps:** Keep git's rule that `--porcelain` replaces the human
      summary — add a **structured** record (e.g. leading `H<TAB>SHA`),
      never prose. `--allow-empty` with no staged targets still creates a
      commit: the SHA record must appear even when target rows are empty
      (`docs/CODES.md` already warns empty output ≠ no commit). Dry-run
      must not invent a SHA. Order: document whether `H` precedes or follows
      target rows and pin it.

      **Acceptance criteria:** Successful `rgit commit --porcelain …`
      stdout includes a stable SHA record plus existing target rows; parsers
      documented in `docs/CODES.md`. `--dry-run --porcelain` unchanged
      (preview rows, no SHA). `--quiet` still silent on stdout.

- [ ] **Fold stderr diagnostics into `rgit context`'s record stream.**
      `context` is the flagless first-turn orientation command, but
      `[ts-only]` and diff warnings still go to stderr only
      (`internal/app/context.go`), while agents parse the fixed `B`/`F`/`C`/`X`
      stdout stream. Precedent: `B` was added as a new record kind without
      growing a flag surface (`specs/design.md` § Commands).

      **Packages / files:** `internal/app/context.go`, `internal/diff/`
      (warning / TSOnly surfaces), `docs/CODES.md`, `docs/USAGE.md`,
      `specs/design.md`.

      **Traps:** Do **not** reintroduce a staged/unstaged split (rejected in
      design). New record kinds must stay within the 16 KiB budget and the
      existing truncation / `X` rules — diagnostics should not crowd out `F`
      rows. Keep stderr mirrors optional or drop them only after porcelain
      consumers exist. Container-escalation warnings are `--sym`-only on
      `diff` today; decide whether `context` invents an equivalent signal
      or only folds warnings `Run` already produces.

      **Acceptance criteria:** `rgit context` stdout carries machine-readable
      records for `[ts-only]` and each diff warning previously stderr-only,
      documented in `docs/CODES.md`. Byte budget and `F`-before-`C`
      truncation priority preserved. No new flags.
