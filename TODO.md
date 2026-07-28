# TODO

Future work only. Decisions already made live in
[`specs/design.md`](specs/design.md).

## v1 — implementation

- [ ] Port [`spike/`](spike/) prototypes to Go tests, then delete the directory
- [ ] Anchor resolution: tree-sitter extents, bare **and** qualified name indexes
- [ ] Blob synthesis: splice, insertion, reverse-offset ordering, EOF-newline rule
- [ ] Argument precedence: pathspec / revision / anchor, with `--` handling
- [ ] LSP daemon probe, spawn-on-demand, and the normalized cross-check
- [ ] `rgit diff` rendering, including `--porcelain`, `MODE`, and `BINARY` rows

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
