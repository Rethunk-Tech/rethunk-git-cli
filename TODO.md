# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave pinned exit 12 in the
exitcode table, exact structured-data refusal stderr, and full help equality
for symbols/context/languages (post-audit should-fix closed — wave-13). E2e
skipped this wave. Deliberately not queued: `rgit restore` (designed and held
back —
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

- [ ] **Doctor `--help` full equality.** `TestRun_DoctorHelpAndUsage` still
      uses `StringContains`; upgrade to `qt.Equals(stdout, doctorHelp)` plus
      empty stderr (mirror wave-13 context/languages).

      **Packages / files:** `internal/app/app_test.go` (or a dedicated
      `doctor_test.go` if keeping `app_test.go` thin).

      **Acceptance criteria:** `go test -short ./internal/app -run
      'TestRun_DoctorHelp'`.

- [ ] **Blame/log `--help` full equality.** `TestRun_BlameHelpAndUsage` /
      `assertAnchorUsageRefusals` pin usage prefixes only; upgrade to
      `blameHelp` / `logHelp` equality plus empty stderr.

      **Packages / files:** `internal/app/blame_test.go`, and the log help
      path that shares the helper.

      **Acceptance criteria:** focused short tests fail unless stdout equals
      the hand-written help const.

- [ ] **Dedup languages `--help` Contains pin.** `TestRun_LanguagesHelpAndUsage`
      in `app_test.go` still Contains-pins usage after
      `TestRun_LanguagesHelpEquality` covers full equality.

      **Packages / files:** `internal/app/app_test.go`.

      **Acceptance criteria:** one help-equality path remains; the weak
      Contains duplicate is gone or reduced to usage-error coverage only.

- [ ] **Structured-data refusal matrix beyond JSON.** Commit exit-12 pin
      covers `package.json` only; YAML/TOML share `IsStructuredData`.

      **Packages / files:** `internal/app/commit_test.go`.

      **Acceptance criteria:** at least one YAML and one TOML `FILE:SYMBOL`
      case refuse with exit 12 and the path-form alternative still succeeds.

- [ ] **`-h` alias pins for hand-parsed helps.** symbols/context/languages
      accept `-h` on the same path as `--help`; wave-13 pinned `--help` only.

      **Packages / files:** `internal/app/symbols_test.go`,
      `context_test.go`, `languages_crosscheck_test.go`.

      **Acceptance criteria:** parallel `-h` cases assert the same equality
      as `--help`.
