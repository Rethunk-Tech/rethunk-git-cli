# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after wave-20's merge/sequencer
parity, unmerged-index refusal, context `H`/`S` records, versioned
shebangs, and symbols HEAD fallback, plus wave-21 planning (index as an
existence source, `--only`, zero-target amend/allow-empty, `--reuse-message`,
skip-worktree bits, languages --in-repo, context stash/sparse, shebang `.exe`,
promisor blobs, target-file input). Deliberately not queued:
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

## Follow-on / carry-forward (wave-20)

- [ ] **Wire `core.ignorecase` into language lookup.**
      `Repo.IgnoreCase` (`internal/gitx/gitx.go`) and
      `ForExtensionFolding` (`internal/resolve/lang.go`) exist; no
      production caller threads them. `LanguageForPath` / `ForExtension`
      still exact-match, so `Foo.GO` is exit 9 even when git treats it
      as `foo.go`.

      **Packages / files:** `internal/resolve/lang.go` (`LanguageForPath`
      / `ForPath`), callers in `internal/app`, `internal/synth/stage.go`,
      `internal/diff/run.go`.

      **Traps:** Lowercase only the extension key, never the path.
      When `core.ignorecase` is false, keep exact match. `filepath.Ext`
      of `file.d.ts` is already `.ts`. Query git once per Repo.

      **Acceptance criteria:** With `core.ignorecase=true`,
      `rgit symbols Foo.GO` lists the same names as `foo.go`. With it
      false, `.GO` still has no grammar. `languages --porcelain`
      extension lists stay lowercase as today.

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
