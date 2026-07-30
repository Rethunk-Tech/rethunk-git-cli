# HUMANS.md

The starting point for running and using `rgit`, pointing into
[`docs/`](docs/) for the full reference. Internals live in
[`AGENTS.md`](AGENTS.md); the reasoning behind the design is in
[`specs/design.md`](specs/design.md).

## Quick start

```bash
cd /some/git/repo
rgit diff                                        # what can I commit?
rgit commit -m "fix(auth): reject expired" auth.go:ValidateToken
rgit -C /some/other/repo diff                    # ...without standing in it
```

Build, prerequisites, language-server setup, environment variables,
verification, and uninstall: [`docs/INSTALL.md`](docs/INSTALL.md).

## What it does

`rgit commit` stages **one symbol at a time** instead of one file at a time.
Naming `auth.go:ValidateToken` commits that function — its doc comment,
attributes, and body — and leaves every other edit in the file uncommitted.

`rgit diff` shows what is committable, labelled with the exact anchors
`rgit commit` accepts, so the output of one is the input of the other.

`rgit blame FILE:SYMBOL` bounds `git blame` to just that symbol's own lines
instead of the whole file.

`rgit log FILE:SYMBOL` shows that symbol's own history — one line per
touching commit, patch-free unless you ask for one with `-p`.

`rgit context` is one-call orientation for a fresh session: recent commits,
plus the same per-symbol diffstat `rgit diff` reports, as a single record
stream — instead of a status, a diffstat, a diff, and a log call separately.

Everything else stays plain `git`. `rgit` has two staging commands — plus
`blame`, `log`, `context`, `languages`, `doctor`, and `completion` for
everything around them — and no opinions about the rest of your workflow.

Commands and flags are in [`docs/USAGE.md`](docs/USAGE.md); anchor syntax is
in [`docs/ANCHORS.md`](docs/ANCHORS.md); exit codes and the `--porcelain`
record formats are in [`docs/CODES.md`](docs/CODES.md). The full index of
every document lives in [`README.md`](README.md#documentation).

## Things worth knowing before you rely on it

`rgit` deliberately behaves like `git add <pathspec> && git commit`. Most of
that is unremarkable, but a few consequences surprise people — none of them
oversights; each is what plain `git commit` already does.

The full list is in
[`docs/USAGE.md`](docs/USAGE.md#behaviour-inherited-from-git); what was
measured to establish each is in
[`specs/design.md`](specs/design.md#governing-principle).

## When a symbol anchor will not work

Anchors need a parsed syntax tree, so they are refused on binaries, symlinks,
submodules, and any language with no grammar — see
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md#unsupported-languages) for which.
A `chmod +x` with no content change has nothing to name either. In every case,
name the path instead — `rgit commit package.json` works fine, and `rgit diff`
never reports such a file as clean.

Which paths are refused, and how each kind stages:
[`docs/ANCHORS.md`](docs/ANCHORS.md#paths-that-anchors-cannot-address).

## Degraded mode

If no language server is reachable, `rgit` resolves symbols with tree-sitter
alone and prints `[ts-only]` on stderr. This is normal and safe — it simply
skips the cross-check that catches build-tag and macro edge cases. `rgit` never
blocks waiting for a cold language server.
