# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

## Release / cross-build

- [ ] **Build and publish darwin/amd64 and darwin/arm64 release artifacts via
      a macOS CI runner.** `make cross`'s matrix is deliberately
      linux/amd64, linux/arm64, windows/amd64 only — darwin fails at zig
      link time without a real macOS SDK
      (`docs/INSTALL.md#cross-builds`: `unable to find dynamic system
      library 'resolv'`). `docs/INSTALL.md` already names the fix: "Build
      darwin binaries on a Mac, or in CI with a macOS runner." No cgo
      cross-toolchain problem to solve — a native `runs-on: macos-latest`
      job sidesteps zig entirely by compiling with Apple's own toolchain.

      **Packages / files:** `.github/workflows/release.yml` (new job
      alongside `release`), `Makefile` (`cross`, `cross-linux-amd64` etc. —
      a native darwin build wants its own non-zig target, not a `CC=zig cc`
      variant), `docs/INSTALL.md#cross-builds`, `docs/LIMITATIONS.md#build-and-platform-limits`.

      **Traps:** the existing `release` job's SQL verification step greps
      `languages | grep -q '^sql'` only for the linux/amd64 binary — a new
      darwin job needs the tree-sitter CLI (`npm install -g tree-sitter-cli`)
      installed too or it silently ships SQL-less. `SHA256SUMS` is written
      once by `make cross`/`sha256sum` over `dist/*` — a separate job
      building into its own `dist/` needs its own checksums merged into the
      release, not overwriting the linux/windows ones. `git describe --tags`
      needs `fetch-depth: 0` on the new job too, or darwin binaries version
      as a bare SHA.

      **Acceptance criteria:** tagging `vX.Y.Z` publishes
      `rgit-vX.Y.Z-darwin-amd64` and `rgit-vX.Y.Z-darwin-arm64` alongside the
      existing three artifacts, each present in `SHA256SUMS` and covered by
      the cosign signature over that file. `docs/LIMITATIONS.md`'s "not
      cross-built" line is removed or corrected. A smoke test (`--version`)
      runs on the `macos-latest` runner itself, the same way linux/amd64
      already gets one.

