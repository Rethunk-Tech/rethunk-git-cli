# Contributing

Read [`AGENTS.md`](AGENTS.md) first — it holds the one invariant every change is
measured against, and the list of things that break silently.

## Before you change behaviour

`rgit` matches git rather than inventing semantics. If a change makes `rgit`
behave differently from `git add <pathspec> && git commit`, say so explicitly in
the PR and justify it. Several earlier designs were reverted for diverging
quietly; the record is in [`specs/design.md`](specs/design.md).

Claims in that record are backed by measurement. If you contradict one, measure
it again and update the record — do not simply reword it.

## Commits

Conventional commits: `type(scope): subject`.

- Subject is imperative and under ~72 characters.
- Body explains **why**, not which files changed.
- One logical unit per commit.
- No AI attribution trailers.

## Tests

**Least tests, highest coverage. The suite stays under 30s** (ideally under 10).
Exactly three files, each holding one happy path plus the edge cases that have
actually bitten — no permutation laundry lists.

| File | Happy path | Critical edge cases |
| --- | --- | --- |
| `rgit_e2e_test.go` | Init repo → edit symbol → `rgit diff` → `rgit commit` → verify HEAD, clean index, hook ran, worktree preserved | Pre-staged sibling file comes along; hook rejection leaves staging intact (exit 128); resolve-all-before-stage leaves index untouched on failure; positional pathspec parity with `--file`; path escape rejection (exit 129) |
| `index_test.go` | Single-symbol blob synthesis staged into the real index | Initial commit on an unborn branch; no-newline-at-EOF preserved; rename staged as two paths yields git's `R100`; pathspec glob and `:(exclude)` pass through; mode-only change surfaces as `MODE`; submodule and symlink staging; `.gitattributes` clean filter (the `--path` requirement) |
| `resolver_test.go` | Tree-sitter extent + doc-comment attribution on a Go fixture | Exit 11 (all unchanged) vs exit 3 (unresolvable); new-symbol insertion when neighbours are also new; `@header` auto-stage compiles; live `gopls` cross-check |

Units run against in-memory tree-sitter and a mock LSP. The live-`gopls` check
skips cleanly when the binary is absent or `-short` is set — mocks cannot catch
a change in real language-server range semantics, which is the class of defect
that invalidated an earlier design.

**Write tests before implementation.** The [`spike/`](spike/) prototypes are the
executable source for the first two files; port them, then delete the directory.

```bash
go test ./...          # full suite
go test -short ./...   # skip the live language-server check
```

## Dependencies

Binary size and dependency count are not constraints — but every dependency
needs a specific, measured justification, recorded in
[`specs/design.md`](specs/design.md#dependencies). "It is the standard choice
for this category" is not one.

Anything that reimplements what git already does is rejected on principle; see
the delegation boundary in [`AGENTS.md`](AGENTS.md#delegation-boundary).

## Documentation

This repo follows the tiered doc layout: README orients and links, `HUMANS.md`
is the authoritative run/use surface, `AGENTS.md` holds internals,
`CONTRIBUTING.md` holds process, `specs/` holds the design record, deep
reference lives in `docs/`.

Do not repeat content between tiers. If something belongs in two places, it
belongs in one and gets linked from the other.

Two repo conventions that are easy to undo by accident:

- **`docs/` is only for documentation shipped with the tool.** Design record and
  migration material go in `specs/`.
- **`AGENTS.md` uses `@-references` (`@specs/design.md`), not markdown links.**
  An agent resolves those without spending a tool call; ordinary links cost a
  `Read` round-trip each. Every other file uses normal markdown links, because
  `@-refs` render as literal text for humans.

Lint before opening a PR that touches any `.md`:

```bash
python3 ~/.claude/skills/doc-audit/scripts/doc-audit.py
```
