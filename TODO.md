# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

## Known limitations

- [ ] Separator ownership stops at `@header` and `@imports`. The same rule was
      verified to generalize to any chain of adjacent top-level declarations —
      each non-final region absorbing its own trailing gap sums exactly, since
      the final region's missing trailing newline offsets the file's own EOF
      terminator — but adopting it would change the general insertion path used
      by every commit, not just the new-file preamble. Deferred as a much larger
      blast radius than the bug that motivated it.

## v2 — grammars

Ordered by measured demand across the repositories `rgit` is actually run
against, the same way the v1 three were chosen (`specs/design.md` § Grammar
scope). Config and data files stage by path meanwhile, which is what a lockfile
or a version bump wants regardless.

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
      the existing adapters falling back to `ImportKinds`, covers both.
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
