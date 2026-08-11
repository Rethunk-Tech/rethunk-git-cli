# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave pinned structured-data
refusal via `runApp` (plus symbols resolvable-key contrast), doctor-style
help loops for context/languages/symbols, and shared
`assertAnchorUsageRefusals` `-h` empty-stderr beside `--help` (post-audit
clean — wave-16; no must/should findings). E2e skipped this wave.
Deliberately not queued: `rgit restore` (designed and held back —
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

- [ ] **Tighten structured-data runApp pin assertions (optional).**
      `TestRun_CommitRefusesStructuredDataSymbolViaRunApp` uses
      `strings.Contains(stdout, "name")` rather than a fields/line-exact
      check, and omits empty stdout/stderr on refuse/path success the way
      some sibling pins do. Style-only godoc on the new test is also absent.

      **Packages / files:** `internal/app/commit_test.go`.

      **Acceptance criteria:** key presence asserted as a discrete symbol
      token (e.g. fields membership); refuse/path outcomes pin empty
      streams where that matches neighbouring commit pins; brief godoc
      states the dispatch + resolvable-key invariant.

- [ ] **Collapse shared help-matrix `--help`/`-h` into one loop (optional).**
      `assertAnchorUsageRefusals` mirrors the two flags as duplicate
      subtests; doctor-style `for` would DRY without changing contracts.

      **Packages / files:** `internal/app/blame_test.go`.

      **Acceptance criteria:** one loop covers `--help` and `-h` with the
      same Success / empty-stderr / `usage: rgit `+cmd assertions; blame
      and log HelpAndUsage still pass.

- [ ] **Languages usage-only help path (optional residual).**
      `TestRun_LanguagesHelpAndUsage` in `app_test.go` was outside the
      wave-16 help-scaffold fence; already qt, not an equality pin.

      **Packages / files:** `internal/app/app_test.go`.

      **Acceptance criteria:** only if a future sweep wants every help
      entrypoint named in one matrix — not required for contract parity.
