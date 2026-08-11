# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave pinned exact
languages unrecognized-argument usage stderr, and tightened structured-data
`runCommit` path-success pins (per-format HEAD via `TrimRight(tc.after)` plus
empty stderr on both the table and runApp sibling) (post-audit CLEAN — no
must/should — wave-19). E2e skipped this wave. Deliberately not queued:
`rgit restore` (designed and held back —
[`specs/design.md`](specs/design.md#rgit-restore-filesymbol-an-accepted-design-deliberately-not-built)),
context staged/unstaged split, TOML taplo / SQL LSP cross-checks, Rust/C/C++/
Vue/Svelte grammars, SCSS/zsh, and orphan-gopls handshake cleanup.

## Completion / tooling

- [ ] **pwsh e2e completion pins (symbols + silent degrade).** bash/zsh/fish
      already have dynamic `FILE:SYMBOL` e2e coverage in
      `cmd/rgit/rgit_e2e_test.go`; pwsh only has parser validation in
      `internal/app/app_test.go`. Optional sibling: one bash/fish case that
      `symbols --` offers `--for-commit`.

      **Packages / files:** `cmd/rgit/rgit_e2e_test.go` (mirror fish/bash
      patterns), optionally `CONTRIBUTING.md` e2e table once pwsh is real.

      **Acceptance criteria:** `go test ./cmd/rgit -run 'TestCompletion_Pwsh'`
      (or equivalent) proves symbol completion and silent failure outside a
      repo; CONTRIBUTING may then list pwsh beside bash/zsh/fish.

## Tests

- [ ] **Align runApp structured-data HEAD pin with trimmed fixture (optional).**
      Table path-success now uses `strings.Contains(got, strings.TrimRight(tc.after, "\n"))`;
      `TestRun_CommitRefusesStructuredDataSymbolViaRunApp` still checks only
      `` `"after"` `` for JSON.

      **Packages / files:** `internal/app/commit_test.go`
      (`TestRun_CommitRefusesStructuredDataSymbolViaRunApp`).

      **Acceptance criteria:** path-success HEAD Contains the trimmed JSON
      fixture blob (or documents why the quoted token alone is enough).
