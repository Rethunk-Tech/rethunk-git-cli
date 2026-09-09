# HUMANS.md

Run and use `rgit`. Internals: [AGENTS.md](AGENTS.md).

## Quick start

```bash
cd /some/git/repo
rgit diff                                        # what can I commit?
rgit commit -m "fix(auth): reject expired" auth.go:ValidateToken
rgit -C /some/other/repo diff                    # ...without standing in it
```

Install, env vars, verify, uninstall: [docs/INSTALL.md](docs/INSTALL.md).

## What it does

`rgit commit` stages **one symbol at a time** instead of one file. Name `auth.go:ValidateToken` to commit that function — doc comment, attributes, body — leaving other edits in the file uncommitted.

`rgit diff` shows committable anchors that `rgit commit` accepts. `rgit show FILE:SYMBOL` prints one symbol's own bytes, at the worktree or at a revision (`--source`). `rgit blame FILE:SYMBOL` and `rgit log FILE:SYMBOL` bound those commands to a symbol's extent ([docs/USAGE.md](docs/USAGE.md)). `rgit context` streams branch, warnings, diff rows, and recent commits in one call ([docs/CODES.md](docs/CODES.md#output-records)).

Everything else stays plain `git`. Full command reference: [docs/USAGE.md](docs/USAGE.md); anchors: [docs/ANCHORS.md](docs/ANCHORS.md).

## Inherited git behaviour

`rgit` deliberately behaves like `git add <pathspec> && git commit`. Surprising consequences are git's own — see [docs/USAGE.md § Behaviour inherited from git](docs/USAGE.md#behaviour-inherited-from-git).

## Anchor limits

Anchors need a parsed syntax tree — refused on binaries, symlinks, submodules, and unsupported languages ([docs/LIMITATIONS.md](docs/LIMITATIONS.md#unsupported-languages)). `rgit commit` also refuses `FILE:SYMBOL` on JSON, YAML, or TOML (other commands still resolve); name the path instead. Details: [docs/ANCHORS.md § Paths that anchors cannot address](docs/ANCHORS.md#paths-that-anchors-cannot-address).

## Degraded mode

With no language server, `rgit` resolves with tree-sitter alone and prints `[ts-only]` on stderr — safe, skips macro/build-tag cross-checks. Never blocks on a cold server.
