<h1 align="center">rgit</h1>

<div align="center">

[![version](https://img.shields.io/badge/version-v1.0.0-brightgreen)](CHANGELOG.md)
[![go](https://img.shields.io/badge/go-1.26%2B-00ADD8)](https://go.dev)
[![license](https://img.shields.io/badge/license-MIT-green)](LICENSE)

</div>

---

Git stages files. `rgit` stages **symbols**.

Naming `auth.go:ValidateToken` commits that one function — doc comment,
attributes, body — and leaves every other edit in the file uncommitted. No line
numbers, no interactive hunk picking, no `git add -p` transcript to get wrong.
Anchors are resolved from a real syntax tree and cross-checked against a
language server, so they survive the edits that break line-based staging.

Underneath, `rgit` is `git add <pathspec> && git commit` at symbol granularity
and nothing more. Hooks run, filters apply, pre-staged work comes along, trailers
and signing work — because git does all of it. Two staging commands
(`diff`, `commit`), `blame` bounded to a symbol's own extent, `log` for its
patch-free history, `context` for one-call repository orientation, plus
meta subcommands (`languages`, `doctor`, `completion`); everything else
stays plain `git`.

Start with [HUMANS.md](HUMANS.md) to run it, or
[docs/INSTALL.md](docs/INSTALL.md) to build it.

## Highlights

- **Symbol anchors, not line ranges** — `FILE:NAME` addresses a function, method,
  type, or const; `@imports`, `@header`, and `@toplevel` reach the regions no
  symbol owns.
- **Bare positionals** — `rgit commit -m "…" auth.go:Validate package.json` mixes
  symbols and paths, accepts every git pathspec form, and needs no flags.
- **Closed loop** — `rgit diff` emits exactly the anchors `rgit commit` consumes.
- **Verified extents** — tree-sitter resolves, a language server cross-checks;
  `commit` hard-fails on a mismatch, `diff` reports it as a warning
  ([docs/CODES.md](docs/CODES.md#exit-6-is-commits-alone)).
- **Git semantics throughout** — git's exit codes, git's pathspecs, git's hooks
  and config. Divergence is treated as a bug.
- **Go, TypeScript/JavaScript, Python, Markdown, Shell, YAML, CSS, JSON,
  TOML**, plus SQL in a build with the `rgit_sql` tag; anything else stages
  by path.

## Documentation

| Document | Contents |
| --- | --- |
| [HUMANS.md](HUMANS.md) | Run and use: quick start, behaviour, degraded mode |
| [docs/INSTALL.md](docs/INSTALL.md) | Build, installer, cross builds, language servers, env vars, verify, uninstall |
| [docs/USAGE.md](docs/USAGE.md) | Commands, argument grammar, flags |
| [docs/ANCHORS.md](docs/ANCHORS.md) | Anchor syntax, extents, pseudo-anchors, special paths |
| [docs/CODES.md](docs/CODES.md) | Exit codes and `--porcelain` record formats |
| [docs/LIMITATIONS.md](docs/LIMITATIONS.md) | What `rgit` does not do, and why |
| [AGENTS.md](AGENTS.md) | Internals, invariants, delegation boundary |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Commits, tests, dependency policy |
| [CHANGELOG.md](CHANGELOG.md) | What changed in each release |
| [SECURITY.md](SECURITY.md) | Reporting a vulnerability, and what is in scope |
| [specs/design.md](specs/design.md) | Design record and the measurements behind it |
| [TODO.md](TODO.md) | Backlog |

## Status

Both staging commands are implemented for Go, TypeScript/JavaScript, Python, Markdown,
Shell, YAML, CSS, JSON, and TOML, plus SQL in a build with the `rgit_sql` tag,
with the language-server cross-check live for Go, TypeScript/TSX, Python,
Shell, YAML, JSON, CSS, and Markdown. See [specs/design.md](specs/design.md)
for what was measured, [docs/LIMITATIONS.md](docs/LIMITATIONS.md) for what
`rgit` does not do, and [TODO.md](TODO.md) for what's deferred.

## License

MIT © Rethunk.Tech
