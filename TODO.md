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

- [ ] **Expand shebang sniffing for the JS/TS ecosystem — no new grammars.**
      Extensionless scripts whose shebang names a Node/TypeScript runner should
      route to the **existing** TypeScript adapter, not exit 9. Map at minimum:
      `node`, `nodejs`, `tsx`, `ts-node`, and unwrap common launchers (`npx`,
      `bunx`, `bun`) to the interpreter they execute — e.g.
      `#!/usr/bin/env npx tsx` → TypeScript. Also handle `#!/usr/bin/env -S …`
      (`shebangInterpreter` in `internal/resolve/lang.go` currently stops at
      `-S` and leaves the line unmapped).

      **Packages / files:** `internal/resolve/lang.go` (`shebangExtension`,
      `shebangInterpreter`, `ForPath`, `LanguageForWorktreePath`),
      `internal/resolve/lang_test.go`, `docs/ANCHORS.md` § Language support,
      `specs/design.md` § Grammar scope (shebang-sniffing subsection).

      **Traps:** Do **not** add Rust/C/C++/Ruby/Perl grammars — only map to
      adapters already in the binary. `zsh` stays excluded (tree-sitter-bash
      mis-parse). Wrappers like `npx`/`bunx` may carry package subcommands —
      only unwrap when the resolved executable is unambiguously a known TS/JS
      runner; otherwise fall through to honest refusal, not a wrong grammar.
      `PeekShebangLine` reads the worktree only — HEAD-only paths stay
      extension-only. Shebang peek is bounded to 256 bytes (`shebangPeekBytes`).

      **Acceptance criteria:** Fixtures for `#!/usr/bin/env node`, `#!/usr/bin/env
      -S node --import tsx`, `#!/usr/bin/env npx tsx`, and `#!/usr/bin/env bun
      run` on extensionless paths resolve symbols via the TypeScript grammar in
      the unit lane. Unmapped interpreters (e.g. `#!/usr/bin/perl`) still
      refuse with exit 9. `rgit diff` and `rgit commit` on a fixture extensionless
      `.ts` runner script attribute changes by symbol. Docs updated in
      `docs/ANCHORS.md`.

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

- [ ] **Symbol-level `rgit diff` for untracked files.** `buildUntrackedReport`
      (`internal/diff/run.go`) collapses each untracked path to a single
      `(untracked)` row with a line count; it only sets `HintSymbol` to the
      first declaration name as a hint, not per-symbol attribution. Comment
      rationale: symbols do not exist at any revision to diff against — but
      agents staging new files still want `new.go:NewFunc +15/-0` before commit.

      **Packages / files:** `internal/diff/run.go` (`buildUntrackedReport`,
      `Run` untracked path), `internal/diff/render.go`, `internal/diff/types.go`
      (`StatusUntracked`, `HintSymbol`), `internal/resolve/` (`Open`,
      `DeclOrder`), `docs/USAGE.md` § Commands, `docs/CODES.md`.

      **Traps:** There is no HEAD blob — attribution is worktree-only vs empty
      (all lines are insertions). Do not call `git diff --no-index` without
      handling exit 1 on differences. `@header`/`@imports` on a new file overlap
      with preamble staging semantics in `internal/synth/stage.go` — diff rows
      must match what `rgit commit` would stage for the same symbols. Binary
      files stay one row. Unsupported languages stay `(no symbols)` / file-level.
      `rgit context` reuses porcelain rendering — shape must stay compatible.

      **Acceptance criteria:** Untracked `new.go` with two functions emits two
      `F` rows (or porcelain equivalent) with per-symbol `+N/-0`, not one
      collapsed row. `rgit diff --sym new.go:SpecificFunc` filters to that
      symbol. Tracked-file behaviour unchanged. Tests in `internal/diff/run_test.go`.
      USAGE example updated (currently shows single `(untracked)` row).

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

## Doctor

- [ ] **Machine-readable `rgit doctor`.** Output today is human-aligned
      `[ok]`/`MISSING` lines via `internal/prereq.Print` (`internal/app/doctor.go`),
      matching `cmd/rgit-install`'s install-time checks. Agents and CI want a
      stable, parseable stream — same role `rgit languages --porcelain` and
      `rgit diff --porcelain` already fill elsewhere (`docs/CODES.md`).

      **Packages / files:** `internal/app/doctor.go`, `internal/prereq/prereq.go`,
      `internal/lsp/servers.go`, `docs/USAGE.md`, `docs/CODES.md` § Output records,
      `specs/design.md` (if doctor contract is recorded).

      **Traps:** Exit code semantics must stay: non-zero only when rgit cannot
      function (missing git), not when language servers are absent — `[ts-only]` is
      normal. Do not conflate install-time fatal checks (go, CGO, C compiler) with
      run-time doctor. Server rows are dynamic (`lsp.Servers()`); grammar list comes
      from `resolve.Languages()`. A `--porcelain` flag is the likely surface — keep
      default human output unchanged.

      **Acceptance criteria:** `rgit doctor --porcelain` emits one tab-separated
      record per check with stable columns (e.g. `KIND`, `NAME`, `STATUS`, `DETAIL`).
      Documented in `docs/CODES.md`. Missing git → exit 128/129 per existing table;
      missing `gopls` → exit 0 with `MISSING` in the record. `internal/app/doctor_test.go`
      or `lanes_test.go` coverage. Completion/help updated if flag added.

## Resolution

