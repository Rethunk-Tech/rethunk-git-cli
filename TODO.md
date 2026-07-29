# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

## Deferred

- [ ] Generalize separator ownership beyond `@header` and `@imports`. The rule
      was verified to hold for any chain of adjacent top-level declarations —
      each non-final region absorbing its own trailing gap sums exactly, since
      the final region's missing trailing newline offsets the file's own EOF
      terminator — but adopting it would change the general insertion path used
      by every commit, not just the new-file preamble — a far larger blast
      radius than the new-file case it would improve. Go is the only language
      positioned to benefit: `OwnsTrailingSeparator` is an explicit `Language`
      method every adapter answers, and only Go answers true, because no other
      grammar here has a formatter-enforced blank-line convention to hang it on.

## v2 — grammars

Ordered by measured demand across the repositories `rgit` is actually run
against, the same way the v1 three were chosen (`specs/design.md` § Grammar
scope). Config and data files stage by path meanwhile, which is what a lockfile
or a version bump wants regardless.

- [ ] Rust, C, C++. Speculative: no surveyed repository contains any. Worth
      doing if that changes, but not ahead of the languages above.

## v2 — commands

`rgit restore FILE:SYMBOL` is the only command left in this set — `blame`,
`context`, and `log` already shipped (dispatched in `internal/app/app.go`,
documented in [`docs/USAGE.md`](docs/USAGE.md) and `CHANGELOG.md`
[Unreleased]), so they no longer belong here. `restore` stays deferred, not
implemented, because it is the one command in the original set that would
do real work — rewrite working-tree bytes — rather than wrap read-only
machinery that already exists; see its own entry below for why that alone
is reason enough to hold it back. The design record below was accepted
against the same question every shipped command in this set was held to:
does it save an LLM tokens `git` already charges? Porcelain record shapes
belong in [`docs/CODES.md`](docs/CODES.md); argument grammar additions in
[`docs/USAGE.md`](docs/USAGE.md).

- [ ] `rgit restore FILE:SYMBOL` — **deferred, not to be implemented for
      now.** This is the only rgit command that would rewrite working-tree
      bytes; `blame`, `context`, and `log` already shipped read-only, and
      holding this one back is what keeps the whole surface non-destructive
      rather than an accident of which commands shipped first. The design
      below stays intact — it is the right contract if a destructive
      command is ever accepted deliberately — but it is a design record, not
      a queued task.
      Surgical undo — splice a symbol's
      `HEAD` (or `--source REV`) content over the working-tree copy, leaving
      everything else untouched. Mechanism: `git show REV:FILE`, resolve the
      same anchor in both blobs, splice via `internal/synth` — blob
      synthesis run in reverse; the machinery exists, this is a new caller.
      Token case: today the only surgical-undo path is read the file, edit
      by hand, hope; that round-trips the whole file through the model.
      This is the only destructive command in the set (it rewrites working-
      tree bytes; `blame`, `context`, and `log` are read-only and need no
      safety net), so it carries a backup contract:

      *Backup:* before writing, the displaced working-tree extent is
      captured and emitted as a `git apply`-compatible unified diff —
      correct path header, the replaced extent plus three lines of context
      either side so `git apply` can relocate it even after neighbouring
      edits. In `--porcelain` mode the patch is a record in the output
      stream; otherwise it goes to stderr in a fenced block. Undo of a
      mistaken restore is therefore plain `git apply`, no `rgit` machinery
      required. The backup is *not* written to disk by `rgit`: the tool
      holds no persistent state (AGENTS.md § State), so the artifact
      travels with the caller, who is already capturing the output stream.
      `git stash` was rejected for this — it is pathspec-granular, not
      symbol-granular, and it mutates stash state the user did not ask for.

      Guardrails: resolve in *both* revisions before writing — a symbol
      that does not exist at the source revision is an error, not a
      deletion. The backup patch is emitted before any byte is written,
      and a failed splice (extent drift since resolution) leaves the file
      untouched, matching the resolve-before-stage invariant in the
      synthesis path.
