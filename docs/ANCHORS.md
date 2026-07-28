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
(`ns.ts:N.inner`), and a Markdown heading (`USAGE.md:install.options`). The
container itself stays addressable by its bare name, and naming it claims
every member — that is what asking for the container means.

Qualification is always the *nearest* container, one level, never a full
breadcrumb — a Markdown `### Options` nested under `## Setup` nested under
`# Install` still addresses as `setup.options`, not `install.setup.options`,
the same ceiling a Go struct's field or a TypeScript namespace's member uses.

A struct field line naming several identifiers at once (`A, B int`) and an
embedded/anonymous field are not addressable by their own anchor: the first
has no way to give one name its own extent without the other's text coming
along, and the second has no name of its own to begin with. Name the
containing type or the path instead. TypeScript's anonymous default export
(`export default function () {}`) is unaddressable for the same reason as the
embedded field — the grammar gives it no name to read.

The two nest differently, which matters when the container is new. A Go method
sits beside its type rather than inside it, so staging one never drags the type
along. A class encloses its members, so naming a member of a class absent from
`HEAD` stages the whole class: there is no way to add a method to a class that
does not exist yet. `rgit` says so on stderr rather than doing it quietly.

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
stderr).

`@imports` spans the whole import block, including any grouping comments
*between* imports — Go has one `import_declaration`, while TypeScript and
Python emit one node per import, so the anchor covers a run rather than a
single node. A comment after the last import belongs to whatever follows it,
not to the block.

Markdown has no import concept, so `@imports` resolves to nothing there — the
same degraded-but-not-an-error result `@header` already gives a TypeScript
file with no shebang. `@header` is YAML/TOML frontmatter (`---`/`+++`) when
present. `@toplevel` spans every heading section, first through last — the
whole document body once frontmatter is set aside. A file's lede (any content
between frontmatter and its first heading) is presently reachable only by
naming the path, not by either pseudo-anchor.

## Paths that anchors cannot address

Symbol anchors are refused (exit 10) on symlinks, gitlinks/submodules, and
binary or non-parseable files. Name the path instead. Behaviour per kind:

| Kind | How it stages |
| --- | --- |
| Regular file | `git add <path>` — CRLF, LFS, and `.gitattributes` filters applied |
| Submodule | Gitlink SHA resolved from the submodule's `HEAD`, or the index if uninitialised |
| Symlink | The target string, mode `120000` |
| Rename | Nothing special — name both paths; git detects the rename at diff time |
| Gitignored | Refused (exit 7) unless already tracked, matching `git add` |

## Language support

v1 vendors four grammars: **Go, TypeScript/JavaScript** (including TSX/JSX),
**Python**, and **Markdown** (`.md`, `.markdown`) — headings and their
sections only; Markdown's inline constructs (emphasis, links, code spans) are
not parsed and have nothing to address. Anything else → exit 9 on a symbol
anchor; name the path.

The grammars deferred to v2 are listed in [`../TODO.md`](../TODO.md).

## Deletions

Deleting a symbol is anchored like any other change — `rgit` resolves the
extent against `HEAD` when it is gone from the worktree, and stages its removal.
`rgit diff` marks these `DELETED`.
