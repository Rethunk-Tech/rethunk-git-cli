# TODO

Future work only. Decisions already made live in
[`specs/design.md`](specs/design.md).

## Known limitations

- [ ] TypeScript multi-declarator statements (`const a = 1, b = 2`) address only
      the first declarator. A change to a later one reports as `(unanchorable)`
      rather than being misattributed, so the output stays honest — but the
      symbol is not nameable. Go's grouped `const`/`var`/`type` blocks address
      each spec individually; TypeScript should match.
- [ ] `@header` resolves to nothing in a TypeScript file with no shebang
      (`hash_bang_line` is the only header kind the grammar offers) or a Python
      file that opens directly with code. A Python file opening with any
      comment does resolve one, since Python's header kind is `comment` — a
      shebang is not required. Callers that auto-stage `@header` for an
      untracked file must tolerate its absence.

- [ ] A symbol inserted into an existing container gains a blank line on each
      side, because boundary padding normalizes spliced regions to exactly one
      (`specs/design.md`). Members that sat adjacent in the worktree are
      committed with a blank line between them — valid, and semantically the
      right content, but not byte-identical to the worktree, so the file still
      reads as modified afterwards. Indentation is preserved.
- [ ] Only class members are addressed one level down. Nested functions,
      methods of a class declared inside a function, and TypeScript namespace
      members still resolve no finer than their nearest top-level declaration.

## v2 — grammars

Config and data files stage by path meanwhile, which is what a lockfile or a
version bump wants regardless.

- [ ] CSS/SCSS selector anchors (`.button-primary`, `@media`)
- [ ] JSON/YAML/TOML key-path anchors (`server.port`)
- [ ] HTML element anchors (`div#app`)
- [ ] Rust, C, C++

## Deferred features

- [ ] Shell completion — the useful form (symbols after `auth.go:`) is a dynamic
      function calling `rgit diff --porcelain`; no framework needed
- [ ] `--json` output — `--porcelain` covers machine consumption for now
