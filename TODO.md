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
      by every commit, not just the new-file preamble. Deferred as a much larger
      blast radius than the bug that motivated it. Go is the only language
      positioned to benefit: `OwnsTrailingSeparator` is an explicit `Language`
      method every adapter answers, and only Go answers true, because no other
      grammar here has a formatter-enforced blank-line convention to hang it on.

## v2 — grammars

Ordered by measured demand across the repositories `rgit` is actually run
against, the same way the v1 three were chosen (`specs/design.md` § Grammar
scope). Config and data files stage by path meanwhile, which is what a lockfile
or a version bump wants regardless.

- [ ] HTML element anchors (`div#app`).
- [ ] Rust, C, C++. Speculative: no surveyed repository contains any. Worth
      doing if that changes, but not ahead of the languages above.
