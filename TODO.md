# TODO

Future work only. Decisions already made live in
[`specs/design.md`](specs/design.md).

## Known limitations

- [ ] `@header` resolves to nothing in a file that opens directly with code —
      no shebang, no licence or module comment, nothing to claim. That is
      correct rather than wrong, but callers that auto-stage `@header` for an
      untracked file must tolerate its absence. A leading comment block does
      resolve in every supported language, and one attached to the first
      declaration by the blank-line rule stays with that declaration rather
      than being claimed as header.
- [ ] A symbol inserted into an existing container gains a blank line on each
      side, because boundary padding normalizes spliced regions to exactly one
      (`specs/design.md`). Members that sat adjacent in the worktree are
      committed with a blank line between them — valid, and semantically the
      right content, but not byte-identical to the worktree, so the file still
      reads as modified afterwards. Indentation is preserved.
      A fix now has a route: `spliceInsert` needs to know whether the inserted
      symbol is a container member (no separator) or a top-level declaration
      (separator), and `resolve.Declaration` already carries the `Container`
      field that answers it — it is simply not threaded through.
- [ ] Nested functions and methods of a class declared *inside* a function
      still resolve no finer than their nearest top-level declaration.
      Containers themselves are addressed one level down in every supported
      language: Go receivers, struct fields and interface methods; TypeScript
      and Python class members; TypeScript namespace members.
- [ ] Some declarations are deliberately left unaddressable, because no byte
      extent belongs to the name alone: Go's shared-name field lines
      (`A, B int`) and embedded/anonymous struct fields, TypeScript's anonymous
      `export default function () {}`, and TypeScript's destructuring
      declarators (`const {a, b} = obj`, `const [x, y] = arr`), whose binding
      names share one pattern node. Each reports `(unanchorable)` rather than
      resolving to an extent that would drag a sibling's text along. Reasoning
      in [`specs/design.md`](specs/design.md).
- [ ] `rgit commit`'s per-target `+N/-M` rows do not sum to git's own raw
      insertion count. The gap is the blank-line separators synthesis writes
      between spliced regions — bytes inside no single anchor's extent. Closing
      it would mean an `(unanchorable)`-remainder row in commit's listing,
      mirroring what `rgit diff` already shows; rolling those lines into a
      named symbol instead would misattribute them. Preview and post-commit
      listings do already agree row for row, which is what
      [`docs/USAGE.md`](docs/USAGE.md) promises.

## v2 — grammars

Ordered by measured demand across the repositories `rgit` is actually run
against, the same way the v1 three were chosen (`specs/design.md` § Grammar
scope). Config and data files stage by path meanwhile, which is what a lockfile
or a version bump wants regardless.

- [ ] Markdown heading anchors (`docs/USAGE.md:diff-scope`) — the most widely
      present language surveyed, and the only one in every repository. The
      grammar's `section` node nests natively, so container qualification
      (`install.options` vs `usage.options`) falls out of the parse tree, and
      `fenced_code_block` keeps a `#` inside a code fence from reading as a
      heading. Emit slugs, accept raw heading text on input.
- [ ] Shell function anchors (`deploy.sh:cleanup`). `function_definition` covers
      both `foo() {}` and `function foo {}`, the namespace is flat so existing
      ordinals handle redefinition, and heredoc bodies are real nodes.
      Temper expectations: fewer than half of surveyed shell *lines* sit inside
      a function and most shell files define none at all — well under the
      91%-inside-a-symbol-body figure that justified the v1 grammars. The value
      is concentrated in library-style scripts, not spread across all shell.
- [ ] YAML key-path anchors (`ci.yml:jobs.build`). Editing one CI job is a
      genuine unit, but YAML is whitespace-sensitive and the synthesis path's
      indentation handling is exactly where bugs have hidden before.
- [ ] CSS/SCSS selector anchors (`.button-primary`, `@media`).
- [ ] JSON/TOML key-path anchors (`server.port`). Breadth overstates the value:
      most of it is `package.json`, tsconfig, and lockfiles, which want
      whole-path staging anyway.
- [ ] SQL. Schema and function definitions benefit; migrations are append-only
      new files, where symbol granularity adds nothing.
- [ ] HTML element anchors (`div#app`).
- [ ] Rust, C, C++. Speculative: no surveyed repository contains any. Worth
      doing if that changes, but not ahead of the languages above.

Two pieces of shared plumbing the next grammar needs, whichever it is:

- [ ] `@imports` is expressed as a list of node *kinds*, which cannot describe
      shell (`source f.sh` is a `command` distinguished by its name — returning
      `"command"` would span nearly the whole script) or markdown (no import
      concept at all, and it must say so rather than resolve to nothing). An
      optional `ImportMatcher` interface the core resolver type-asserts, with
      the existing three adapters falling back to `ImportKinds`, covers both.
- [ ] Language lookup is keyed on file extension, so an extensionless script
      with a `#!` line resolves nothing — a minority of shell scripts, but not
      a negligible one. A `ForPath` variant that sniffs the shebang would fix
      it, but it changes a registry contract every grammar shares.

## Deferred features

- [ ] Shell completion — the useful form (symbols after `auth.go:`) is a dynamic
      function calling `rgit diff --porcelain`; no framework needed. Cheaper now
      than when it was deferred: `pflag` is already a dependency and
      `--porcelain` is the data source.
- [ ] `--json` output — `--porcelain` covers machine consumption for now
- [ ] `-S` is not accepted as shorthand for `--gpg-sign`; the long form is.
      `pflag` checks a shorthand's `NoOptDefVal` before checking for an attached
      value, so git's own idiomatic `-Skeyid` misparses as an unknown `-k`
      flag. Shipping the shorthand needs that resolved upstream or worked
      around, and a flag that silently misreads its argument is worse than one
      that is absent.
