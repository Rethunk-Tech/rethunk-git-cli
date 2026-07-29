# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

## Known limitations

Both of the first two were re-judged against the CSS, JSON, TOML and SQL
grammars and are unaffected, so the growing grammar count has not changed the
calculus. Separator ownership is now an explicit `Language` method rather than a
name switch, so every adapter states its own answer; all of them except Go
answer false, because none has a deterministic formatter-enforced blank-line
convention to hang the rule on. The YAML gap is specific to tree-sitter-yaml's
external scanner grafting a comment onto whichever block was still open, which
TOML (flat, bracket-delimited) and CSS (brace-delimited) do not share.

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
- [ ] SCSS and SASS have no anchor support, and cannot until a grammar ships Go
      bindings. Neither candidate does: `tree-sitter-grammars/tree-sitter-scss`
      publishes `bindings/{c,node,python,rust,swift}` and is not a Go module at
      all, and `serenadeai/tree-sitter-scss` publishes only `{node,rust}`.
      Parsing `.scss` with the CSS grammar is not a substitute — nesting,
      `$variables` and `@mixin` yield ERROR nodes, the same reason
      `lang_shell.go` refuses `.zsh`. `.css` is claimed; `.scss`/`.sass` are not.
- [ ] `make cross` covers linux/amd64, linux/arm64 and windows/amd64 but not
      darwin. Cross-building for macOS needs a macOS SDK: `rgit` links
      tree-sitter through cgo, so `CGO_ENABLED=0` is impossible, and package
      `net` (via `go.lsp.dev/jsonrpc2`) forces `-lresolv` and
      `-framework CoreFoundation` at link time. Building with `-tags
      netgo,osusergo` does not avoid it. `zig cc` covers every other target.

## v2 — grammars

Ordered by measured demand across the repositories `rgit` is actually run
against, the same way the v1 three were chosen (`specs/design.md` § Grammar
scope). Config and data files stage by path meanwhile, which is what a lockfile
or a version bump wants regardless.

- [ ] HTML element anchors (`div#app`).
- [ ] Rust, C, C++. Speculative: no surveyed repository contains any. Worth
      doing if that changes, but not ahead of the languages above.
