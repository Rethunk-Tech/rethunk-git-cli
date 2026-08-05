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

## LSP

- [ ] **Orphan daemon cleanup on handshake failure.** When a managed socket
      answers but the LSP handshake fails, `internal/lsp/dial.go` unlinks the
      path so the next invocation can respawn — but the old process may still
      listen on the stranded inode until `-listen.timeout` idle shutdown
      (`internal/lsp/servers.go` `daemonArgs`). Accepted gap in
      `specs/design.md` § Resolution model.

      **Packages / files:** `internal/lsp/dial.go`, `internal/lsp/client.go`,
      `internal/lsp/servers.go`, `internal/lsp/dial_test.go`, `specs/design.md`.

      **Traps:** No server wired into `Dial` exposes shutdown RPC or PID today
      — any fix must discover a portable signal (e.g. pidfile written at spawn,
      `lsp` shutdown where supported) without killing unrelated processes. Never
      unlink or signal a user-supplied `$RGIT_LSP_SOCKET`. `owner_windows.go`
      makes `sameOwner` a no-op — behaviour may differ by platform. Handshake
      failure deep into `dialBudget+queryDeadline` is evidence of a stuck daemon,
      but not proof — avoid killing a slow-but-healthy server on a loaded machine.

      **Acceptance criteria:** Test in `internal/lsp/dial_test.go`: simulated
      handshake failure on a managed socket leaves no listener on the path **and**
      does not leak a process past test timeout (or documents bounded orphan
      lifetime if full kill is impossible on CI). Next dial succeeds without
      waiting for idle timeout. User-supplied socket path untouched. Design
      record updated with chosen mechanism and remaining best-effort bounds.

## Context

- [ ] **Expand `rgit context` beyond commits + diff rows.** The command is
      intentionally flagless and fixed-shape today (`internal/app/context.go`):
      `C` commit records (last 20), `F` rows reused from `rgit diff
      --porcelain`, hard-capped at 16 KiB with an `X TRUNCATED` trailer.
      Agents still need separate calls for branch name, upstream/ahead-behind,
      `[ts-only]` resolution health, or a larger byte budget on huge trees.

      **Packages / files:** `internal/app/context.go` (`buildContextStream`,
      `contextByteBudget`, `contextRecentCommitLimit`), `internal/gitx/gitx.go`
      (branch/tracking primitives if added), `internal/app/doctor.go` (reuse
      health signal, do not duplicate probes), `docs/USAGE.md` § Context,
      `docs/CODES.md` § Output records, `specs/design.md` § `rgit context`.

      **Traps:** The original guardrail rejects a flag surface that turns context
      into "git status with extra steps" — any expansion must stay one call with
      a **fixed** record grammar (new record types, not flags). New records need
      single-letter tags and tab separation like `C`/`F`/`X`. `[ts-only]` belongs
      on stderr today (`tsOnlyNotice`); moving it into the stream is a contract
      change parsers must opt into. Raising `contextByteBudget` affects every
      agent turn — prefer new optional record kinds that appear only when
      relevant (e.g. `B` branch) over unbounded growth. Must still compose over
      `diffpkg.Run`, not re-walk files.

      **Acceptance criteria:** At minimum one new record type documented in
      `docs/CODES.md` (e.g. `B\t<branch>\t<upstream>\t<ahead>\t<behind>` or
      `H\t<ts-only|ok>` for resolution health). Default stream still fits 16 KiB
      in the fixture suite; truncation behaviour unchanged for `F` rows. `rgit
      context` remains flagless. Unit tests for `buildContextStream` cover new
      record types and budget interaction. `docs/USAGE.md` and
      `specs/design.md` updated.

## Diff

## Install / distribution

