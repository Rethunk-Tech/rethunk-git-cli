# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

## Release / cross-build

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
