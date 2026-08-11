# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave pinned full help
equality for doctor/blame/log (including path-scoped log help), languages
Contains dedup, YAML/TOML structured-data exit-12 refusals, and `-h` alias
equality for context/symbols/languages (post-audit should-fix closed —
wave-14). E2e skipped this wave. Deliberately not queued: `rgit restore`
(designed and held back —
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

- [ ] **`assertAnchorUsageRefusals` help empty-stderr.** The shared refusal
      matrix still Contains-pins `--help` stdout and discards stderr; dedicated
      equality tests cover full pins, but a regression that leaked help to
      stderr would slip if those equality tests were removed.

      **Packages / files:** `internal/app/blame_test.go`
      (`assertAnchorUsageRefusals`).

      **Acceptance criteria:** the shared `--help` subtest asserts empty
      stderr (and ideally keeps the weak usage-prefix Contains).

- [ ] **Structured-data refusal depth.** Wave-14 table-drives JSON/YAML/TOML
      via `runCommit` with `.yaml` only; `IsStructuredData` never runs the
      resolver, so fixture keys are illustrative. Optional deepenings: `.yml`
      extension, a `runApp(t, "commit", …)` dispatch pin, and/or a positive
      that a resolvable key would have been addressable absent the guard.

      **Packages / files:** `internal/app/commit_test.go`.

      **Acceptance criteria:** at least one additional path (`.yml`, `runApp`,
      or resolvable-key proof) fails unless the guard still refuses exit 12
      with the exact stderr template and path-form commit still succeeds.

- [ ] **Help-pin style / doctor usage strength.** Cross-file help tests mix
      `qt` loops, nested `t.Run`, and `t.Fatalf`; doctor usage-error still
      only checks non-empty stderr while symbols pins exact wording. Uniformity
      and a stronger doctor usage pin are polish, not coverage gaps.

      **Packages / files:** `internal/app/doctor_test.go`,
      `context_test.go`, `symbols_test.go`, `languages_crosscheck_test.go`,
      `blame_test.go`, `log_test.go`.

      **Acceptance criteria:** doctor `"extra"` asserts the exact usage
      stderr shape (mirror symbols); optional follow-on normalizes help-test
      scaffolding without changing the equality contracts.