- [ ] **Distribution packaging beyond raw release binaries.** Today users get
      `dist/rgit-$VERSION-{linux,windows}-*.tar.gz` from `make cross` /
      `.github/workflows/release.yml` and `cmd/rgit-install` for source builds
      (`docs/INSTALL.md`). No Homebrew formula, system package, or curl-to-bash
      installer that also handles `PATH`, shell completion, and optional
      `-with-servers`.

      **Packages / files:** `.github/workflows/release.yml`, `Makefile` (`cross`,
      `install`), `cmd/rgit-install/`, `docs/INSTALL.md`, new packaging metadata
      (e.g. `packaging/homebrew/`, `scripts/install.sh` — exact layout TBD at
      implementation time).

      **Traps:** cgo + tree-sitter means formulas must build from source on the
      target arch or ship per-platform bottles — fat binaries are not an option.
      darwin artifacts are not in `make cross` (SDK limitation per
      `docs/LIMITATIONS.md`) — packaging must not promise macOS binaries the
      release workflow does not produce unless a macOS CI job is added separately.
      `rgit_sql` tag: release linux/amd64 includes SQL per CONTRIBUTING; other
      targets may be SQL-less — document per artifact. Shell completion install
      path differs bash vs zsh (`docs/INSTALL.md` § Shell completion). Keep
      `rgit-install` stdlib-only — heavy logic stays in CI/packaging scripts,
      not the installer's prerequisite chain.

      **Acceptance criteria:** At least one supported distribution path documented
      end-to-end in `docs/INSTALL.md` (e.g. Homebrew tap or verified install
      script with checksum). Installed `rgit` and `rgit --version` work on a
      clean machine with only git (and runtime deps the doc names). Optional
      language-server install remains opt-in, not default. CI verifies the
      packaging metadata (formula lint or install-script dry-run).

## Resolution

## Diff

- [ ] **Batch git blob reads in the diff hot path.** `diff.Run` already uses one
      `git diff --numstat` for enumeration (`internal/gitx/gitx.go`
      `DiffNumstat`), but each changed file calls `scope.Old.read` / `scope.New.read`
      separately in `buildFileReport` (`internal/diff/run.go`) — N files ⇒ N
      `git cat-file` subprocesses (or equivalent). Large attribution runs (whole
      tree, vendor bump) pay linear git overhead.

      **Packages / files:** `internal/gitx/gitx.go` (new batch `cat-file` helper),
      `internal/diff/run.go`, `internal/diff/scope.go` (`side.read`),
      `specs/design.md` § Blob synthesis (held-parse gains are already in-process;
      this is git I/O batching).

      **Traps:** Batch protocol must handle missing blobs (deleted paths, renames
      `old => new`). Scope sides differ: worktree reads may use `os.ReadFile` not
      cat-file — only batch the git-backed sides. Revision-to-revision diffs need
      both revs. Do not break `hash-object --path` invariants in synth (diff is
      read-only). Streaming vs memory: batching loads all changed blobs — cap or
      stream per path count if needed.

      **Acceptance criteria:** Measured reduction in git subprocess count on a
      fixture with ≥10 changed files (test can count `exec` invocations via hook or
      wrapper). Identical `rgit diff --porcelain` output before/after. No regression
      in rename/delete/symlink edge cases covered by `internal/diff/run_test.go`.

## Log / blame

## Repository edges

- [ ] **Submodule and sparse-checkout behaviour audit.** Symbol anchors on
      submodules are refused (exit 10) per `docs/ANCHORS.md` § Paths that anchors
      cannot address; staging uses gitlink SHA from submodule HEAD
      (`internal/synth/special.go`). Sparse checkouts, partial clones, and
      pathspecs that omit populated paths may leave surprising holes — unanchored
      behaviour vs silent whole-file fallback needs a documented matrix.

      **Packages / files:** `internal/synth/special.go`, `internal/cli/precedence.go`
      (path existence checks), `internal/gitx/gitx.go`, `internal/diff/scope.go`,
      `docs/ANCHORS.md`, `docs/LIMITATIONS.md`, `specs/design.md`.

      **Traps:** `git rev-parse` / worktree existence checks differ for sparse
      paths. Submodule in `.gitmodules` but not initialized — index vs worktree
      SHA. `-C` subdirectory repos. Do not promise symbol resolution inside
      submodules without explicit product decision. Fixes must match git's own
      behaviour, not invent semantics (AGENTS.md invariant).

      **Acceptance criteria:** Documented table in `docs/LIMITATIONS.md` or
      `ANCHORS.md`: sparse path absent from worktree, uninitialized submodule,
      symlink to file, nested submodule — for each, `diff`/`commit`/`blame` behaviour
      and exit code. Gaps found during audit become fix tasks or explicit
      limitations. Tests in `internal/synth/special_test.go` or e2e for any
      behaviour change (not documentation-only if fixable).

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

## Diff / context

## Synth / install

