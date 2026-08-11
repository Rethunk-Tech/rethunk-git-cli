# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after a fenced wave pinned shared
`assertAnchorUsageRefusals` `--help` empty-stderr, a `.yml` structured-data
refusal row, and exact doctor unrecognized-argument usage stderr (post-audit
clean — wave-15). E2e skipped this wave. Deliberately not queued: `rgit
restore` (designed and held back —
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

- [ ] **Structured-data refusal depth (remainder).** Wave-15 added a
      `config.yml` table row beside JSON/YAML/TOML; the matrix still calls
      `runCommit` directly and fixture keys remain illustrative because
      `IsStructuredData` never runs the resolver. Optional deepenings: a
      `runApp(t, "commit", …)` dispatch pin, and/or a positive that a
      resolvable key would have been addressable absent the guard.

      **Packages / files:** `internal/app/commit_test.go`.

      **Acceptance criteria:** at least one additional path (`runApp` or
      resolvable-key proof) fails unless the guard still refuses exit 12
      with the exact stderr template and path-form commit still succeeds.

- [ ] **Help-pin scaffolding uniformity.** Doctor `"extra"` now pins exact
      usage stderr; cross-file help tests still mix `qt` loops, nested
      `t.Run`, and `t.Fatalf`. Optional: extend the shared refusal matrix
      empty-stderr pin from `--help` alone to also cover `-h` (dedicated
      equality tests already pin `-h`).

      **Packages / files:** `internal/app/doctor_test.go`,
      `context_test.go`, `symbols_test.go`, `languages_crosscheck_test.go`,
      `blame_test.go`, `log_test.go`.

      **Acceptance criteria:** help-test scaffolding is normalized without
      changing the equality contracts; optional shared-matrix `-h` empty
      stderr if not already covered by dedicated equality tests alone.
