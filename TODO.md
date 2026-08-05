# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

## Synthesis

- [ ] **Generalize separator ownership beyond the new-file preamble.** Today
      `resolve.ExtendThroughOwnedSeparator` (`internal/resolve/pseudo.go`) is
      called only from `internal/synth/stage.go`'s `addPreamble` for `@header`
      and `@imports` on brand-new files. Go is the only adapter with
      `OwnsTrailingSeparator() == true` (`internal/resolve/lang_go.go`). The
      design record verified the rule holds for any chain of adjacent top-level
      declarations — each non-final region absorbing its own trailing gap sums
      exactly, since the final region's missing trailing newline offsets the
      file's own EOF terminator (`specs/design.md` § Blob synthesis) — but
      adopting it for ordinary (tracked-file) commits would change the general
      insertion path, not just the preamble case.

      **Packages / files:** `internal/resolve/pseudo.go`
      (`ExtendThroughOwnedSeparator`, `headerExtent`/`importsExtent` callers),
      `internal/resolve/lang.go` (`OwnsTrailingSeparator`), `internal/synth/`
      (`stage.go` edit planning, `classify.go` insertion boundaries),
      `internal/diff/attribute.go` (symbol row boundaries must stay honest).

      **Traps:** `rgit diff`'s `(unanchorable)` row is the correct answer for
      whitespace between two tracked regions today — widening extents in the
      general path must not silently absorb a neighbour's edits or make diff
      rows sum to more than git's numstat. Only Go benefits; inventing separator
      ownership for Prettier/Black languages would be wrong (`lang_typescript.go`,
      `lang_python.go` both return false with measured reasoning). Any change
      must preserve the byte-identical round trip verified in
      `internal/synth/synth_test.go`.

      **Acceptance criteria:** A new Go file staged via symbol anchors still
      produces preamble rows whose line counts sum to git's raw insertion count
      (existing behaviour). Additionally, staging a single new Go function in a
      tracked file where gofmt's blank line after the preceding declaration is
      part of the authored layout attributes that blank line to the inserted
      symbol's extent (or documents explicitly why not). `rgit diff` on the same
      change does not mis-attribute bytes to the wrong symbol. Unit coverage in
      `internal/synth/`; no regression in `cmd/rgit/index_test.go` preamble
      cases.
