# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave landed
`rgit completion pwsh`, `rgit blame --follow-rename`, Windows CI cosign
exercise for `install.ps1`, and a dedicated `USAGE.md` Symbols section
(plus wave-audit should-fix closeout).
Deliberately not queued: `rgit restore` (designed and held back —
[`specs/design.md`](specs/design.md#rgit-restore-filesymbol-an-accepted-design-deliberately-not-built)),
context staged/unstaged split, TOML taplo / SQL LSP cross-checks, Rust/C/C++/
Vue/Svelte grammars, SCSS/zsh, and orphan-gopls handshake cleanup.

## Docs / design record

- [ ] **Record `blame --follow-rename` in `specs/design.md`.** Log's
      rename-boundary section still contrasts HEAD resolution as “unlike
      `blame`” (`specs/design.md` § `--follow-rename`); blame now uses HEAD
      under the flag (`internal/app/blame.go`). Add a short blame entry and
      retarget that contrast so the design record matches shipped behaviour.
      Docs in `USAGE.md` / `LIMITATIONS.md` already describe the HEAD-blob
      rule — this is the design-record lag only.
