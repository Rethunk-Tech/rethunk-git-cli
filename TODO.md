# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave landed completion
help-after-shell pins for all shells, HUMANS/CHANGELOG context-order
clarifications (including B-first and W-in-order), Full-reference footer
coverage for diff/commit/top-level plus suffix-strict blank-line pins on
hand-written helps, and post-audit should-fixes (wave-11; adversarial audit
closed — no must-fix; should-fixes landed). E2e skipped this wave.
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

- [ ] **Strengthen m12 help-after-shell pin signal.** Wave-11 table covers
      `{bash,zsh,fish,pwsh}` × `{--help,-h}` but asserts only exit 0 plus two
      substrings. Optional: pin empty stderr and/or fuller `completionHelp`
      equality / per-shell token.

      **Packages / files:** `internal/app/app_test.go` (`TestRun_Completion`).

      **Acceptance criteria:** a help-path regression that still prints a
      partial usage string fails the focused short test.

## Tests

- [ ] **Tighten footer pin for pflag/top-level helps beyond Contains.**
      Diff/commit/top-level rows in `TestHandWrittenHelpFullReferenceFooter`
      still use `strings.Contains` only (`wantBlankLineBefore: false`). Low
      risk while the footer string is unique; a mid-body coincidence would
      still pass.

      **Packages / files:** `internal/app/completion_test.go`.

      **Acceptance criteria:** assertion fails unless the footer appears in the
      help's expected terminal position for those rows (without requiring the
      hand-written blank-line suffix layout).
