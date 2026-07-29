# Changelog

Notable changes to `rgit`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- `rgit doctor` aligns its detail column across both report sections. A
  language-server label longer than the old fixed 24-character field pushed
  its own path out of line, and `[ok]` / `MISSING` being different widths
  shifted the name column by status. Both are padded now.

## [1.0.0] — 2026-07-28

First public release.

`rgit` is `git add <pathspec> && git commit` at symbol granularity. Naming
`auth.go:ValidateToken` commits that one function — doc comment, attributes,
body — and leaves every other edit in the file uncommitted. Two commands,
`diff` and `commit`; everything else stays plain `git`.

What ships:

- **Symbol anchors** — `FILE:NAME` addresses a declaration or a container
  member, with `@header`, `@imports`, and `@toplevel` reaching the regions no
  symbol owns ([`docs/ANCHORS.md`](docs/ANCHORS.md)).
- **Bare positionals** — pathspecs, revisions, and anchors mix freely in one
  invocation under a six-rule precedence table, with no escape syntax
  ([`docs/USAGE.md`](docs/USAGE.md#argument-shape)).
- **Ten grammars unconditionally** — Go, TypeScript, TSX/JavaScript, Python,
  Markdown, Shell, YAML, CSS, JSON, and TOML — plus SQL behind the `rgit_sql`
  build tag ([`docs/INSTALL.md`](docs/INSTALL.md#sql-support)).
- **Language-server cross-check** for 9 of those 11 grammars, with tree-sitter
  always producing the extent that gets staged. A missing or cold server
  degrades to `[ts-only]` rather than blocking
  ([`docs/LIMITATIONS.md`](docs/LIMITATIONS.md#language-server-coverage)).
- **A machine contract** — stable exit codes and `--porcelain` records, with
  `rgit completion bash|zsh` as an in-repo consumer of the latter
  ([`docs/CODES.md`](docs/CODES.md)).
- **Git semantics throughout** — git's exit codes, pathspecs, hooks, filters,
  trailers, and signing, because git does all of it. Divergence is a bug
  ([`docs/USAGE.md`](docs/USAGE.md#behaviour-inherited-from-git)).

Known limitations are catalogued in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md); the reasoning and the
measurements behind every decision are in
[`specs/design.md`](specs/design.md).