- [ ] **Richer ambiguous-anchor remediation (exit 4 and near-miss exit 3).**
      Exit 4 already lists each colliding symbol's `Qualified` form
      (`internal/resolve/index.go` `resolve`). Exit 3 uses Levenshtein `suggest`
      over qualified names only. AGENTS.md invariant: a bare name that exists only
      inside containers should steer toward qualification, not a misleading
      "unresolved" when the honest answer is "qualify it".

      **Packages / files:** `internal/resolve/index.go` (`resolve`, `suggest`,
      `ResolveError.Error`), `internal/synth/classify.go`, `docs/USAGE.md`,
      `docs/ANCHORS.md` § Qualification, `cmd/rgit/resolver_test.go`.

      **Traps:** Do not change exit codes — 3 vs 4 carry different remediations
      (`docs/CODES.md`). Levenshtein suggestions must not mask true ambiguity.
      Container-qualified forms differ by language (`Declaration.Sep` — CSS uses
      different rules). HTML uses `tag#id`, not `Container.member`. Ordinal forms
      (`#N`) are a last resort, not the primary suggestion.

      **Acceptance criteria:** Fixture: two `Box.size` / `Circle.size` methods —
      bare `size` → exit 4 with candidates `Box.size`, `Circle.size` and message
      nudging qualification. Fixture: bare name absent but single container member
      with close Levenshtein match → exit 3 candidates prefer `Container.name`.
      Existing `resolver_test.go` ambiguous cases unchanged. Error text stable enough
      for `--porcelain` consumers where applicable.

- [ ] **Stronger ordinal-anchor warnings beyond commit.** `rgit commit` already
      prints `[warning] anchor 'file:Foo#2' is positional; inserting a symbol above
      it repoints it` for ordinals in the plan (`internal/app/commit.go`,
      `internal/synth/stage.go` `plan.ordinals`). `rgit diff`, `blame`, and `log`
      accept `Foo#N` silently today.

      **Packages / files:** `internal/app/diff.go`, `internal/app/blame.go`,
      `internal/app/log.go`, `internal/synth/stage.go` (ordinal detection),
      `internal/resolve/resolver.go` (`ParseOrdinal`), `docs/ANCHORS.md`,
      `cmd/rgit/rgit_e2e_test.go` (ordinal warning tests).

      **Traps:** Warning must fire only for ordinal-resolved anchors, not
      container-qualified or unique bare names. Do not fail the command — advisory
      only, same as commit. `diff --porcelain` must not interleave warnings into
      stdout records; stderr only. Do not warn on read-only commands if that would
      spam agents on every `rgit log` — consider once-per-invocation or only when
      the ordinal anchor is the explicit target.

      **Acceptance criteria:** `rgit diff auth.go:Init#2` (fixture with duplicate
      bare names) emits the ordinal warning on stderr and succeeds. Unique
      `auth.go:ValidateToken` does not warn. Commit behaviour unchanged.
      Documented in `docs/USAGE.md` § Warnings.

- [ ] **`rgit languages` repo-scoped filter.** `rgit languages` lists every
      grammar compiled into the binary (`internal/app/languages.go`,
      `resolve.Languages()`), runtime-accurate for build tags (SQL). Agents in a
      monorepo often want "which grammars appear in **this** repo's tracked files"
      to decide whether symbol anchors are worth attempting.

      **Packages / files:** `internal/app/languages.go`, `internal/gitx/gitx.go`
      (`ls-files` or equivalent), `internal/resolve/lang.go` (`ForExtension`,
      `ForPath`), `docs/USAGE.md`, `docs/CODES.md`.

      **Traps:** Extension-only scan misses extensionless shebang scripts — reuse
      `LanguageForWorktreePath` or bounded peek where practical, or document the
      gap. Submodule/gitlink paths are not regular files — exclude per
      `docs/ANCHORS.md` exit 10. Filter is advisory; binary still contains all
      grammars. Do not require a git repo for plain `rgit languages` (breaks
      install-time use).

      **Acceptance criteria:** New flag (e.g. `rgit languages --in-repo` or
      `--porcelain --filter=tracked`) lists only grammars with ≥1 matching tracked
      path in CWD repo. Repo with only `.go` files omits `python` when Python
      grammar is compiled in. Outside a repo, flag errors clearly or no-ops per
      chosen contract (documented). Tests with temp repo fixtures.

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

- [ ] **`rgit blame -p` / `--patch`.** `rgit log` already accepts `-p`/`--patch`
      as opt-in patch output (`internal/app/log.go`). `rgit blame` supports
      `--porcelain` only (`internal/app/blame.go`), forwarding to `git blame
      --porcelain` over the symbol's line range.

      **Packages / files:** `internal/app/blame.go`, `internal/app/shared.go`
      (`parseAnchorCommandArgs`), `internal/app/completion.go` (`rgitBlameFlags`),
      `docs/USAGE.md` § Blame, `docs/CODES.md`, `internal/app/blame_test.go`,
      `internal/app/completion_test.go`.

      **Traps:** Mutually exclusive with `--porcelain` if git treats them that way
      — mirror `log`'s flag validation. Default stays human-readable blame.
      Patch body is git's own format, unmodified (same convention as `diff -p`).
      Line range comes from resolved extent — wrong anchor still exit 3/4/9, never
      whole-file widen.

      **Acceptance criteria:** `rgit blame -p file.go:Symbol` emits git's standard
      patch-style blame for the symbol's lines only. `--porcelain` and `-p` together
      → exit 129 with clear message. Help, completion, and USAGE updated.

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
