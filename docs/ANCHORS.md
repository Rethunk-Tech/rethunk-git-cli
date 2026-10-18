# Anchors

`FILE:NAME` names a symbol's extent. Whether an argument *is* an anchor:
[`USAGE.md`](USAGE.md#argument-shape). Unaddressable shapes:
[`LIMITATIONS.md`](LIMITATIONS.md#constructs-no-anchor-reaches).

## What an extent covers

Leading **doc comments, decorators, and attributes**. A comment block
directly above a symbol with **no intervening blank line** belongs to it:

```go
func A() {}

// free-floating note      <- belongs to neither

// Doc for B.              <- B's
//go:noinline              <- B's
func B() {}                <- B's
```

A hunk owned by no symbol is `(unanchorable)` in `rgit diff`; name the
path. Overlapping or nested anchors in one file merge before staging.

A symbol whose new text names top-level declarations the same file adds
stages those too — direct references, same file, only declarations `HEAD`
lacks. A sibling already in `HEAD` stays excluded.

## Qualification

Same-named symbols are legal. A container qualifies its members one level:
`auth.go:A.Get`, `svc.ts:Svc.login`, `USAGE.md:install.options`,
`ci.yml:jobs.build`. Naming the container claims every member. Nearest
container only: `### Options` under `## Setup` under `# Install` is
`setup.options`, not `install.setup.options`.

Go methods sit beside their type, so staging one never drags the type. A
class encloses its members: naming a member of a class absent from `HEAD`
stages the whole class. `rgit commit` says so; `rgit diff --sym` on that
member warns. The unfiltered listing does not — every sibling has a row.

| Form | Example | Notes |
| --- | --- | --- |
| Bare | `auth.go:ValidateToken` | Exit 4 if several match |
| Container-qualified | `auth.go:A.Get` | Preferred |
| gopls spelling | `auth.go:(*A).Get` | Accepted; needs quoting |
| Markdown slug | `USAGE.md:diff-scope` | Emitted; raw heading text accepted on input |
| Ordinal | `auth.go:init#2` | Last resort; warns |

Emit the spelling that never needs quoting (`A.Get`, the slug). Accept
what a person or IDE already has.

Ordinals count container-qualified names. A TypeScript `get`/`set` pair is
`Box.size#1` / `Box.size#2`. Two YAML keys that collapse to the same
one-level qualifier (`a.common.port`, `b.common.port`) are
`common.port#1` / `common.port#2`.

A Markdown heading's extent is the section it opens, subsections included.
A setext heading (`===` / `---`) is the heading line only.

## Pseudo-anchors

| Anchor | Covers |
| --- | --- |
| `@header` | Preamble: shebang, copyright, build tags, package doc, package clause |
| `@imports` | Import / use / include declarations |
| `@toplevel` | Package-level decls excluding imports and header |

`@header` plus `@imports` make a synthesized new file compile; both are
auto-staged for an untracked file. `@header` is empty when the file opens
with code. A leading comment attached to the first declaration by the
blank-line rule stays with that declaration.

`@imports` spans the whole import run, including grouping comments
between imports (Go: one `import_declaration`; TypeScript/Python: one
node per import). A comment after the last import belongs to what follows.

Markdown: `@imports` is empty; `@header` is YAML/TOML frontmatter;
`@toplevel` is every heading plus the lede. YAML: `@header` is a leading
comment run, not `---` / `%YAML`; `@imports` is empty; `@toplevel` is every
top-level key. HTML: `@header` is `<!DOCTYPE html>` plus an adjacent
comment; `@imports` is empty (`<link>` / `<script src>` are ordinary tags);
`@toplevel` is every id-bearing element.

## Paths that anchors cannot address

`commit` refuses (exit 10) on symlink, gitlink, binary, unmerged,
skip-worktree, and assume-unchanged. Name the path.

| Kind | How it stages |
| --- | --- |
| Regular file | `git add <path>` — filters applied |
| Submodule | Gitlink SHA from the submodule `HEAD`, or the index if uninitialised |
| Symlink | Target string, mode `120000` |
| Rename | Name both paths; git detects at diff time |
| Gitignored | Exit 7 unless already in the index or `HEAD` |
| Unmerged | Exit 10; name the path |
| Skip-worktree / assume-unchanged | Exit 10; bits unchanged |

`FILE:SYMBOL` on JSON, JSONC, JSON5, YAML, or TOML resolves for read-only
commands; `commit` refuses (exit 12). Name the path.
[`CODES.md`](CODES.md#exit-12-is-commits-alone).

## Language support

Sixteen grammars ship unconditionally; SQL is `-tags rgit_sql`.

| Grammar | Extensions | Addresses |
| --- | --- | --- |
| Go | `.go` | Every top-level declaration |
| TypeScript | `.ts`, `.mts`, `.cts` | Every top-level declaration |
| TSX/JavaScript | `.tsx`, `.jsx`, `.js`, `.mjs`, `.cjs` | As above; JS parses under TSX |
| Python | `.py`, `.pyi` | Every top-level declaration |
| Rust | `.rs` | Every item, plus fields, variants, trait/impl members, inline `mod`. Qualify with `::` (`Config::new`). Impl blocks named as Rust reads them (`impl Config`). Doc comment and outer attributes (`#[test]`) are in the extent |
| Markdown | `.md`, `.markdown`, `.mdx` | Headings and their sections. MDX constructs: [`LIMITATIONS.md`](LIMITATIONS.md#constructs-no-anchor-reaches) |
| Shell | `.sh`, `.bash` | Functions and top-level assignments. Redefined functions: ordinals |
| Zsh | `.zsh`, `.zshrc`, `.zshenv`, `.zprofile` | As Shell, zsh grammar. `#!/usr/bin/env zsh` selects it |
| YAML | `.yaml`, `.yml` | Mapping keys, one-level qualified |
| CSS | `.css` | Selectors and at-rules. `.sass`: [`LIMITATIONS.md`](LIMITATIONS.md#unsupported-languages) |
| SCSS | `.scss` | As CSS, plus `@mixin`/`@function` by name and top-level `$variable`. `@use`/`@forward`/`@import` are `@imports` |
| JSON | `.json` | Object key paths, one-level qualified |
| JSONC | `.jsonc` | As JSON; comments as docs. A trailing comma is an error node — keys after it are not indexed |
| JSON5 | `.json5` | As JSON; unquoted and single-quoted keys name the same |
| TOML | `.toml` | Key paths and `[table]`/`[[array]]` headers, one-level qualified |
| HTML | `.html`, `.htm` | Id-bearing element, tag-qualified (`div#app`) |
| SQL | `.sql` | `CREATE TABLE`/`VIEW`/`FUNCTION`/`INDEX`/`TRIGGER`/`TYPE`, schema-qualified one level. [`INSTALL.md`](INSTALL.md#sql-support) |

**CSS.** A grouped rule is **one anchor per selector**, each staging the
whole rule: `.a, .b { … }` answers to `.a` or `.b`. The list is not an
anchor. A repeated selector is exit 4 (`.a#1`). A comma inside
`:is()`/`:not()` is not a separator. An at-rule addresses by its full
prelude (`@media (max-width: 600px)`); no prelude → the bare keyword.
Nested rules inside `@media`/`@supports`/`@keyframes` have no anchor.
Native nesting **is** addressable: `.parent .child` (literal space).
Three levels qualify by the immediate parent only.

`styles.css:@header` is legal CSS and collides with the pseudo-anchor. The
pseudo-anchor always wins. The at-rule still appears in `rgit diff` under
its qualified name.

**TOML.** `[table]` / `[[array]]` is addressable by its bracket text.
`[[servers]]` twice is `servers#1`/`servers#2`. A dotted pair key (`a.b =
1`) is one bare name, not a second qualification scheme. Naming a table
stages the blank line before the next header.

**SQL.** Schema-qualified `s.t` is `s.sql:s.t`. `CREATE TRIGGER` has no
schema qualifier; same-named triggers disambiguate by ordinal. Unnamed
`CREATE INDEX` is unaddressable.

**HTML.** Tag + id only, any depth; naming an outer element claims nested
id-bearing descendants. `id` is matched case-insensitively; the value is
not. A dot in the id is part of the value (`a#toc-mod.io`).

An extensionless file matches its shebang. Extension wins first. `bash`/`sh`
→ Shell, `python3`/`python`/`python3.12` → Python (not Python 2). A
trailing `.exe` on the interpreter is stripped. **`zsh` is excluded** from
the bash grammar — wrong extent is worse than refusal; use a `.zsh*`
extension or the zsh shebang.

Node ecosystem → TypeScript: `node`, `nodejs`, `tsx`, `ts-node`, `bun`,
`deno`, including `env -S` and `npx`/`bunx NAME` (unwrap once). Versioned
`node20` / `bun1.2` too.

Shebang is read from the worktree, else the first 256 bytes of the `HEAD`
blob. Unmapped shebang is exit 9.

Cross-check coverage and permanent `[ts-only]`:
[`LIMITATIONS.md`](LIMITATIONS.md#language-server-coverage). Exit 9 is no
grammar at all.

## Deletions

Resolve against `HEAD` when the symbol is gone from the worktree.
`rgit diff` marks them `DELETED`.
