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
