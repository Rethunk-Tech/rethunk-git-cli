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

## Diff

- [ ] **Support `rev:path` two-blob diff scope.** Rule 3 in
      `internal/cli/precedence.go` already classifies `HEAD:a.go` as
      `KindRevPath`, but `internal/diff/classify.go` refuses it (exit 129) because
      two independent blobs have no single changed file to group symbol rows under
      (`docs/USAGE.md` § Argument shape, `specs/design.md` § Argument grammar).
      Valid git syntax: `git diff HEAD~1:f.go HEAD:f.go`.

      **Packages / files:** `internal/diff/classify.go`, `internal/diff/scope.go`,
      `internal/diff/run.go`, `internal/cli/precedence.go`,
      `internal/gitx/gitx.go` (`CatFile`), `docs/USAGE.md`, `docs/CODES.md`,
      `specs/design.md`.

      **Traps:** `rgit diff`'s report is per-file — two-blob scope needs an
      explicit output shape (likely one synthetic file key or a documented
      porcelain extension). Symbol attribution requires parsing **both** blobs;
      re-use `resolve.Open` per side (`run.go` already holds parses open). Do
      not break the `A..B` / `--range` scope path. A single `rev:path` without a
      paired blob is not this feature — stay refused or document as usage error.
      `--porcelain` record contract in `docs/CODES.md` must be extended, not
      silently changed.

      **Acceptance criteria:** `rgit diff HEAD~1:auth.go HEAD:auth.go` (fixture
      with one symbol changed between revisions) emits per-symbol rows for the
      path, not exit 129. `rgit diff HEAD:a.go` alone remains refused with a
      clear message. Porcelain shape documented and covered in
      `internal/diff/classify_test.go` / `run_test.go`. Help text in
      `docs/USAGE.md` describes the two-blob form and its limits (no pathspec
      magic mixing).

## Log

- [ ] **`rgit log FILE:SYMBOL` across file renames.** Today history is
      `git log -L` bounded to the file's **current** name, resolved once against
      HEAD (`internal/app/log.go`, `docs/LIMITATIONS.md` § History across
      renames). Following a rename correctly requires re-resolving the symbol's
      extent at every commit that could have renamed the file — a parse per
      commit — traded off as rarer than plain history lookup (`specs/design.md` §
      Commands).

      **Packages / files:** `internal/app/log.go`, `internal/gitx/gitx.go`,
      `internal/resolve/` (per-commit `Open` + `Resolve`), `docs/LIMITATIONS.md`,
      `docs/USAGE.md`, `specs/design.md`.

      **Traps:** `git log --follow` does not give symbol extents — cannot delegate
      wholesale. Per-commit resolution must use the blob **at that commit**, not
      HEAD's extent, or `-L` ranges will be wrong after the symbol moved lines.
      Performance: large files × long history × tree-sitter parse — may need a
      `--no-follow` default with opt-in `--follow-rename` rather than changing
      default behaviour silently. Renames without content change still shift path.
      Worktree is irrelevant (log is HEAD-only) — do not read worktree copies.

      **Acceptance criteria:** Fixture: file `old.go:Foo` renamed to `new.go`,
      symbol `Foo` edited before and after rename — `rgit log new.go:Foo
      --follow-rename` (or chosen flag) lists commits touching `Foo` under both
      names, newest first. Without the flag, behaviour matches today's
      HEAD-name-only semantics. Porcelain `HASH<TAB>SUBJECT` shape unchanged.
      Documented limitation lifted or qualified in `docs/LIMITATIONS.md`.

## Release

- [ ] **Signed release artifacts.** `release.yml` publishes `dist/*` with
      `gh release create` and `SHA256SUMS` from `make cross` (`.github/workflows/release.yml`,
      `Makefile`). No minisign/cosign attestations today — consumers verify checksum
      only.

      **Packages / files:** `.github/workflows/release.yml`, `Makefile` (`cross`
      target, `SHA256SUMS`), `docs/INSTALL.md` (verify instructions), `SECURITY.md`
      if key distribution is documented.

      **Traps:** Sigstore/cosign needs OIDC `id-token: write` permission and a
      documented public key or Rekor log for verification. Signing must not break
      existing checksum-only workflow — add signatures alongside, not replace.
      Windows `.exe` and Unix binaries need the same policy. Private fork PRs cannot
      test OIDC fully — document manual verify path.

      **Acceptance criteria:** Each release asset has a detached signature verifiable
      with documented command (`cosign verify-blob` or `minisign -Vm`). `docs/INSTALL.md`
      § Verify updated. CI job fails if signing step fails (no unsigned release on
      tag push). `SHA256SUMS` still published.
