# Anchors

How to name a symbol. For the grammar that decides whether an argument *is* an
anchor, see [`USAGE.md`](USAGE.md#argument-shape).

An anchor is `FILE:NAME`. It stages that symbol's extent and nothing else.

## What an extent covers

A symbol's extent includes its **leading doc comments, decorators, and
attributes** — what a person means by "this function".

A comment block directly above a symbol with **no intervening blank line**
belongs to it. A blank line breaks the association, matching godoc, rustdoc,
and JSDoc:

```go
func A() {}

// free-floating note      <- belongs to neither

// Doc for B.              <- B's
//go:noinline              <- B's
func B() {}                <- B's
```

A hunk owned by no symbol shows as `(unanchorable)` in `rgit diff`; stage it by
naming the path.

Overlapping or nested anchors in one file merge into a single contiguous extent
before staging.

## Qualification

Same-named symbols are legal — Go permits two `func init()` in a file, and
`(*A).Get` / `(*B).Get` share a bare name.

A container qualifies its members in every supported language: Go's receiver
type (`auth.go:A.Get`), a struct's fields and an interface's methods
(`auth.go:S.Field`, `auth.go:I.Do`), a TypeScript or Python class
(`svc.ts:Svc.login`, `svc.py:Svc.login`), a TypeScript namespace
(`ns.ts:N.inner`), a Markdown heading (`USAGE.md:install.options`), and a YAML
mapping key (`ci.yml:jobs.build`). The container itself stays addressable by
its bare name, and naming it claims every member — that is what asking for the
container means.

Qualification is always the *nearest* container, one level, never a full
breadcrumb — a Markdown `### Options` nested under `## Setup` nested under
`# Install` still addresses as `setup.options`, not `install.setup.options`,
the same ceiling a Go struct's field, a TypeScript namespace's member, or a
YAML key nested three levels deep uses: `jobs.build`'s own `runs-on` key
addresses as `build.runs-on`, not `jobs.build.runs-on`.

A struct field line naming several identifiers at once (`A, B int`) and an
embedded/anonymous field are not addressable by their own anchor: the first
has no way to give one name its own extent without the other's text coming
along, and the second has no name of its own to begin with. Name the
containing type or the path instead.

TypeScript's anonymous default export
(`export default function () {}`) is unaddressable for the same reason as the
embedded field — the grammar gives it no name to read.

So are TypeScript's
destructuring declarators — `const {a, b} = obj` and `const [x, y] = arr` bind
several names off one pattern node, so no single name owns an extent of its
own; name the containing statement or the path instead.

A YAML sequence item
is unaddressable the same way — `steps` in a GitHub Actions job addresses the
whole list (`build.steps`), but no single step has a name of its own to
address it by.

A flow-style mapping or sequence (`{ a: 1 }`, `[1, 2]`) is a
leaf too, at any nesting depth: the key holding one is addressable, but
nothing inside it is.

The two nest differently, which matters when the container is new.

A Go method
sits beside its type rather than inside it, so staging one never drags the type
along.

A class encloses its members, so naming a member of a class absent from
`HEAD` stages the whole class: there is no way to add a method to a class that
does not exist yet.

`rgit commit` says so on stderr rather than doing it
quietly, and `rgit diff --sym` on that exact member warns too — narrowing the
listing to one member would otherwise hide that its siblings are coming along
with it.

The unfiltered listing never warns: every sibling already has its own
row there, so the notice would only repeat what is already visible.

| Form | Example | Notes |
| --- | --- | --- |
| Bare | `auth.go:ValidateToken` | Fails with exit 4 if several symbols match |
| Container-qualified | `auth.go:A.Get` | Preferred |
| gopls spelling | `auth.go:(*A).Get` | Accepted on input; needs shell quoting |
| Markdown slug | `USAGE.md:diff-scope` | Emitted; a heading's own text (`"Diff scope"`) is accepted on input too, needs quoting |
| Ordinal | `auth.go:init#2` | Last resort; warns and suggests qualification |

`rgit` accepts gopls's `(*A).Get` spelling so an anchor copied from an IDE
outline resolves, but always **emits** `A.Get` — the parenthesised form contains
`*` and parens that the shell globs unless quoted. A Markdown heading works the
same way in reverse: `rgit` always **emits** the slug, lowercase with hyphens,
but accepts the heading's own raw text on a bare (uncontainer-qualified)
anchor — copying a title out of an editor should not require hand-slugifying
it first. Both are the same rule: accept what a person or another tool is
likely to already have; emit the one spelling that never needs shell quoting.

Ordinals are positional, so an inserted symbol repoints them. `rgit diff` emits
the qualified anchor when unambiguous and the ordinal form otherwise, so
copy-paste always matches resolution.

The ordinal rule counts container-qualified names, not bare ones, so it reaches
inside a container as well as beside one. A TypeScript `get`/`set` pair share a
name within their class, and address as `Box.size#1` and `Box.size#2`; the bare
`Box.size` is ambiguous (exit 4) and lists both. The same applies to two classes
of the same name in one file — and to two Markdown headings of the same text
nested under the same parent (`options#1` / `options#2`). Two headings with the
same text under *different* parents do not collide at all: `install.options`
and `usage.options` are already distinct.

Because qualification is only ever one level, two YAML keys can collide
without their trees having anything to do with each other: `a.common.port` and
`b.common.port` both qualify to `common.port` — the immediate parent's own
name, discarding that its own grandparents differ — so they disambiguate as
`common.port#1` / `common.port#2`, the same ordinal rule, not a merge.

A Markdown heading's extent is the whole section it opens — the heading line
plus everything nested under it, subsections included — so naming a heading
stages the same thing naming a class does: everything it encloses. One
exception: a setext heading (`Title` underlined with `===` or `---`) never
opens its own section — measured against the grammar's parse tree, only a
`#`-style heading does that — so a setext heading is addressable by its own
slug, but its extent is the heading line alone; the text after it belongs to
whatever section already enclosed it.

## Pseudo-anchors

Regions no symbol owns:

| Anchor | Covers |
| --- | --- |
| `@header` | File preamble: shebang, copyright, build tags, package doc, package clause |
| `@imports` | Import / use / include declarations |
| `@toplevel` | Package-level vars, consts, types, functions — excluding imports and header |

`@header` plus `@imports` is enough to make a synthesized new file compile,
which is why both are staged automatically for an untracked file (announced on
stderr). `@header` resolves to nothing when a file opens directly with code —
no shebang, no licence or module comment. That is correct, not a bug, but a
caller auto-staging `@header` for an untracked file must tolerate its absence.
A leading comment block resolves in every supported language; one attached to
the first declaration by the blank-line rule (see § What an extent covers)
stays with that declaration instead.

`@imports` spans the whole import block, including any grouping comments
*between* imports — Go has one `import_declaration`, while TypeScript and
Python emit one node per import, so the anchor covers a run rather than a
single node. A comment after the last import belongs to whatever follows it,
not to the block.

Markdown has no import concept, so `@imports` resolves to nothing there — the
same degraded-but-not-an-error result `@header` already gives a TypeScript
file with no shebang. `@header` is YAML/TOML frontmatter (`---`/`+++`) when
present. `@toplevel` spans every heading section, first through last, plus a
file's lede — any content between frontmatter and its first heading — so the
whole document body is reachable through one pseudo-anchor or the other once
frontmatter is set aside.

A YAML file's own `@header` is a leading top-of-file comment run, not the
`---` document-start marker or a `%YAML`/`%TAG` directive — those stay with
whichever key's extent happens to contain them.

`@imports` resolves to nothing,
the same as Markdown.

`@toplevel` spans every top-level key, first through
last, which in practice is the whole document once `@header` is set aside.

An HTML file's own `@header` is its leading `<!DOCTYPE html>` plus any
comment immediately preceding or following it — a real, distinct node kind
in this grammar, unlike Markdown or YAML's comment-only preamble.

`@imports`
resolves to nothing: no node kind in this grammar plays the role of an
import statement, so a `<link rel="stylesheet">` or `<script src="...">` —
semantically import-shaped, but not a distinct node from any other tag — is
not reachable through it.

`@toplevel` spans every addressable (id-bearing)
element, first through last, which in a typical page — one root `<html>`
element enclosing everything — is the whole document once `@header` is set
aside, the same as it is for every other language with no `@imports` of its
own.

## Paths that anchors cannot address

Symbol anchors in `commit` are refused (exit 10) on symlinks,
gitlinks/submodules, binary, unmerged, skip-worktree, and assume-unchanged
paths. Name the path instead. Behaviour per kind:

| Kind | How it stages |
| --- | --- |
| Regular file | `git add <path>` — CRLF, LFS, and `.gitattributes` filters applied |
| Submodule | Gitlink SHA resolved from the submodule's `HEAD`, or the index if uninitialised |
| Symlink | The target string, mode `120000` |
| Rename | Nothing special — name both paths; git detects the rename at diff time |
| Gitignored | Refused (exit 7) unless already tracked in the index or `HEAD`, matching `git add` |
| Unmerged index path | `FILE:SYMBOL` refused with exit 10; name the path instead |
| Skip-worktree / assume-unchanged | `FILE:SYMBOL` refused with exit 10; bits left unchanged. Name the path instead |

A `FILE:SYMBOL` anchor into JSON, YAML, or TOML resolves fine for `diff`,
`blame`, and `log` — all three read-only — but `rgit commit` refuses it (exit
12): the format itself has no error `rgit` could rely on to catch a splice
that disagrees with the file's own grammar, so the write it would produce is
never trustworthy. Name the path instead. See
[`CODES.md`](CODES.md#exit-12-is-commits-alone).

## Language support

Eleven grammars ship unconditionally; a twelfth, SQL, ships only behind the
`rgit_sql` build tag (see its own row). All twelve claim these extensions:

| Grammar | Extensions | Addresses |
| --- | --- | --- |
| Go | `.go` | Every top-level declaration |
| TypeScript | `.ts`, `.mts`, `.cts` | Every top-level declaration |
| TSX/JavaScript | `.tsx`, `.jsx`, `.js`, `.mjs`, `.cjs` | As above; plain JS parses under the TSX grammar, a superset that also accepts untyped JS |
| Python | `.py`, `.pyi` | Every top-level declaration |
| Markdown | `.md`, `.markdown` | Headings and their sections only — inline constructs such as emphasis, links, and code spans are not parsed and have nothing to address |
| Shell | `.sh`, `.bash` | Functions and top-level variable assignments. Shell has no containers, so a redefined function disambiguates by ordinal the same way two same-named Go functions would |
| YAML | `.yaml`, `.yml` | Mapping keys, container-qualified one level the same way a Markdown heading is. Sequence items and anything inside a flow-style `{...}`/`[...]` value have no name to address |
| CSS | `.css` | Selectors and at-rules. `.scss`/`.sass` unsupported — see [`LIMITATIONS.md`](LIMITATIONS.md#unsupported-languages) |
| JSON | `.json` | Object key paths, container-qualified one level the same way a YAML mapping key is. Arrays and non-object documents have nothing to address |
| TOML | `.toml` | Key paths and `[table]`/`[[array]]` headers, container-qualified one level. Inline tables and arrays have nothing to address inside them |
| HTML | `.html`, `.htm` | An id-bearing element, tag-qualified (`div#app`), to any nesting depth. An element with no id has no anchor of its own — see [`LIMITATIONS.md`](LIMITATIONS.md#constructs-no-anchor-reaches) |
| SQL | `.sql` | `CREATE TABLE`/`VIEW`/`FUNCTION`/`INDEX`/`TRIGGER`/`TYPE`, schema-qualified one level. Ships behind the `rgit_sql` build tag ([`INSTALL.md`](INSTALL.md#sql-support)) |

A `---`-separated multi-document YAML stream, and a comment sitting between
the end of a nested value and the next, more shallowly indented key, are both
unaddressable by key — see
[`LIMITATIONS.md`](LIMITATIONS.md#constructs-no-anchor-reaches) for why.

A CSS selector's bare name is its own text, exactly as written —
`.button-primary`, `#app`, `div`, or a comma-joined list like `.a, .b`, which
stages as one anchor rather than two.

An at-rule (`@media`, `@supports`,
`@keyframes`, `@font-face`, and any custom at-rule the grammar accepts)
addresses by its full prelude, not the bare keyword —
`@media (max-width: 600px)` — because two at-rules of the same kind in one
file are the ordinary case, not a corner, and only the prelude tells them
apart; an at-rule with no prelude at all (`@font-face { ... }`) degrades to
the bare keyword.

Nested rules inside an `@media`/`@supports`/`@keyframes`
block are not descended into and have no anchor of their own — naming the
enclosing at-rule stages the whole block.

This is a different case from
native CSS Nesting, covered next, and unaffected by it: an at-rule is never
walked for nested rule sets, whether the at-rule sits at the top level or
inside another rule's own block.

`@import` is a real, distinct node kind, so
`@imports` is meaningful for CSS, unlike Markdown, YAML, JSON, or TOML — but
an `@import` is reachable only through `@imports`, never as a bare anchor of
its own, the same way a Go file's imports are invisible to its own
`Declarations`.

A rule directly nested inside another rule's own block — native CSS Nesting,
`.parent { .child { ... } }` — **is** addressable, unlike the at-rule case
above: naming `.child` alone works when it is unambiguous, and its qualified
form is `.parent .child`, joined with a literal space rather than the `.`
every other language's own container qualification uses, because that space
is the descendant combinator CSS itself would use to flatten the same
nesting (`.parent .child { ... }` means the same thing written flat).

Nesting
three levels deep qualifies by the immediate parent only, the same
one-level rule a YAML or JSON key nested three deep already follows: a rule
inside `.mid` inside `.outer` is addressed as `.mid .inner`, never the full
`.outer .mid .inner` chain.

**A generic at-rule can be spelled anything, including a pseudo-anchor's own
name** — CSS reserves no at-rule keywords, so `styles.css:@header` is legal
CSS and genuinely collides with the pseudo-anchor `@header`.

The
pseudo-anchor always wins: `rgit` checks for `@header`/`@imports`/`@toplevel`
before it ever consults a file's own symbols, so a same-spelled at-rule is
never reachable by that name under any circumstance — not merely
deprioritized.

It still shows up in `rgit diff`'s ordinary listing under its
own qualified name; only the anchor spelling `@header` itself is shadowed.

A JSON object's own key paths address the same way a YAML mapping key does:
`config.json:server.port` claims one nested key, and naming an object claims
everything under it.

JSON has no comment syntax, so `@header` and `@imports`
both resolve to nothing there, the same degraded-but-not-an-error result
Markdown gives a file with no shebang.

**Breadth overstates the value here**
— most JSON `rgit` runs against in practice is `package.json`, a tsconfig,
or a lockfile, all of which want whole-path staging regardless of whether a
key anchor exists; this grammar earns its place on the narrower case where a
single nested config key is genuinely the unit that changed, not by making
every JSON file's full contents individually addressable.

An array, at any
depth, is a leaf — `list` addresses the whole array, never one element.

TOML addresses the same way, with one more form: a `[table]` or
`[[array]]` header is itself addressable by its own bracket text
(`config.toml:server` claims the whole table), and its members qualify
under that same text verbatim — `server.tls`'s own members address as
`server.tls.<key>`, not a further-nested path, since the header's dotted
spelling already is the container.

Two array-of-tables entries sharing one
header (`[[servers]]` twice) collide the same way two same-named Go
functions do — `servers#1`/`servers#2`, ordinal by source order, not by
array index — the same for their own same-named members.

A dotted pair key
written directly (`a.b = 1`, legal at the document root or inside a table
body) is not split into its own container and leaf; its bare name is the
full dotted spelling, one anchor rather than a second qualification scheme.

Naming a table also stages any blank line between it and the next section
header — the grammar attributes that gap to the table itself, since nothing
else could claim it.

Inline tables (`{ a = 1 }`) and arrays are leaves,
never descended into, the same as YAML's flow-style values.

SQL addresses `CREATE TABLE`, `CREATE VIEW`, `CREATE FUNCTION`, `CREATE
INDEX`, `CREATE TRIGGER`, and `CREATE TYPE` by the name being defined —
`schema.sql:users`, `schema.sql:active_users`.

`DROP`, `ALTER`, `INSERT`,
`SELECT`, and `CREATE SCHEMA` all parse but declare no persistent named
object, so none is addressable; naming the path stages those the same way it
does an unaddressable shape in any other language.

A schema-qualified name
(`CREATE TABLE s.t`) qualifies one level, the same as a Go receiver or a TOML
table header — `s.sql:s.t` addresses it, and a bare `t` disambiguates by
ordinal against another schema's `t` in the same file the way two identically
named Go functions do.

`CREATE TRIGGER` never carries a schema qualifier of
its own — Postgres does not allow one — so two same-named triggers in one
file (legal when they fire on different tables) disambiguate by ordinal only;
there is no qualifier that captures which table each fires on.

`CREATE INDEX`
with no name (`CREATE INDEX ON t (c)`, legal SQL — the database assigns one)
is unaddressable, the same as any other symbol with no name of its own to
read. SQL ships behind the `rgit_sql` build tag rather than unconditionally
— [`INSTALL.md`](INSTALL.md#sql-support) covers what that means for a build
and what a user without the tree-sitter CLI loses.

HTML addresses one shape only: an element carrying an `id` attribute,
tag-qualified as `div#app` — element and id, nothing wider.

There is
deliberately no anchor for a class, an attribute selector, `nth-of-type`, a
pseudo-class/pseudo-element, or a descendant/child/sibling combinator — see
[`LIMITATIONS.md`](LIMITATIONS.md#constructs-no-anchor-reaches) for the full
list and why.

`rgit` resolves anchors; it is not a CSS selector engine.

An
element is addressable at any nesting depth, not only at the top level — a
mount point (`div#app`) five levels deep inside a full page shell resolves
the same as one at the root — and naming an outer element claims everything
nested inside it, id-bearing descendants included, the same "naming the
container claims its members" rule every other language's own container
qualification already follows.

An element with **no** id gets no anchor of
its own at all, even when it is the only one of its tag in the file: this
resolver's index has no per-parent scoping, so falling back to a bare tag
name would make ordinary tags like `div` collide across nearly every real
document; only an element's own written text (its tag name and its id's
value, both taken verbatim) is ever staged, matching how a CSS selector
already stages.

Attribute names, including `id` itself, are matched
case-insensitively (`ID`, `Id`, and `id` all recognize the same attribute),
per the WHATWG HTML spec — the id's own value is not case-folded.

Two
elements sharing one id — the same tag twice, or two different tags — collide
in this resolver's index exactly the way two identically named Go functions
or two identical CSS selectors already do: exit 4 (ambiguous), listing each
element's own tag-qualified spelling as a candidate.

A file whose extension claims no grammar is matched by its shebang instead, so
an extensionless `bin/` script or git hook is addressable like any other file.
`bash` and `sh` resolve to the shell grammar and `python3`/`python` to Python,
in both the `#!/bin/sh` and `#!/usr/bin/env sh` spellings. Versioned Python 3
interpreters such as `python3.12` and `python3.13` resolve the same way;
Python 2 names remain unsupported. **`zsh` is deliberately excluded** — the
bash grammar mis-parses zsh-specific syntax, and a wrong extent is worse than
an honest refusal. The extension is always tried first, so this changes
nothing for a file that has one. A trailing `.exe` on the interpreter basename
is stripped case-insensitively before lookup; other Windows suffixes are not.

The Node/TypeScript ecosystem routes to the TypeScript adapter the same way:
`node`, `nodejs`, `tsx`, `ts-node`, `bun`, and `deno` all resolve to it, whether
spelled directly (`#!/usr/bin/env node`) or via `env -S` with a flag of the
interpreter's own (`#!/usr/bin/env -S node --import tsx`). `npx NAME` and
`bunx NAME` unwrap once further to `NAME` itself — both are package runners,
not interpreters, and `NAME` is what actually decides the language
(`#!/usr/bin/env npx tsx` is TypeScript, not "npx"). Versioned Node and Bun
names such as `node20` and `bun1.2` resolve to TypeScript too. No new grammar
is added for any of this; every one of them routes to a language this resolver
supports from an extensionless shebang.

The shebang is read from the worktree when that copy exists. If the worktree
copy is absent, the resolver samples the first 256 bytes of the `HEAD` blob
through `git cat-file` instead, so a deleted script or a revision comparison
can still use its grammar and symbol extents. The extension still wins, and an
unmapped shebang remains exit 9.

The language-server cross-check covers Go, TypeScript/TSX, Python, Shell,
YAML, JSON, CSS, Markdown, and HTML
([`docs/INSTALL.md#language-servers`](INSTALL.md#language-servers)). TOML
and SQL stay `[ts-only]` permanently instead, which is a supported result,
not a degraded one — see
[`LIMITATIONS.md`](LIMITATIONS.md#language-server-coverage) for why. Exit 9
is reserved for a language with no grammar at all — see
[`LIMITATIONS.md`](LIMITATIONS.md#unsupported-languages) for the current
examples; name the path instead.

## Deletions

Deleting a symbol is anchored like any other change — `rgit` resolves the
extent against `HEAD` when it is gone from the worktree, and stages its removal.
`rgit diff` marks these `DELETED`.
