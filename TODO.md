# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

Items below are the residual queue after wave-21's ignorecase threading,
shebang `.exe` strip, `languages --in-repo` untracked/HEAD, context
stash/sparse `W` records, zero-target amend/allow-empty, `--reuse-message`,
and skip-worktree/assume-unchanged refusal. Deliberately not queued:
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

## Follow-on / carry-forward (wave-21)

Planning only. Remaining from the wave-21 list: index as an existence
source, `--only`, promisor blobs, and `--pathspec-from-file`. Git docs
(`git add --intent-to-add`, `git commit --only`, `cat-file --batch`
missing vs promisor, `check-ignore` vs index) were checked against the
live call sites. Wave-21 already landed ignorecase threading, shebang
`.exe`, `--in-repo` untracked/HEAD, context stash/sparse, zero-target
amend/allow-empty, `--reuse-message`, and skip-worktree refusal — do
not restate those here.

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

## Follow-on / carry-forward (wave-21 audit)

- [ ] **`--reuse-message` with zero targets.** Zero targets are allowed
      for `--amend` / `--allow-empty` / `--fixup` / `--squash`, not for
      `--reuse-message` alone (`internal/app/commit.go`). Plain
      `git commit --reuse-message=HEAD` with a staged index and no
      pathspec succeeds. `docs/USAGE.md` currently matches rgit.

      **Packages / files:** `internal/app/commit.go` (the target-count
      exemption), [`docs/USAGE.md`](docs/USAGE.md#flags).

      **Traps:** Still skip staging, not `git add -A`. `--reuse-message`
      with `-m` remains git's 128. Do not register commit `-C`.

      **Acceptance criteria:** With a staged path and no named targets,
      `rgit commit --reuse-message=HEAD` writes a commit whose message
      equals HEAD's (and authorship per git). Bare `rgit commit` with
      no flags still exits 129.
