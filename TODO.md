# TODO

Future work only. Decisions already made live in
[`specs/design.md`](specs/design.md).

## Known limitations

- [ ] TypeScript multi-declarator statements (`const a = 1, b = 2`) address only
      the first declarator. A change to a later one reports as `(unanchorable)`
      rather than being misattributed, so the output stays honest — but the
      symbol is not nameable. Go's grouped `const`/`var`/`type` blocks address
      each spec individually; TypeScript should match.
- [ ] `@header` resolves to nothing in a TypeScript or Python file with no
      shebang, since neither language has a package clause. Callers that
      auto-stage `@header` for an untracked file must tolerate its absence.

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

## Migration

- [ ] Cut over from the `rethunk-git` MCP — checklist in
      [`specs/CUTOVER.md`](specs/CUTOVER.md)
