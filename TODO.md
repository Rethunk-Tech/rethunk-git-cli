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
- [ ] A YAML comment sitting between the end of a nested value and the next,
      more shallowly indented sibling is unreachable by any single-key anchor.
      tree-sitter-yaml's own external scanner grafts it onto whichever block
      was still open when it consumed the comment token, regardless of the
      comment's own written column, so neither the preceding key nor the one
      it was written above claims it (`lang_yaml.go`'s `trimTrailingComment`).
      Still reachable via `@toplevel` or the whole file.

## v2 — grammars

Ordered by measured demand across the repositories `rgit` is actually run
against, the same way the v1 three were chosen (`specs/design.md` § Grammar
scope). Config and data files stage by path meanwhile, which is what a lockfile
or a version bump wants regardless.

- [ ] CSS/SCSS selector anchors (`.button-primary`, `@media`).
- [ ] JSON/TOML key-path anchors (`server.port`). Breadth overstates the value:
      most of it is `package.json`, tsconfig, and lockfiles, which want
      whole-path staging anyway.
- [ ] SQL. Schema and function definitions benefit; migrations are append-only
      new files, where symbol granularity adds nothing.
- [ ] HTML element anchors (`div#app`).
- [ ] Rust, C, C++. Speculative: no surveyed repository contains any. Worth
      doing if that changes, but not ahead of the languages above.

## Deferred features

- [ ] Shell completion — the useful form (symbols after `auth.go:`) is a dynamic
      function calling `rgit diff --porcelain`; no framework needed. Cheaper now
      than when it was deferred: `pflag` is already a dependency and
      `--porcelain` is the data source.
- [ ] `-S` is not accepted as shorthand for `--gpg-sign`; the long form is.
      `pflag` checks a shorthand's `NoOptDefVal` before checking for an attached
      value, so git's own idiomatic `-Skeyid` misparses as an unknown `-k`
      flag. Shipping the shorthand needs that resolved upstream or worked
      around, and a flag that silently misreads its argument is worse than one
      that is absent.
