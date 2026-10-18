# Contributing

Read [`AGENTS.md`](AGENTS.md) first.

## Before you change behaviour

A change that makes `rgit` diverge from `git add <pathspec> && git commit`
must say so in the PR. Deliberate divergence lives next to the implementation;
rejected alternatives live in [`TODO.md`](TODO.md). Contradicting either
needs a new measurement, not a reword.

## Commits

`type(scope): subject` — imperative, ~72 characters, one unit, why in the
body, no AI trailers.

## Changelog

[`CHANGELOG.md`](CHANGELOG.md): Keep a Changelog, newest first. Add to
Unreleased in the same commit as a behaviour change, new grammar, flag, or
exit code. Refactors, tests, and docs do not. Link the owning reference;
do not restate it.

Tag `vX.Y.Z` to cut a release
([`.github/workflows/release.yml`](.github/workflows/release.yml)). That
workflow fails if any artifact's `rgit languages` has no `sql` row:

- linux/amd64 and windows/amd64 run directly (windows via Wine)
- linux/arm64 runs in an arm64 container under QEMU (dynamically linked;
  bare QEMU has no aarch64 glibc sysroot)

See [`docs/INSTALL.md`](docs/INSTALL.md#cross-builds). Move Unreleased under
the new heading and bump the README version badge (a static shield).

Check `sqlGrammarVersion` in
[`cmd/rgit-install/main.go`](cmd/rgit-install/main.go) against
[tree-sitter-sql](https://github.com/DerekStride/tree-sitter-sql) tags:
`@latest` cannot track it because that module gitignores `parser.c`
([`internal/resolve/sqlgrammar/grammar.go`](internal/resolve/sqlgrammar/grammar.go)).
A newer tag: bump the constant, `make sql-parser`, `go test -tags rgit_sql
./...`, update fixtures whose extents shifted, then tag.

## Tests

**Least tests, highest coverage. Suite under 30s** (ideally under 10). One
happy path per file plus edges that have bitten.

[`.github/workflows/ci.yml`](.github/workflows/ci.yml): the unit lane is
`gate` (`.gate.toml`); the full suite is `-race` over every test, including
those `-short` skips. Coverage is printed, not gated. Lane and numbers in
[§ Coverage](#coverage) are review discipline.

- **Unit lane (`-short`) is where a regression must fail.**
  `internal/app/app_test.go` drives `app.Run`; `internal/app/lanes_test.go`
  is the same for hooks, the real index, and forwarded git flags;
  `internal/cli/precedence_test.go` is the six-rule table. `index_test.go`
  lives in `cmd/rgit` but drives `synth.Stage` and is not skipped by
  `-short`.
- **`rgit_e2e_test.go` is the slow lane.** It builds and execs the binary.
  `-short` skips the file.

Temporary repos come from [`internal/gittest`](internal/gittest/gittest.go).

| File | Happy path | Critical edges |
| --- | --- | --- |
| `rgit_e2e_test.go` | Init → edit → `diff` → `commit` → HEAD, clean index, hook, worktree | Hook rejection rolls staging back (exit 128; also unit-pinned); `--` and leading-colon magic reach real `git add`; pathspec parity; path escape and malformed `--sym` (129); subdirectory paths; unborn branch; `--fixup` + `-m`; `--gpg-sign`/`--no-gpg-sign`/`--push`; completion in bash/zsh/fish/pwsh; exit codes and help as a process |
| `index_test.go` | Single-symbol blob into the real index | Unborn + gitignore (7); EOF newline; rename as two paths; glob/`:(exclude)`; `MODE`; submodule/symlink; clean filter (`--path`); nested extents; new-file preamble; class members without siblings; Go method stays a sibling; deletion stays parseable; YAML round-trip; resolve-all-before-stage; nearest-sibling insert; reverse-offset splice; no invented blank (PEP 8 excepted); `--dry-run` vs numstat |
| `resolver_test.go` | Tree-sitter + doc comment on a Go fixture | Exit 3 + did-you-mean; new-symbol neighbours; `@header`/`@imports`/`@toplevel`; live `gopls` |

Prefer a real dependency over a double. A live language server is dialled
when installed; the live-`gopls` check skips when the binary is absent or
`-short` is set. `-short` never reaches the managed gopls daemon:
`internal/lsptest.RunWithoutManagedDaemon` points `XDG_RUNTIME_DIR` at a
directory rgit refuses.

**Write tests before implementation.** Cases call `t.Parallel()` unless they
need `t.Setenv` or `t.Chdir`. `internal/app` uses `-C dir`; only
working-directory cases change directory.

```bash
go test ./...          # full suite
go test -short ./...   # unit lane
go test -race ./...    # full tree
```

### Coverage

Always `-coverpkg=./...` — much of the suite drives another package.

```bash
go test -coverpkg=./... -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -1
go test -short -coverpkg=./... -coverprofile=short.out ./...
go tool cover -func=short.out | tail -1
```

The **`-short` number** is the one a regression must fail in. A package that
drops without the binary is a unit-lane gap.

`rgit_e2e_test.go` builds with `-cover` and discards `GOCOVERDIR` unmerged,
so the full and `-short` totals matching is that artifact, not proof the
unit lane covers the binary. Merge first: `go tool covdata textfmt
-i=<GOCOVERDIR> -o=e2e.out`.

### Benchmarks

`BenchmarkAttribution_200MemberClass` in `internal/diff/bench_test.go` is a
regression gate on the held-open parse (`attributeSymbolsOpen`). Compare
`ns/op` to a prior run, not an absolute:

```bash
go test -bench=Attribution -benchmem ./internal/diff/
```

Not in CI. Run by hand around changes to `internal/diff/attribute.go` or
`internal/resolve` parse caching.

## Modernization

```bash
go fix -diff ./...
go fix ./...         # run twice; fixes unlock fixes
```

Only applies constructs the `go` directive already permits. Read the diff:
mechanical `a, b := before, after` is worth collapsing.

## Linting

```bash
make lint
```

[`.golangci.yml`](.golangci.yml): `default: standard` plus the fleet enable
list. One errcheck exemption: `fmt.Fprint*` to the caller's stdout/stderr.

## Dependencies

Every dependency needs a measured justification, recorded with it or in
[`TODO.md`](TODO.md#dependencies). Reimplementing git is rejected
([`AGENTS.md`](AGENTS.md#delegation-boundary)).

## Documentation

README orients; `HUMANS.md` is run-and-use; `docs/` is the reference;
`AGENTS.md` is internals; this file is process; `TODO.md` is unbuilt work
and closed measurements. One home per fact; the other links.

- **`docs/` ships with the tool.** Why-the-code lives beside the code; unbuilt
  decisions live in `TODO.md`.
- **An `@-reference` in `AGENTS.md` is a budget line.** `CLAUDE.md` is
  `@AGENTS.md`, so every `@path` loads in every session. Only
  `@CONTRIBUTING.md` stays: its test rules bind changes that would not look
  them up. Everything else is a markdown link.

Doc-linter exemptions: [`.doc-audit.json`](.doc-audit.json).
