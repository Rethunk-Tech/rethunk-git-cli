# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave aligned
`runCommit` structured-data pins with the `runApp` sibling (empty stdout on
refuse; path listing + HEAD content on success), hoisted help-only `Chdir`
outside the shared-anchor and context help flag loops, and moved
`containsString` into `test_helpers_test.go` (post-audit CLEAN — no
must/should — wave-18). E2e skipped this wave. Deliberately not queued:
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

- [ ] **Languages usage-only help path (optional residual).**
      `TestRun_LanguagesHelpAndUsage` in `app_test.go` was outside the
      wave-16 help-scaffold fence; already qt, not an equality pin.

      **Packages / files:** `internal/app/app_test.go`.

      **Acceptance criteria:** only if a future sweep wants every help
      entrypoint named in one matrix — not required for contract parity.

- [ ] **Per-format HEAD content pins on structured-data `runCommit` (optional).**
      Table success path asserts `strings.Contains(got, "after")` for every
      format; the `runApp` sibling pins `` `"after"` `` for JSON only.
      Contract-intentional for wave-18; tighten only if per-format blobs
      need distinct substrings.

      **Packages / files:** `internal/app/commit_test.go`
      (`TestRunCommit_RefusesSymbolAnchorOnStructuredData`).

      **Acceptance criteria:** each table case pins a format-appropriate
      HEAD substring (or stays on the shared `after` token with a brief
      comment why that is enough).

- [ ] **Pin empty stderr on structured-data `runCommit` path success (optional).**
      Refuse path pins empty stdout; path-success still leaves stderr
      unpinned. Mirror the `runApp` sibling or pin `stderr == ""` if that
      path must never emit warnings.

      **Packages / files:** `internal/app/commit_test.go`
      (`TestRunCommit_RefusesSymbolAnchorOnStructuredData`).

      **Acceptance criteria:** path-success asserts empty stderr (or
      documents why warnings remain allowed).
