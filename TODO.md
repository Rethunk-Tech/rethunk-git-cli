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
fallback) and wave-21 planning (index as an existence source, `--only`,
zero-target amend/allow-empty, `--reuse-message`, skip-worktree bits,
languages --in-repo, context stash/sparse, shebang `.exe`, promisor
blobs, target-file input). Deliberately not queued:
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

## Follow-on / carry-forward (wave-21)

Planning only. Git docs (`git add --intent-to-add`, `git commit --only`
/ `--reuse-message`, `ls-files -v` skip-worktree / assume-unchanged,
`cat-file --batch` missing vs promisor, `check-ignore` vs index) and
pflag interspersed parsing were checked against the live call sites;
none of the items below is already tracked above. Wave-20 already owns
unmerged stages, `LsFilesStage` line-1, sequencer/`B` detached, versioned
shebangs, `core.ignorecase`, and symbols HEAD fallback — do not restate
those here.

- [ ] **Treat the index as an existence source (gitignore + classify).**
      `GitPathChecker.ExistsInWorktreeOrHEAD` (`internal/cli/precedence.go`)
      and `checkGitignoreRefusal` (`internal/synth/special.go`) only
      consult the worktree and `HEAD` (`LsTreeTolerant`). `git add`
      treats an index entry as tracked: `git add -f -N ignored.go` then
      `git add ignored.go` succeeds, and a staged-new file whose
      worktree copy was deleted is still committable (`gitx.Add` already
      special-cases staged removals via `hasStagedChange`). rgit does
      not: an ignored path not in `HEAD` is exit 7 even when it is in
      the index, and `FILE:SYMBOL` on an index-only path fails rule 4/5
      (unresolvable) instead of resolving against the index blob.

      **Packages / files:** `internal/cli/precedence.go`
      (`GitPathChecker`), `internal/synth/special.go`
      (`checkGitignoreRefusal`), `internal/gitx/gitx.go` (`LsFilesStage`
      or a dedicated index probe), `internal/synth/stage.go`
      (`openFilePlan` HEAD-vs-worktree reads),
      [`docs/USAGE.md`](docs/USAGE.md#argument-shape),
      [`docs/ANCHORS.md`](docs/ANCHORS.md).

      **Traps:** Do not invent a fourth classification rule — extend
      "worktree or HEAD" to "worktree, index, or HEAD", still in that
      order so a dirty worktree wins. Wave-20's unmerged refusal still
      applies before any splice. Intent-to-add's empty index blob is
      not a synthesis base: splice worktree vs `HEAD` (missing) as
      today; the empty blob is only existence. `os.Stat` follows
      symlinks; keep `classifyPath`'s `Lstat` refusal. Query the index
      once, not per token.

      **Acceptance criteria:** After `git add -f -N skip-me.go` on a
      gitignored new file, `rgit commit -m "…" skip-me.go:Foo` stages
      the symbol and is not exit 7. After `git add new.go && rm new.go`
      (never committed), `rgit commit -m "…" new.go:Foo` resolves from
      the index blob (or refuses with a clear "worktree gone, index
      only" if synthesis cannot run without worktree bytes — pick one
      and pin it). A path in neither worktree, index, nor `HEAD` still
      fails as today. `git add` of a never-indexed ignored path remains
      exit 7.

- [ ] **`rgit commit --only`: commit named targets, leave other staged
      work uncommitted.** Inherited behaviour is that pre-staged work
      comes along ([`docs/USAGE.md`](docs/USAGE.md#behaviour-inherited-from-git)).
      Git's own `--only` / `-o` is the opt-out. Forwarding
      `git commit --only -- <paths>` after `update-index --cacheinfo`
      is wrong: `--only` rebuilds a temporary index from `HEAD` plus
      the *worktree* path, which would replace a synthesized blob with
      the whole dirty file.

      **Packages / files:** `internal/app/commit.go`,
      `internal/gitx/gitx.go` (`CommitOptions`),
      [`docs/USAGE.md`](docs/USAGE.md#flags).

      **Traps:** Implement `--only` as a temporary index that is
      `read-tree HEAD` plus the synthesized (and pathspec-`git add`'d)
      entries, then `git commit` with `GIT_INDEX_FILE` pointing at it —
      the real index must still hold the leftover staged work afterward.
      Do not `git stash`. `--include` / `-a` stay unqueued (they widen
      the commit; rgit names targets). Still require a target. Mutually
      exclusive with nothing already exclusive except perhaps a
      documented clash with `--amend` if git's own combination is
      already weird — match git, do not police.

      **Acceptance criteria:** With `other.go` fully staged and
      `a.go:Foo` dirty, `rgit commit --only -m "…" a.go:Foo` produces a
      commit whose tree differs from `HEAD` only in `a.go`'s `Foo`
      extent; `other.go` remains staged. The same invocation without
      `--only` still includes `other.go`. Pathspec `--only a.go` matches
      `git commit --only -- a.go`.

- [ ] **Zero-target `--amend` and `--allow-empty`.**
      `runCommit` requires at least one target even when `--amend` or
      `--allow-empty` is set (`internal/app/commit.go`). Plain
      `git commit --amend --no-edit` and `git commit --allow-empty -m`
      operate on the index as it stands and take no pathspec. Message-only
      amend therefore needs a dummy path today.

      **Packages / files:** `internal/app/commit.go` (the target-count
      check vs `autoMessage` / `allowEmpty`),
      [`docs/USAGE.md`](docs/USAGE.md#flags).

      **Traps:** Zero targets means *skip staging*, not `git add -A`.
      `--amend` with neither `-m` nor `-F` still uses `--no-edit`.
      `--allow-empty` with no targets and no `--amend` still needs
      `-m`/`-F`. A mix of unchanged named targets plus `--allow-empty`
      stays the existing exit-11 suppression. Do not fold this into
      wave-20's merge `--no-edit` (that still names a path).

      **Acceptance criteria:** `rgit commit --amend` with no targets
      and no `-m` rewrites HEAD's tree identically and keeps HEAD's
      message. `rgit commit --allow-empty -m "chore: ping"` with no
      targets writes an empty commit. `rgit commit -m "…" ` with no
      targets and no `--allow-empty`/`--amend`/`--fixup`/`--squash`
      still exits 129.

- [ ] **Forward `--reuse-message=<commit>` (git's commit `-C`).**
      Global `rgit -C <path>` is directory-chdir and must stay before
      the command so it cannot collide with `git commit -C`. The long
      form `--reuse-message` does not collide and is not on the commit
      flag set. `--reedit-message` (`-c`) opens an editor; rgit never
      does.

      **Packages / files:** `internal/app/commit.go`,
      `internal/gitx/gitx.go` (`CommitOptions`),
      [`docs/USAGE.md`](docs/USAGE.md#flags) (next to `--amend`'s
      `--no-edit` note).

      **Traps:** Do not register short `-C` on `commit`. `--reedit-message`
      is a usage error (129) that names `--reuse-message`, not a silent
      alias. Combine with `-m` the way git does (git rejects or
      appends — match git, measure). `autoMessage` must treat
      `--reuse-message` like `--amend` so a message is not also
      required. Conventional-commit warning should not fire on a
      reused subject that is already non-conventional.

      **Acceptance criteria:** `rgit commit --reuse-message=HEAD -m` is
      not required; the new commit's message equals that commit's.
      `rgit commit -C repo commit --reuse-message=HEAD --allow-empty`
      still chdirs first. `--reedit-message` exits 129.

- [ ] **Refuse or preserve skip-worktree / assume-unchanged on symbol
      stage.** `UpdateIndexCacheinfo` is `update-index --add --cacheinfo`
      (`internal/gitx/gitx.go`), which replaces the cache entry and
      drops CE_SKIP_WORKTREE / CE_VALID (assume-unchanged). Default
      diff already never sees skip-worktree paths
      ([`docs/LIMITATIONS.md`](docs/LIMITATIONS.md#symlinks-submodules-renames-and-content-filters));
      an explicit `FILE:SYMBOL` on a materialized sparse path can still
      reach synthesis and silently un-skip the file, breaking
      sparse-checkout.

      **Packages / files:** `internal/gitx/gitx.go` (`ls-files -v` or
      `--debug`), `internal/synth/special.go` / `stage.go`,
      [`docs/ANCHORS.md`](docs/ANCHORS.md) / [`docs/LIMITATIONS.md`](docs/LIMITATIONS.md).

      **Traps:** Prefer exit 10 (special path) over a new code — same
      "name the path" advice. Pathspec targets still go through
      `git add`, which is how git itself materializes sparse paths.
      Do not clear bits "and restore them after" — a failed commit
      would still have mutated the index. Wave-20 unmerged is a
      different index state; check skip-worktree on stage 0 only.

      **Acceptance criteria:** `FILE:SYMBOL` on a path `git ls-files -v`
      marks `S` or `h` refuses before `hash-object`; bits unchanged.
      `rgit commit thefile` (path) still delegates to `git add`.
      Ordinary stage-0 paths unchanged.

- [ ] **`rgit languages --in-repo` counts untracked and HEAD-only files.**
      `filterLanguagesInRepo` walks `LsFilesTracked` and
      `LanguageForWorktreePath` (`internal/app/languages.go`), so a
      greenfield repo of untracked `.go` files lists nothing, and a
      deleted tracked script whose grammar is only at `HEAD` is
      omitted. Wave-20's symbols HEAD fallback is the same hole on
      another command.

      **Packages / files:** `internal/app/languages.go`
      (`filterLanguagesInRepo`), `internal/gitx` (`LsFilesOthers` already
      exists), [`docs/USAGE.md`](docs/USAGE.md#languages).

      **Traps:** Still advisory. Do not parse ignored untracked
      (`--exclude-standard` already). Submodule gitlinks still skip.
      `--in-repo` without a repo stays a git failure. Porcelain columns
      unchanged.

      **Acceptance criteria:** `git init` + untracked `main.go` →
      `--in-repo` lists `go`. After deleting a tracked extensionless
      `#!/usr/bin/env python3` script, `--in-repo` still lists
      `python` (once wave-20 shebang versions exist, `python3.12`
      too). A repo with only `.md` does not list `go`.

- [ ] **`rgit context`: stash and sparse-checkout records.**
      Wave-20 covers detached `B` and sequencer refs. A non-empty
      `refs/stash` and an active sparse-checkout are the other
      first-turn surprises that make `F` rows look like deletions.
      Design already allows a new record kind only when relevant, and
      forbids a flag surface
      ([`specs/design.md`](specs/design.md#rgit-context-one-diffpkgrun-call-one-new-git-log--n-primitive-no-second-attribution-path)).

      **Packages / files:** `internal/app/context.go`,
      `internal/gitx/gitx.go`, [`docs/CODES.md`](docs/CODES.md#rgit-context),
      [`docs/USAGE.md`](docs/USAGE.md#context).

      **Traps:** No flags. Do not reopen staged/unstaged split. Keep
      the 16 KiB budget and `B`/`W`/`F`/`C`/`X` sort — new kinds sit
      with `W` (diagnostic, cheap) so they survive truncation.
      Absent when `stash` is empty and sparse is off. Do not dump
      stash reflog subjects (that is `git stash list`). Sparse record
      names that sparse is on, not every excluded path.

      **Acceptance criteria:** `git stash push` then `rgit context`
      emits a machine-distinct stash-present record; dropping the
      stash removes it. `git sparse-checkout set` emits a sparse
      record; a full checkout does not. A clean `main` with neither
      is byte-identical to today's stream aside from `C` hashes
      (and aside from wave-20's `B`/sequencer once that lands).
      CODES documents the kinds. Byte budget still truncates with `X`.

- [ ] **Strip a Windows `.exe` suffix on shebang interpreters.**
      `shebangInterpreter` uses `filepath.Base` and looks up the
      exact token (`internal/resolve/lang.go`). `#!/usr/bin/env python.exe`
      and `#!C:\Python312\python.exe` unwrap to `python.exe`, which
      misses `shebangExtension`. Distinct from wave-20's dotted
      version strip (`python3.12`).

      **Packages / files:** `internal/resolve/lang.go`
      (`shebangInterpreter` / `shebangExtension`),
      `internal/resolve/lang_test.go`,
      [`docs/ANCHORS.md`](docs/ANCHORS.md#language-support).

      **Traps:** Strip `.exe` only, case-insensitive, after Base and
      after wave-20's version strip — `python3.12.exe` should still
      become `python3`. Do not strip `.bat` / `.cmd` / `.ps1`.
      Unknown `foo.exe` stays unmapped. Keep the 256-byte peek.

      **Acceptance criteria:** Extensionless `#!/usr/bin/env python.exe`
      resolves with the Python grammar. `#!/usr/bin/env node.exe`
      uses the TypeScript adapter. `#!/usr/bin/env python3` and
      `python3.12` (wave-20) unchanged. `#!/usr/bin/env perl.exe`
      still has no grammar.

- [ ] **Distinguish promisor-missing blobs from absent paths.**
      `CatFile` / `BatchCatFile` / `CatFileSample` fold every non-zero
      `cat-file` exit and every `--batch` `missing` line into
      `exists=false` (`internal/gitx/gitx.go`). On a partial clone,
      a path that exists in the tree but whose blob was not fetched
      looks like a deletion: diff attributes a full remove, commit
      synthesis splices against empty `HEAD`. Git itself would
      lazy-fetch or say "promisor".

      **Packages / files:** `internal/gitx/gitx.go` (`BatchCatFile`,
      `CatFile`, `CatFileSample`), `internal/diff/scope.go`,
      `internal/synth/stage.go`, [`docs/LIMITATIONS.md`](docs/LIMITATIONS.md)
      if the honest answer is "we still cannot fetch".

      **Traps:** Do not implement a second object store. If git can
      fetch (`git cat-file` does, when the promisor remote is
      reachable), let that happen rather than pre-empting it. Offline
      / `extensions.partialClone` with a hard `missing` should fail
      loudly (git failure, exit 128) rather than synthesize against
      emptiness. Do not treat a genuine absent path as a promisor
      error. `CatFileSample`'s `Kill` of a huge blob is unrelated and
      must stay.

      **Acceptance criteria:** In a blobless clone with the remote
      unreachable, `rgit diff` / `rgit commit FILE:SYMBOL` on a
      not-yet-fetched tracked file exits 128 (or a documented
      existing code) and does not stage a truncated/empty splice.
      A path actually absent from `HEAD` still `exists=false`. A
      fully-local clone is unchanged.

- [ ] **Read commit/diff targets from a file (git's
      `--pathspec-from-file`).** ARG_MAX bites an agent naming dozens
      of `FILE:SYMBOL` anchors. Git's `--pathspec-from-file` /
      `--pathspec-file-nul` exist for pathspecs; rgit must parse each
      line through `ClassifyArgs` because anchors are not pathspecs.

      **Packages / files:** `internal/app/commit.go`,
      `internal/app/diff.go`, `internal/cli/precedence.go`,
      [`docs/USAGE.md`](docs/USAGE.md#flags).

      **Traps:** Same grammar as positionals, one target per line
      (NUL with `--pathspec-file-nul`). `-` means stdin, and then
      `-F -` cannot also consume stdin — refuse that combination.
      Lines are targets, never flags (`--` is not implied per line).
      Empty lines skip. Do not invent a second anchor syntax.
      Mutually exclusive with nothing except the stdin clash.
      Still require a message on commit unless `autoMessage`.

      **Acceptance criteria:** A file listing `a.go:Foo` and `b.go`
      is equivalent to those two positionals for `commit` and `diff`.
      `--pathspec-file-nul` accepts a path with a newline. `-F -`
      plus `--pathspec-from-file=-` exits 129. A huge argv of the
      same targets still works.