- [ ] **Smoke-test the windows/amd64 and linux/arm64 release artifacts, not
      just linux/amd64.** `.github/workflows/release.yml`'s `verify the
      linux/amd64 artifact carries SQL` step execs that one binary directly
      because the `ubuntu-latest` runner can run it natively; the
      windows/amd64 and linux/arm64 binaries `make cross` also produces are
      published with no execution check at all — a broken cross-build
      (wrong `CC` target, a linker regression) would only surface from a
      user's own machine.

      **Packages / files:** `.github/workflows/release.yml`.

      **Traps:** `ubuntu-latest` cannot exec a foreign-arch or foreign-OS
      binary directly — linux/arm64 needs `docker/setup-qemu-action` (or
      equivalent binfmt registration) and windows/amd64 needs Wine
      (`wine64 rgit-*.exe --version`) installed on the runner first. Keep
      the check to `--version`/`languages`, the same bar the existing
      linux/amd64 step clears — a full `go test` run under emulation is a
      much larger, flakier ask and isn't what this item is for.

      **Acceptance criteria:** `make cross`'s three artifacts each run
      `--version` and `languages | grep -q '^sql'` successfully in
      `release.yml` before signing; a broken cross-build fails the release
      the same way a missing SQL grammar already does today for
      linux/amd64.

## Tooling

- [ ] **Add `rgit completion fish`.** `internal/app/completion.go` only
      emits bash and zsh (`docs/USAGE.md#shell-completion`,
      `CONTRIBUTING.md`'s own test-lane table calls out "real bash and
      zsh"). fish is a common third shell with its own completion DSL
      (`complete -c rgit -n ...`), distinct enough from bash/zsh's
      `compgen`/`compadd` model that it needs its own script, not a
      translation.

      **Packages / files:** `internal/app/completion.go` (new
      `fishCompletionScript` const, `runCompletion`'s switch),
      `internal/app/completion_test.go` (`TestCompletionFlags_MatchLiveFlagSets`
      currently only checks bash/zsh's flag lists against live `--help`
      output — fish needs the same drift guard, not a hand-copied list),
      `docs/USAGE.md#shell-completion`, `docs/INSTALL.md#shell-completion`.

      **Traps:** the dynamic `FILE:SYMBOL` completion (shelling out to
      `rgit diff --porcelain` and cutting the SYMBOL column) has to be
      reimplemented in fish's function syntax, not ported line-by-line from
      the bash version — fish has no `compgen`/`COMPREPLY`. `rgitSubcommands`,
      `rgitDiffFlags`, `rgitCommitFlags`, etc. are already shared string
      constants; reuse them rather than hand-duplicating the flag lists a
      third time. `-C <path>` sits before the subcommand the same way it
      does for bash/zsh — fish completion must walk past it too or the
      subcommand-conditioned (`-n`) completions never fire.

      **Acceptance criteria:** `rgit completion fish` prints a script that,
      loaded in a real fish shell, offers subcommands, per-subcommand
      flags, and `FILE:SYMBOL` completion for `diff`/`commit`/`blame`/`log`
      the same way the bash/zsh scripts do. A `TestCompletionFlags_MatchLiveFlagSets`-style
      case guards fish's flag lists the same as the other two shells. CI's
      e2e lane gets a fish-presence-gated smoke case matching the existing
      bash/zsh ones (skips cleanly when fish is absent from the runner).

## Doctor / prerequisites

- [ ] **`rgit doctor` checks that git is present but never checks its
      version.** `internal/prereq.LookPath` (`internal/prereq/prereq.go`)
      only probes presence via `exec.LookPath`. `rgit` shells out to git for
      every write and relies on specific flag behaviour (`internal/gitx.go`'s
      `hash-object --path`, `diff --numstat`, `log -L`, sparse-checkout
      advice text) that an old-enough git may not support or may format
      differently — the failure mode today is whatever cryptic error that
      git version happens to produce, not a clear "git too old" from
      `doctor`.

      **Packages / files:** `internal/prereq/prereq.go` (new version-parsing
      helper), `internal/app/doctor.go` (new check row), `internal/gitx/gitx.go`
      (source of truth for which flags are actually relied on), `docs/CODES.md#rgit-doctor---porcelain`
      (new `env` row).

      **Traps:** `git --version` output has shipped multiple formats across
      distros (`git version 2.43.0`, plus a `.windows.1`/Apple-git suffix on
      some platforms) — parse defensively rather than a fixed-position
      split. Determine the actual floor by auditing `internal/gitx` and
      `internal/synth` for the newest flag/behaviour in use, don't guess a
      round number. A version check that's too strict breaks `doctor` on
      still-working older git; keep the check informational (`doctor`
      reports it, doesn't refuse to run anything) matching how a missing
      language server already degrades rather than blocks.

      **Acceptance criteria:** `rgit doctor` gains an `env` row for git's
      resolved version; below the documented floor it reports `MISSING`
      (or a distinct degraded status) with the detected version and the
      floor in `DETAIL`, and `rgit doctor --porcelain` carries the same
      fact machine-readably. The floor itself is recorded once, in
      `docs/INSTALL.md#prerequisites`, not duplicated between the check and
      the docs.

## Resolution

- [ ] **Survey demand for Vue and Svelte single-file-component anchors.**
      Every grammar this repository ships was prioritized by the same
      demand survey over real repositories (`specs/design.md`'s governing
      principle and grammar-scope reasoning) — Rust/C/C++ stayed unsupported
      because none turned up. `.vue`/`.svelte` files are common in
      TypeScript-heavy frontend repos this tool already targets and were not
      part of the original survey's language set; re-run the same
      methodology rather than assuming either a yes or a no.

      **Packages / files:** `specs/design.md#grammar-scope` (record the
      survey and its verdict, same as the existing per-language entries),
      `internal/resolve/grammars.go`, `internal/resolve/lang.go` (registration
      point, if the verdict is yes).

      **Traps:** SFCs are not single-language files — a `.vue` file
      interleaves `<template>`, `<script>`/`<script setup>`, and `<style>`
      blocks, each its own grammar. This does not fit the existing
      one-tree-sitter-language-per-extension model
      (`internal/resolve/lang.go`'s `Adapter` interface) at all; a naive
      "pick one grammar for the extension" answer would be wrong for the
      dominant real-world shape of these files, not merely incomplete. The
      survey has to answer whether composite parsing is worth building
      *before* any resolver code changes, not after.

      **Acceptance criteria:** `specs/design.md` records the survey
      (repository count and hit rate, same form as the existing grammar
      table's justification column) and its verdict. If the verdict is
      "build it," this item is superseded by a new, separately-scoped TODO
      entry describing the composite-parsing design; if "reject," the
      verdict and reasoning join the other settled rejections in
      `specs/design.md` and this entry is simply deleted.
