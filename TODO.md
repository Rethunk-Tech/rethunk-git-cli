# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave landed USAGE Flags /
Help alignment (`languages --in-repo`, doctor, blame `-p`, log extras,
`symbols --help`), top-level help naming doctor, README meta `symbols`,
`symbolsHelp` Full-reference footer, context/blame comment corrections, and
wave-audit should-fix closeout (Flags opener no longer claims only
commit/diff have a flag surface).
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

## Docs

- [ ] **`completionHelp` missing Full-reference footer.** Every other
      hand-parsed subcommand help ends with `Full reference: docs/USAGE.md`;
      `internal/app/completion.go`'s `completionHelp` stops at the INSTALL
      line. Append the same footer.

      **Packages / files:** `internal/app/completion.go`

      **Acceptance criteria:** `rgit completion --help` prints the footer;
      siblings remain unchanged.

- [ ] **`symbolsHelp` Full-reference glued to `--for-commit` line.**
      Siblings put a blank line before `Full reference: docs/USAGE.md`;
      `internal/app/symbols.go` does not. Insert the blank line.

      **Packages / files:** `internal/app/symbols.go`

      **Acceptance criteria:** `symbolsHelp` matches sibling footer spacing.
