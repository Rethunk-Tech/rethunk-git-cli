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

## Resolution

- [ ] **Wire HTML LSP cross-check.** `vscode-html-language-server` matches
      `rgit`'s ranges for bare `tag#id` elements, but the cross-check stays
      unwired because tree-sitter-html's void-element nodes absorb trailing
      whitespace/text past the tag (`internal/resolve/lang_html.go`,
      `specs/design.md` § Grammar scope, `docs/LIMITATIONS.md` § Language-server
      coverage). A second, non-blocking gap: class-bearing elements are named
      `tag#id.class1.class2` by the server, never matching `tag#id` — that case
      can degrade to `[ts-only]` without blocking wiring for id-only elements.

      **Packages / files:** `internal/resolve/extent.go` (`declOnlyExtent`,
      `extentEnd`, `trailingCommentTrimmer` seam), `internal/resolve/lang_html.go`,
      `internal/lsp/servers.go` (HTML server entry), `internal/lsp/wiring_test.go`,
      `internal/resolve/crosscheck.go`, `docs/LIMITATIONS.md`, `docs/INSTALL.md`.

      **Traps:** YAML's `trailingCommentTrimmer` trims the wrong edge for a
      different defect — do not copy it blindly. Extending `declOnlyExtent` touches
      **every** wired grammar's cross-check path; the trim seam must be optional
      per-adapter. Void-element absorption affects `<input>`, `<img>`, `<br>`, etc.
      Class-name normalization alone does not unblock void elements. Do not widen
      HTML anchor scope (no class selectors).

      **Acceptance criteria:** With `vscode-html-language-server` installed and
      reachable, `rgit commit` on a fixture `div#app` element cross-checks without
      `[ts-only]` and without exit 6. Void-element fixtures with id still
      cross-check after the trim seam. Class-bearing `div#app.widget` continues
      to degrade to `[ts-only]` (safe). `rgit doctor` reports HTML server status.
      Design record and `docs/LIMITATIONS.md` updated to reflect wired status.
