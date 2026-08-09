# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave landed optional
cosign verification in `scripts/install.ps1`, a Linux CI mock for the
cosign install path, `rgit symbols` + full-declaration shell completion,
`rgit blame` HEAD fallback, shebang sniff from HEAD, log lone-`--` clarity,
per-segment `--max-count` docs under `--follow-rename`, and `HUMANS.md`
notes for context `W` plus anchor log date bounds.
Deliberately not queued: `rgit restore` (designed and held back —
[`specs/design.md`](specs/design.md#rgit-restore-filesymbol-an-accepted-design-deliberately-not-built)),
context staged/unstaged split, TOML taplo / SQL LSP cross-checks, Rust/C/C++/
Vue/Svelte grammars, SCSS/zsh, and orphan-gopls handshake cleanup.

## Completion / tooling

- [ ] **Add `rgit completion pwsh` (PowerShell).** Completion ships for
      bash, zsh, and fish only (`internal/app/completion.go`); windows/amd64
      is a published target with no shell UX. PowerShell's native path is
      `Register-ArgumentCompleter -Native`, not a `compgen` port. Symbol
      candidates should call `rgit symbols` / `rgit symbols --for-commit`
      (already shipped), not a fourth copy of porcelain filtering.

      **Packages / files:** `internal/app/completion.go` (new script +
      switch arm), `internal/app/completion_test.go` (flag-list drift guard),
      `docs/USAGE.md`, `docs/INSTALL.md`.

      **Traps:** Native completer scriptblock receives
      `$wordToComplete`, `$commandAst`, `$cursorPosition` — parse the AST for
      subcommand / `-C` / `FILE:` the way bash walks `COMP_WORDS`, do not
      assume argv-style splitting. Emit
      `System.Management.Automation.CompletionResult` objects. Windows
      PowerShell 5.1 vs PowerShell 7 differences in completer registration.

      **Acceptance criteria:** `rgit completion pwsh` prints a script that,
      registered in PowerShell 7, offers subcommands, per-subcommand flags,
      and `FILE:SYMBOL` candidates for `diff`/`commit`/`blame`/`log`.
      Flag-list drift test covers pwsh the same as bash/zsh/fish. Unknown
      shell error text lists `pwsh` among supported shells.

## Blame / log

- [ ] **`--follow-rename` on `rgit blame`.** Log gained rename-boundary
      re-resolution (`internal/app/log.go`, `gitx.FindRename`); blame has no
      equivalent flag surface (`rgitBlameFlags` is only `-p/--porcelain`).
      Same rename+reorder failure mode `docs/LIMITATIONS.md` § History
      across renames documents for `git log -L`. Blame already falls back to
      HEAD when the worktree file is gone — combine carefully; do not invent
      a second attribution path.

      **Packages / files:** `internal/app/blame.go`, `internal/app/completion.go`
      (`rgitBlameFlags`), `internal/gitx/gitx.go`, `docs/USAGE.md`,
      `docs/LIMITATIONS.md`.

      **Traps:** Porcelain format must stay git's own. Prefer reusing
      `FindRename` + per-boundary resolve, matching log's “one parse per
      rename, not per commit” trade-off (`specs/design.md`).

      **Acceptance criteria:** Fixture with rename + in-file reorder:
      `rgit blame new.go:Foo --follow-rename` attributes across the rename
      where plain blame stops. Without the flag, behaviour matches today.
      Completion / help list the new flag; drift test updated.

## Release / install

- [ ] **Windows CI cosign-branch exercise for `install.ps1`.** Linux
      `install-script` mocks cosign + fixture bundle; the Windows job still
      only `-DryRun`. Optional polish: align dry-run cosign plan wording
      between `install.sh` and `install.ps1` (identity args already match).

      **Packages / files:** `.github/workflows/ci.yml`, optionally
      `scripts/install.ps1` / `scripts/install.sh` dry-run strings.

## Docs

- [ ] **Dedicated `USAGE.md` § for `rgit symbols`.** Shell completion
      documents the enumerator; there is no top-level command section yet
      (flags, `--for-commit`, exit behaviour). Optional: note `symbols` in
      the `HUMANS.md` command inventory beside `completion`.