- [ ] **Gitattributes and LFS filter audit.** `git hash-object -w --path` is
      mandatory (`internal/gitx/gitx.go` `HashObject`, AGENTS.md invariant) so
      clean/smudge filters run. `cmd/rgit/index_test.go`
      `TestStage_GitattributesCleanFilterRequiresPath` covers a new `.go` file;
      gaps may remain for renames, symlinks, submodules, binary attributes, and
      LFS pointers.

      **Packages / files:** `internal/gitx/gitx.go`, `internal/synth/stage.go`,
      `cmd/rgit/index_test.go`, `docs/LIMITATIONS.md`, `specs/design.md` § Blob
      synthesis.

      **Traps:** Filters can change blob bytes vs worktree read — diff attribution
      compares worktree text; staged blob must match what `git commit` would store.
      LFS smudge on read vs clean on write asymmetry. Submodule gitlinks bypass
      content filters. Do not bypass `--path` for "optimization".

      **Acceptance criteria:** Documented matrix in `LIMITATIONS.md` or INSTALL.md
      for: clean filter on new file, modified file, rename, symlink target, LFS
      tracked extension. Each gap found → test + fix or explicit limitation.
      Existing `TestStage_GitattributesCleanFilterRequiresPath` still passes.

- [ ] **`rgit-install -with-servers` idempotency audit.** Server installs group by
      manager/package (`cmd/rgit-install/servers_install.go` `buildInstallJobs`);
      commands are documented idempotent (`go install`, `npm install -g`). Repeated
      runs, partial failure mid-catalog, and PATH warnings need verification.

      **Packages / files:** `cmd/rgit-install/servers_install.go`,
      `cmd/rgit-install/servers.go`, `cmd/rgit-install/main.go`,
      `cmd/rgit-install/servers_install_test.go`, `docs/INSTALL.md` § Installing
      servers automatically.

      **Traps:** `cargo install` rebuilds can be slow — timeout is 5m (`installTimeout`).
      bun vs npm selection (`selectNPMManager`) may flip between runs. Partial install
      must not leave rgit binary missing when `-with-servers` combined with main
      install. `marksman` is managerNone — unmanaged hint only. Failed job should
      not claim success.

      **Acceptance criteria:** Test or scripted check: two consecutive `-with-servers`
      dry-runs report no duplicate work or error. Simulated failure on job 2 of 3
      leaves job 1 installed and exits non-zero with actionable message. Document
      recovery steps in INSTALL.md. `main_test.go` covers ordering with
      `-generate-only` — extend if gaps found.

## Doctor

- [ ] **Doctor live LSP handshake probe.** `rgit doctor` today checks language
      server binaries with `LookPath` only (`internal/app/doctor.go`) — presence
      on PATH, not whether a daemon answers or completes `initialize`. `rgit diff`
      / commit perform the real cross-check via `internal/lsp/dial.go`.

      **Packages / files:** `internal/app/doctor.go`, `internal/lsp/dial.go`,
      `internal/lsp/client.go`, `internal/prereq/prereq.go`, `docs/USAGE.md`,
      `docs/INSTALL.md` § Language servers.

      **Traps:** Doctor must not spawn daemons or block for seconds on a cold CI
      host — optional quick probe with short timeout, or a separate `--deep` flag
      vs default PATH-only. Missing server stays exit 0 (informational). Do not
      conflate handshake failure with fatal exit. User `$RGIT_LSP_SOCKET` should be
      reported but not unlinked. Overlap with planned `doctor --porcelain` — design
      records together.

      **Acceptance criteria:** Default doctor output distinguishes `on PATH` vs
      `reachable` (handshake ok) vs `degraded` for at least one server (e.g.
      gopls). CI without servers: still exit 0, `[ts-only]` explained. No regression
      in doctor exit code when git missing. Test with `internal/lsptest` mock or
      skipped live probe.

## Performance

- [ ] **Benchmark regression gate for diff attribution.** Design record measured
      ~39× speedup holding a parse open vs re-parsing per declaration on a 200-member
      class (`specs/design.md` § Grammar scope). `resolve.File` and `diff/run.go`
      held-parse path ship, but there is no `testing.B` or CI gate to catch
      regressions.

      **Packages / files:** new `internal/diff/bench_test.go` or
      `internal/resolve/bench_test.go`, `CONTRIBUTING.md` § Tests,
      `.github/workflows/ci.yml` (optional `-bench` job or threshold),
      `specs/design.md` (reference measurement).

      **Traps:** Benchmarks must be `-short` skippable or bounded — CI budget
      `<30s` suite rule. CGO + tree-sitter makes benches machine-noisy; compare
      against baseline ratio, not absolute ms. Do not run benches on every PR if
      flaky — nightly or manual `make bench` acceptable if documented. Fixture must
      be repo-local, not download.

      **Acceptance criteria:** `go test -bench=Attribution -benchmem ./internal/diff/`
      (or chosen name) runs a large-class fixture and logs ns/op. CONTRIBUTING
      documents how to run and interpret. Optional: CI step fails if >2× slower
      than checked-in baseline file — only if stable on ubuntu-latest.
