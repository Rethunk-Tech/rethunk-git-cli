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

## Performance

- [ ] **Parallelize per-file cross-check queries in `rgit diff`/`commit`
      across changed files.** `internal/diff/run.go`'s file loop
      (`buildFileReport` per entry) runs strictly sequentially, and
      `internal/lsp.Session` is documented "not safe for concurrent use;
      one invocation resolves in sequence" (`internal/lsp/session.go`).
      Every non-`gopls` server (vtsls, pyright, bash-language-server,
      yaml-language-server, the `vscode-langservers-extracted` trio,
      marksman) has no daemon — each cross-checked file pays a fresh
      subprocess spawn and LSP handshake, one at a time, even though these
      queries are independent of each other. The git-backed blob reads
      already got this treatment (`prefetchBlobs`/`BatchCatFile`, one
      batched `cat-file` call instead of one per file); the per-file
      cross-check is the remaining sequential cost on a commit touching
      many files across non-daemon languages.

      **Packages / files:** `internal/diff/run.go` (the loop at its
      per-entry `buildFileReport` call), `internal/lsp/session.go`
      (`Session.Dial`'s `clients`/`degraded` maps, currently unguarded).

      **Traps:** `Session.Dial`'s two maps are plain, unsynchronized `map[string]*Client`/
      `map[string]bool` — naive parallelization races on first dial of any
      language with more than one changed file. `gopls` is a shared daemon
      (`internal/lsp/dial.go`'s spawn lock already handles concurrent
      *invocations* of `rgit` itself dialling it, not concurrent goroutines
      *within* one invocation reusing the same `*Client`) — check
      `Client`'s own concurrency contract before assuming a cached `gopls`
      client is safe to reuse from multiple goroutines simultaneously.
      Report ordering is a promise (`internal/diff/run.go`'s
      `sortReport`/CODES.md's output-record ordering guarantee) — parallel
      completion must not leak into output order; collect into
      per-index slots and sort/assign after the fan-out, not append as
      goroutines finish. Bound concurrency rather than spawning one
      goroutine per changed file unconditionally — a commit touching
      hundreds of files must not launch hundreds of simultaneous LSP
      subprocesses.

      **Acceptance criteria:** `internal/diff/bench_test.go` gains a
      benchmark (or an existing one is extended) measuring wall-clock on a
      fixture with many changed files across at least two non-daemon
      languages, showing a real reduction against the sequential baseline —
      not just a code-shape change. `-race ./internal/lsp/...` (already the
      CI race job's scope, `.github/workflows/ci.yml`) stays clean. Output
      ordering and content are byte-identical to the sequential path on the
      existing `internal/diff` test fixtures.
