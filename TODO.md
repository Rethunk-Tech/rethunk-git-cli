# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after wave-19's test pins, plus
wave-20 planning (merge/sequencer parity, unmerged index, context
orientation, shebang versions, ignorecase extensions, symbols HEAD
fallback). Deliberately not queued:
`rgit restore` (designed and held back —
[`specs/design.md`](specs/design.md#rgit-restore-filesymbol-an-accepted-design-deliberately-not-built)),
context staged/unstaged split, TOML taplo / SQL LSP cross-checks, Rust/C/C++/
Vue/Svelte grammars, SCSS/zsh, JSONC/JSON5, orphan-gopls handshake cleanup,
and any web/shadcn surface (this is a CLI; `@shadcn/command` does not apply).

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

- [ ] **Align runApp structured-data HEAD pin with trimmed fixture (optional).**
      Table path-success now uses `strings.Contains(got, strings.TrimRight(tc.after, "\n"))`;
      `TestRun_CommitRefusesStructuredDataSymbolViaRunApp` still checks only
      `` `"after"` `` for JSON.

      **Packages / files:** `internal/app/commit_test.go`
      (`TestRun_CommitRefusesStructuredDataSymbolViaRunApp`).

      **Acceptance criteria:** path-success HEAD Contains the trimmed JSON
      fixture blob (or documents why the quoted token alone is enough).

## Follow-on / carry-forward (wave-20)

Planning only. Git docs (`git commit --no-edit` mid-merge, `ls-files
--unmerged` / index stages 1–3, `hash-object --path`) and pflag
interspersed parsing were checked against the live call sites; none of
the items below is already tracked above.

- [ ] **Honor git's no-editor merge/sequencer message.**
      `rgit commit` currently requires `-m`/`-F` unless `--amend` /
      `--fixup` / `--squash` (`internal/app/commit.go`). Plain `git
      commit` with no message during a merge (or cherry-pick / revert)
      uses `MERGE_MSG` / `--no-edit` and still writes the two-parent
      commit. `gitx.Commit` already forwards `--no-edit` for amend.
      Mid-merge `rgit commit <targets>` without `-m` is therefore a
      usage error (129) rather than git's own completion path — a
      divergence from "merges are not special-cased"
      ([`docs/USAGE.md`](docs/USAGE.md#behaviour-inherited-from-git),
      [`specs/design.md`](specs/design.md#governing-principle)).

      **Packages / files:** `internal/app/commit.go` (`autoMessage` /
      `noEdit`), `internal/gitx/gitx.go` (`CommitOptions.NoEdit`),
      [`docs/USAGE.md`](docs/USAGE.md) commit flags + inherited
      behaviour.

      **Traps:** Do not invent a merge message or special-case parents —
      git already reads `MERGE_HEAD` unaided. Rebase-continue is
      `git rebase --continue`, not `git commit`; only states where
      `git commit --no-edit` already succeeds (`MERGE_HEAD`,
      `CHERRY_PICK_HEAD`, `REVERT_HEAD`). Still require a target.
      Conventional-commit warning must not fire on git's generated
      merge subject.

      **Acceptance criteria:** In a conflict-free merge with `MERGE_HEAD`
      set, `rgit commit --no-verify path` (no `-m`) exits 0, uses the
      prepared merge message, and produces a two-parent commit. The
      same invocation with no sequencer state still exits 129. Cherry-pick
      and revert `--no-edit` match git. `-m` still overrides.

- [ ] **Refuse symbol anchors on unmerged index paths.**
      A conflicted path has stages 1/2/3 and usually no stage 0
      (`git ls-files --unmerged`; `git cat-file -p :path` is stage 0).
      `classifyPath` (`internal/synth/special.go`) never inspects
      stages. `LsFilesStage` (`internal/gitx/gitx.go`) takes
      `strings.Fields` of the *whole* `--stage` dump, so three unmerged
      lines still return the first mode and `found=true`. Index-side
      `contentSide.read` (`internal/diff/scope.go`) uses `CatFile("",
      path)` (stage 0), so `rgit diff` / `rgit context` during a merge
      can treat a conflicted file as missing. `update-index --cacheinfo`
      does not collapse stages the way `git add` does, so a symbol
      splice could leave the path unmerged or mix a synthesized stage 0
      with leftover higher stages.

      **Packages / files:** `internal/gitx/gitx.go` (`LsFilesStage`,
      new unmerged probe), `internal/synth/special.go` /
      `internal/synth/stage.go`, `internal/diff/scope.go`,
      [`docs/ANCHORS.md`](docs/ANCHORS.md) / [`docs/LIMITATIONS.md`](docs/LIMITATIONS.md)
      (special paths), [`docs/CODES.md`](docs/CODES.md) only if a new
      exit is truly required.

      **Traps:** Do not invent a new exit code if exit 10 (special path)
      or a clear 128 with existing wording can carry it — 10 already
      means "symbol anchor refused; name the path". Pathspec targets
      must still go through `git add`, which is how git marks a
      conflict resolved. Do not splice conflict-marker bytes as if they
      were a declaration. `LsFilesStage` must not silently pick line 1
      of a multi-stage dump even after the refusal lands.

      **Acceptance criteria:** `FILE:SYMBOL` on a path listed by
      `git ls-files -u` refuses before any `hash-object` /
      `update-index`; index is unchanged. `rgit commit thefile` (path)
      still delegates to `git add` and can complete the merge. `rgit
      diff` during a merge lists the conflicted path instead of
      dropping it as absent. `LsFilesStage` on a clean stage-0 path is
      unchanged.

- [ ] **`rgit context`: detached HEAD and in-progress sequencer.**
      `CurrentBranch` returns the literal `HEAD` when detached
      (`internal/gitx/gitx.go`), so the `B` record reads as a branch
      named `HEAD` with empty upstream. Merge / rebase / cherry-pick /
      revert / bisect leave refs (`MERGE_HEAD`, `REBASE_HEAD`, …) that
      first-turn orientation never reports. Design already allows a new
      record *kind* that appears only when relevant (that is how `B`
      and `W` shipped) and forbids a flag surface
      ([`specs/design.md`](specs/design.md#rgit-context-one-diffpkgrun-call-one-new-git-log--n-primitive-no-second-attribution-path)).

      **Packages / files:** `internal/app/context.go`,
      `internal/gitx/gitx.go`, [`docs/CODES.md`](docs/CODES.md#rgit-context),
      [`docs/USAGE.md`](docs/USAGE.md#context).

      **Traps:** No flags. Do not re-open the rejected staged/unstaged
      split. Keep the 16 KiB budget and `B`/`W`/`F`/`C`/`X` sort. A
      detached `B` must not look like a branch named `HEAD` (empty
      `BRANCH` plus a distinct kind, or a dedicated field — pick one
      and pin it in CODES). Sequencer record is absent when none of
      those refs exist. Unborn branch stays "no `B`", not a fake
      detached record.

      **Acceptance criteria:** Detached HEAD emits a machine-distinct
      record (not `B<TAB>HEAD<TAB>…`). Mid-merge emits a relevant
      sequencer record naming the operation; a clean branch on `main`
      is byte-identical to today's stream aside from `C` hashes. CODES
      documents the new kind. Byte budget still truncates with `X`.

- [ ] **Versioned shebang interpreters.**
      `shebangExtension` maps exact `python` / `python3` / `node` /
      `nodejs` / `tsx` / `ts-node` / `bun` (`internal/resolve/lang.go`).
      `#!/usr/bin/python3.12` and `#!/usr/bin/env python3.13` unwrap to
      `python3.12` / `python3.13` and miss. Same for `node20`. Design
      already unwraps `env`, `env -S`, `npx`, `bunx`
      ([`specs/design.md`](specs/design.md) shebang paragraph).

      **Packages / files:** `internal/resolve/lang.go`
      (`shebangInterpreter` / `shebangExtension`),
      `internal/resolve/lang_test.go`,
      [`docs/ANCHORS.md`](docs/ANCHORS.md#language-support).

      **Traps:** Do not map `python2`, `python2.7`, or unknown
      prefixes — guessing wrong is worse than exit 9. Strip a trailing
      dotted version only from already-mapped families (`python3`,
      `python`, `node`). `deno` may join the node/bun → `.ts` family;
      `zsh` stays unmapped. Keep the 256-byte peek.

      **Acceptance criteria:** Extensionless `#!/usr/bin/python3.12`
      and `#!/usr/bin/env -S python3.13 -u` resolve with the Python
      grammar. `#!/usr/bin/python2` still has no grammar. Existing
      exact `python3` / `node` / `bun` cases unchanged.

- [ ] **Case-insensitive extension lookup when `core.ignorecase` is true.**
      `ForExtension` keys `registered` on the exact suffix
      (`internal/resolve/lang.go`). Git on macOS/Windows typically has
      `core.ignorecase=true`, so `Foo.GO` / `app.JSON` are the same
      paths git tracks, but rgit reports no grammar (exit 9) instead of
      the Go/JSON adapter.

      **Packages / files:** `internal/resolve/lang.go` (`ForExtension` /
      `ForPath`), a single `git config --get core.ignorecase` (or
      equivalent) at repo open in `internal/gitx` / `internal/app`,
      not per file.

      **Traps:** Lowercase only the extension key, never the path
      (Go paths are case-sensitive in module space). When
      `core.ignorecase` is false, keep exact match. Do not treat
      `file.d.ts` as `.d.ts` — `filepath.Ext` is already `.ts`. Query
      git once per invocation.

      **Acceptance criteria:** With `core.ignorecase=true`,
      `rgit symbols Foo.GO` lists the same names as `foo.go`. With it
      false, `.GO` still has no grammar. `languages --porcelain`
      extension lists stay lowercase as today.

- [ ] **`rgit symbols` HEAD fallback when the worktree file is gone.**
      Default `blame` already resolves from HEAD when the worktree copy
      is missing (`internal/app/blame.go`); `log` always uses HEAD.
      `runSymbols` only `os.ReadFile`s the worktree
      (`internal/app/symbols.go`), so completion of `deleted.go:Foo`
      (`internal/app/completion.go`'s `_rgit_symbols`) fails closed
      even though the symbol still exists at HEAD.

      **Packages / files:** `internal/app/symbols.go`, completion
      scripts in `internal/app/completion.go`,
      [`docs/USAGE.md`](docs/USAGE.md#symbols).

      **Traps:** `--for-commit` still omits structured-data languages.
      Do not invent a revision flag. Unsupported language stays exit 9.
      A path present in neither worktree nor HEAD stays a read/resolve
      failure, not a silent empty list that looks like "file has no
      symbols".

      **Acceptance criteria:** After deleting a tracked `a.go` that
      still has `Foo` at HEAD, `rgit symbols a.go` prints `Foo` (and
      `@header` / `@imports` / `@toplevel` as today). Completion of
      `a.go:F` offers `Foo`. A never-tracked missing path still errors.
