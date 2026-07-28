# HUMANS.md

Everything needed to run and use `rgit`. Internals live in
[`AGENTS.md`](AGENTS.md); the reasoning behind the design is in
[`specs/design.md`](specs/design.md).

## Quick start

```bash
go build -ldflags="-s -w" -o rgit . && install -m 0755 rgit ~/.local/bin/rgit

cd /some/git/repo
rgit diff                                        # what can I commit?
rgit commit -m "fix(auth): reject expired" auth.go:ValidateToken
```

Prerequisites, language-server setup, environment variables, verification, and
uninstall: [`docs/INSTALL.md`](docs/INSTALL.md).

## What it does

`rgit commit` stages **one symbol at a time** instead of one file at a time.
Naming `auth.go:ValidateToken` commits that function — its doc comment,
attributes, and body — and leaves every other edit in the file uncommitted.

`rgit diff` shows what is committable, labelled with the exact anchors
`rgit commit` accepts, so the output of one is the input of the other.

Everything else stays plain `git`. `rgit` has two commands and no opinions
about the rest of your workflow.

## Reference

| Topic | Where |
| --- | --- |
| Install, language servers, env vars, verify, uninstall | [`docs/INSTALL.md`](docs/INSTALL.md) |
| Commands, argument grammar, flags, exit codes, diff scope | [`docs/USAGE.md`](docs/USAGE.md) |
| Anchor syntax, extents, pseudo-anchors, special paths | [`docs/ANCHORS.md`](docs/ANCHORS.md) |
| Migrating off the `rethunk-git` MCP | [`specs/CUTOVER.md`](specs/CUTOVER.md) |

## Things worth knowing before you rely on it

`rgit` deliberately behaves like `git add <pathspec> && git commit`, which has
consequences people are sometimes surprised by:

- **Work you staged earlier comes along.** If you ran `git add` before invoking
  `rgit`, that work is in the commit. This is git's behaviour, not an oversight.
- **A rejected commit leaves your staging alone.** Nothing is rolled back,
  exactly as with `git commit`.
- **Hooks can stage things you did not name.** A `pre-commit` hook running
  `git add -A` sweeps the worktree, under `rgit` just as under `git`. Use
  `--no-verify` when that matters.

The full list, with what was measured to establish each:
[`specs/design.md`](specs/design.md#governing-principle).

## When a symbol anchor will not work

Anchors need a parsed syntax tree, so they are refused on binaries, symlinks,
submodules, and languages outside the v1 set (Go, TypeScript/JavaScript,
Python). Name the path instead — `rgit commit package.json` works fine.

A `chmod +x` with no content change has no symbols to name either; `rgit diff`
still lists it as `MODE` so it is never silently missed.

## Degraded mode

If no language server is reachable, `rgit` resolves symbols with tree-sitter
alone and prints `[ts-only]` on stderr. This is normal and safe — it simply
skips the cross-check that catches build-tag and macro edge cases. `rgit` never
blocks waiting for a cold language server.
