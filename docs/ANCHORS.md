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

| Form | Example | Notes |
| --- | --- | --- |
| Bare | `auth.go:ValidateToken` | Fails with exit 4 if several symbols match |
| Container-qualified | `auth.go:A.Get` | Preferred |
| gopls spelling | `auth.go:(*A).Get` | Accepted on input; needs shell quoting |
| Ordinal | `auth.go:init#2` | Last resort; warns and suggests qualification |

`rgit` accepts gopls's `(*A).Get` spelling so an anchor copied from an IDE
outline resolves, but always **emits** `A.Get` — the parenthesised form contains
`*` and parens that the shell globs unless quoted.

Ordinals are positional, so an inserted symbol repoints them. `rgit diff` emits
the qualified anchor when unambiguous and the ordinal form otherwise, so
copy-paste always matches resolution.

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

v1 vendors three grammars: **Go, TypeScript/JavaScript** (including TSX/JSX),
and **Python**. Anything else → exit 9 on a symbol anchor; name the path.

The grammars deferred to v2 are listed in [`../TODO.md`](../TODO.md).

## Deletions

Deleting a symbol is anchored like any other change — `rgit` resolves the
extent against `HEAD` when it is gone from the worktree, and stages its removal.
`rgit diff` marks these `DELETED`.
