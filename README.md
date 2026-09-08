<h1 align="center">rgit</h1>

<div align="center">

[![version](https://img.shields.io/badge/version-v2.0.0-brightgreen)](CHANGELOG.md)
[![go](https://img.shields.io/badge/go-1.27%2B-00ADD8)](https://go.dev)
[![license](https://img.shields.io/badge/license-MIT-green)](LICENSE)

</div>

---

Git stages files. `rgit` stages **symbols**.

Naming `auth.go:ValidateToken` commits that one function and leaves every other edit uncommitted. No line numbers, no `git add -p`.

## Quick start

```bash
cd /some/git/repo
rgit diff
rgit commit -m "fix(auth): reject expired" auth.go:ValidateToken
```

Build and runbook: [HUMANS.md](HUMANS.md).

## Highlights

- **Symbol anchors** — `FILE:NAME` addresses a function, method, type, or const; `@imports`, `@header`, `@toplevel` reach regions no symbol owns.
- **Bare positionals** — mix symbols and paths, every git pathspec form, no flags required.
- **Closed loop** — `rgit diff` emits exactly the anchors `rgit commit` consumes.
- **Verified extents** — tree-sitter resolves; a language server cross-checks; mismatch hard-fails on `commit`.
- **Git semantics** — git exit codes, pathspecs, hooks, and config throughout.
- **Eleven grammars ship** — SQL behind `rgit_sql` tag; anything else stages by path ([docs/LIMITATIONS.md](docs/LIMITATIONS.md)).

## Documentation

| Document | Contents |
| --- | --- |
| [HUMANS.md](HUMANS.md) | Run and use: install, usage, degraded mode |
| [docs/INSTALL.md](docs/INSTALL.md) | Build, language servers, env vars, verify, uninstall |
| [docs/USAGE.md](docs/USAGE.md) | Commands, argument grammar, flags |
| [docs/ANCHORS.md](docs/ANCHORS.md) | Anchor syntax, extents, special paths |
| [docs/CODES.md](docs/CODES.md) | Exit codes and `--porcelain` formats |
| [docs/LIMITATIONS.md](docs/LIMITATIONS.md) | What `rgit` does not do |
| [AGENTS.md](AGENTS.md) | Internals, invariants, delegation boundary |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Commits, tests, dependency policy |
| [CHANGELOG.md](CHANGELOG.md) | Release notes |
| [SECURITY.md](SECURITY.md) | Vulnerability reporting |
| [specs/design.md](specs/design.md) | Design record and measurements |

## License

MIT © Rethunk.Tech
