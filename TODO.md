# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave landed CODES/HUMANS
context-order doc fixes, symbols/completion unit pins, hand-written help
footer regression coverage, and post-audit should-fixes (wave-10; adversarial
audit closed — no must-fix; should-fixes landed).
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

- [ ] **Broaden m12 completion help pin beyond bash.**
      `TestRun_Completion` pins `completion bash --help` only; `runCompletion`
      treats help wherever it appears for every shell. Optional table over
      `{bash,zsh,fish,pwsh}` × `{--help,-h}`.

      **Packages / files:** `internal/app/app_test.go` (`TestRun_Completion`).

      **Acceptance criteria:** `go test -short ./internal/app -run 'TestRun_Completion$'`
      covers help-after-shell for each supported shell.

## Docs / help

- [ ] **HUMANS.md: optional "B sorts first" clause.** Wave-10 context prose
      documents `B → W → F → C` but omits that `B` is always first when present
      (`docs/CODES.md`, `docs/USAGE.md`).

      **Packages / files:** `HUMANS.md`.

      **Acceptance criteria:** one clause matching CODES/USAGE without restating
      the full record grammar.

- [ ] **CHANGELOG context stream order drift.** Older release-note wording may
      still describe commits-before-diff for `rgit context`; not touched in
      wave-10.

      **Packages / files:** `CHANGELOG.md` (spot-check historical entries only if
      still user-facing as current behaviour).

      **Acceptance criteria:** no present-tense claim that contradicts live
      `B → W → F → C` order.

## Tests

- [ ] **Extend Full-reference footer pin to diff/commit/top-level help.**
      `TestHandWrittenHelpFullReferenceFooter` covers hand-parsed helps only;
      `runDiffHelp` / `runCommitHelp` / top-level already carry the footer.

      **Packages / files:** `internal/app/completion_test.go`.

      **Acceptance criteria:** table includes diff, commit, and top-level with
      `wantBlankLineBefore: false`; `go test -short ./internal/app -run
      'TestHandWrittenHelpFullReferenceFooter'`.

- [ ] **Tighten footer blank-line assertion to suffix.** Current check is
      `strings.Contains(help, "\n\n"+footer)`; prefer a suffix-strict form so a
      mid-body double-newline coincidence cannot pass.

      **Packages / files:** `internal/app/completion_test.go`
      (`TestHandWrittenHelpFullReferenceFooter`).

      **Acceptance criteria:** fails if the footer is not preceded by a blank
      line at end-of-help for completion/symbols.
