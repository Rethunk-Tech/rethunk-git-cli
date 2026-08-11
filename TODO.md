# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave tightened the
structured-data `runApp` pin (discrete key token, refuse empty stdout,
default path-commit listing + HEAD content — no `--quiet` graft) and
collapsed `assertAnchorUsageRefusals` `--help`/`-h` into one loop
(post-audit should-fixes closed — wave-17). E2e skipped this wave.
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

- [ ] **Align sibling structured-data `runCommit` pins with runApp (optional).**
      `TestRunCommit_RefusesSymbolAnchorOnStructuredData` still pins refuse
      exit + exact stderr only (no empty stdout) and path-success exit only
      (no listing / HEAD content). Wave-17 tightened only the `runApp`
      variant.

      **Packages / files:** `internal/app/commit_test.go`.

      **Acceptance criteria:** refuse pins empty stdout; path success pins
      listing and/or `HEAD:<path>` content without `--quiet`, matching
      `TestRun_CommitRefusesStructuredDataSymbolViaRunApp`.

- [ ] **Languages usage-only help path (optional residual).**
      `TestRun_LanguagesHelpAndUsage` in `app_test.go` was outside the
      wave-16 help-scaffold fence; already qt, not an equality pin.

      **Packages / files:** `internal/app/app_test.go`.

      **Acceptance criteria:** only if a future sweep wants every help
      entrypoint named in one matrix — not required for contract parity.

- [ ] **Hoist help-only `Chdir` outside flag loops (optional hygiene).**
      `assertAnchorUsageRefusals` (and `TestRun_ContextHelpAndUsage`) call
      `t.Chdir(t.TempDir())` inside each `--help`/`-h` subtest; doctor and
      symbols help loops chdir once outside. Not a contract gap.

      **Packages / files:** `internal/app/blame_test.go`, optionally
      `internal/app/context_test.go`.

      **Acceptance criteria:** help-only loops share one tempdir chdir;
      blame/log HelpAndUsage still pass.

- [ ] **Move `containsString` to shared test helper (optional).**
      `commit_test.go` now calls `containsString` defined in
      `symbols_test.go` (same package). Fine today; extract if more commit
      tests adopt it.

      **Packages / files:** new `internal/app/test_helpers_test.go` (or
      similar), `internal/app/symbols_test.go`, callers.

      **Acceptance criteria:** helper has one definition; symbols and
      commit tests still compile and pass.
