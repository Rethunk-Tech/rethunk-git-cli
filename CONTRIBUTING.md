# Contributing

Read [`AGENTS.md`](AGENTS.md) first — it holds the one invariant every change is
measured against, and the list of things that break silently.

## Before you change behaviour

`rgit` matches git rather than inventing semantics. If a change makes `rgit`
behave differently from `git add <pathspec> && git commit`, say so explicitly in
the PR and justify it — the reasoning already on record is in
[`specs/design.md`](specs/design.md).

Claims in that record are backed by measurement. If you contradict one, measure
it again and update the record — do not simply reword it.

## Commits

Conventional commits: `type(scope): subject`.

- Subject is imperative and under ~72 characters.
- Body explains **why**, not which files changed.
- One logical unit per commit.
- No AI attribution trailers.

## Changelog

[`CHANGELOG.md`](CHANGELOG.md) carries one entry per tagged release, newest
first, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) form.
Add to the unreleased entry in the same commit as the change that earns it —
a behaviour change, a new grammar, a new flag, an exit code. Refactors, test
work, and documentation edits do not earn one.

Entries say what changed and link to the reference that documents it. They do
not restate it: the tiered layout below is what keeps one authority per fact.

Cutting a release: tag `vX.Y.Z`, which
[`.github/workflows/release.yml`](.github/workflows/release.yml) turns into a
GitHub release with the cross-built and natively-built binaries and their
`SHA256SUMS`. That workflow also fails the release outright if any
artifact's own `rgit languages` output has no `sql` row — a `-tags
rgit_sql` regression on any artifact this actually reaches: linux/amd64
and windows/amd64 execute directly (windows via Wine); linux/arm64 runs
inside a matching arm64 container image under QEMU emulation, since it is
dynamically linked against glibc and bare QEMU has no aarch64 sysroot to
resolve that against; whichever darwin arch matches the `macos-latest`
runner's own (arm64, as of this writing) executes directly in its own
job — the other darwin artifact is checked by file type only, not
executed. See [`docs/INSTALL.md`](docs/INSTALL.md#cross-builds).
Move the unreleased entries under the new version heading, and bump the
README's version badge — it is a static shield, so nothing else catches it
going stale. Also check `sqlGrammarVersion` in
[`cmd/rgit-install/main.go`](cmd/rgit-install/main.go) against
[`tree-sitter-sql`](https://github.com/DerekStride/tree-sitter-sql)'s own
tags: `@latest` cannot track it, because that module gitignores `parser.c` at
every tag ([`internal/resolve/sqlgrammar/grammar.go`](internal/resolve/sqlgrammar/grammar.go)),
so this pin only ever moves by hand and nothing else reminds you to look. If
a newer tag exists, bump the constant, then `make sql-parser` and `go test
-tags rgit_sql ./...` to confirm the new grammar still generates, compiles,
and resolves — update any resolver fixtures whose anchor extents shifted —
before tagging.

## Tests

**Least tests, highest coverage. The suite stays under 30s** (ideally under
10). Each file holds one happy
path plus the edge cases that have actually bitten — no permutation laundry
lists.

The two test lanes below, plus `golangci-lint`, run in
[`.github/workflows/ci.yml`](.github/workflows/ci.yml) on every push and pull
request — that much is enforced, not just documented. The ≤30s suite-time
budget and the coverage numbers in [§ Coverage](#coverage) are not: CI runs
no timing check and no coverage step, so both stay a review discipline
rather than a CI gate.

There are two lanes, and which one a case belongs in is the first decision:

- **The unit lane is where the guarantee lives.** It runs under `-short`, and
  **a regression must fail here.** Anything only the end-to-end file proves is
  a gap, not coverage — measure it rather than assuming (see § Coverage).
  `internal/app/app_test.go` covers the whole command surface by calling
  `app.Run` directly with buffers, which is why `internal/app` exists outside
  `main` at all; `internal/app/lanes_test.go` holds the same kind of
  `app.Run`-driven case for guarantees that also need a real hook, a real
  index, or forwarded git flags — split into its own file so a concurrent
  editor of `app_test.go` never collides with it; `internal/cli/precedence_test.go`
  covers the six-rule argument table; other packages test their own
  internals beside them. `index_test.go` (below) belongs to this lane too,
  despite living in `cmd/rgit`: its cases drive `synth.Stage` directly and
  never touch the built binary, so `-short` does not skip them.
- **`rgit_e2e_test.go` is the slow lane.** It builds the binary and execs it,
  so `-short` skips the whole file. It earns its place by proving the assembled
  program behaves — argument precedence through a real process, hooks, the
  index, exit codes as a caller observes them — not by being the only thing
  that proves a behaviour at all.

The three files below, beside `main.go` in [`cmd/rgit`](cmd/rgit), are the
home for the design's validated cases; a case one of them covers must not be
lost when it is refactored.

Temporary repositories come from [`internal/gittest`](internal/gittest/gittest.go)
rather than being hand-rolled per package. It shells out to the real git
binary, like everything else here.

| File | Happy path | Critical edge cases |
| --- | --- | --- |
| `rgit_e2e_test.go` | Init repo → edit symbol → `rgit diff` → `rgit commit` → verify HEAD, clean index, hook ran, worktree preserved | Hook rejection leaves staging intact (exit 128, also pinned at the unit level: a real hook is process-level enough to earn both); `--` and leading-colon pathspec magic reaching real `git add`, not just classification; positional pathspec parity with `--file`; path escape and malformed `--sym` rejection (exit 129); invocation from a subdirectory resolves paths relative to it; unborn branch lists everything committable; `--fixup`'s own message-appending with `-m`; `--gpg-sign`/`--no-gpg-sign` and `--push` reaching real git; shell completion driving real bash and zsh; exit codes and help text as an external process observes them |
| `index_test.go` | Single-symbol blob synthesis staged into the real index | Initial commit on an unborn branch, and its gitignore refusal (exit 7); no-newline-at-EOF preserved, and an appended symbol inheriting HEAD's EOF newline; rename staged as two paths yields git's `R100`; pathspec glob and `:(exclude)` pass through; mode-only change surfaces as `MODE`; submodule and symlink staging; `.gitattributes` clean filter (the `--path` requirement); overlapping/nested anchors coalesce into one extent; a new file's `@header`/`@imports` preamble stages automatically; class/container members (TypeScript, Python) stage without their sibling, and a Go receiver method stays a sibling never escalated into; a member deletion keeps the file parseable across a class's own indentation; YAML nested-key edits round-trip byte-identical; resolve-all-before-staging-any leaves the index untouched on a partial failure; a new symbol's nearest-sibling insertion walks past other new, unstaged siblings; multiple symbols splice in reverse byte-offset order; a container member insert is byte-identical to the worktree, no blank line invented (Python's own PEP 8 line is the deliberate exception); `--dry-run`'s preamble rows agree with git's real numstat |
| `resolver_test.go` | Tree-sitter extent + doc-comment attribution on a Go fixture | Exit 3 (unresolvable) and its did-you-mean candidates; new-symbol insertion when neighbours are also new; `@header`, `@imports`, and `@toplevel` extents, including imports spanning interior comments; live `gopls` cross-check |

Units run against in-memory tree-sitter. Prefer the real dependency over a
double wherever one is reachable: a language server that is installed gets
dialled for real, and the live-`gopls` check skips cleanly only when the binary
is absent or `-short` is set. A double encodes what its author believed the
dependency did and then stops tracking it — real language-server range
semantics can diverge from what a stand-in assumes, and that gap is exactly
what a double cannot catch. Reach for one only where the real thing is
unreachable, and say at the seam what would catch its drift.

**Write tests before implementation.**

Tests run in parallel — every top-level test case in this repo calls
`t.Parallel()`. A case that needs `t.Setenv` or `t.Chdir` cannot, and must say
so: `internal/app`'s cases change directory, because `openRepo` resolves the
repository from the working directory. Everything else builds its own temp
repository, most often via `internal/gittest`, and shares nothing with any
other case, which is what makes `t.Parallel()` safe to add without auditing
the whole suite for shared state each time.

```bash
go test ./...          # full suite, end-to-end cases included
go test -short ./...   # unit lane: skips the built binary and the live server
go test -race ./...    # the concurrency that matters: jsonrpc2, the spawn lock
```

CI's own race job scopes to `./internal/lsp/...` rather than `./...` —
jsonrpc2 and the spawn lock live there, and racing every other package on
every push was not worth the extra minutes. Run `-race ./...` locally
before touching concurrent code anywhere else in the tree.

### Coverage

Always pass `-coverpkg=./...`: much of this suite drives code from another
package, so without it `internal/app`, `internal/cli`, `internal/resolve` and
`internal/synth` report 0.0% while being covered heavily in fact.

```bash
go test -coverpkg=./... -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -1
```

The number that matters is the **`-short` one**, since that is the lane a
regression has to fail in. Compare the two; a package that drops sharply
without the binary is one the unit lane does not really cover:

```bash
go test -short -coverpkg=./... -coverprofile=short.out ./...
go tool cover -func=short.out | tail -1
```

**That comparison is in-process only — it cannot see the built binary's own
coverage.** `rgit_e2e_test.go` builds `rgitBin` with `-cover` and points
`GOCOVERDIR` at a temp directory for the whole process (`TestMain`), but
removes that directory unmerged once the run finishes. Every case in this
file execs the real binary, and the coverage that binary itself accumulated
is discarded before either `coverprofile` above is written — so the full
lane's number is exactly the in-process short-lane number plus whatever the
in-process parts of the full lane alone added, never the binary's own
exec'd paths. The full and `-short` totals reading identical is this
artifact, not evidence the unit lane already covers everything the binary
exercises. To actually see the binary's own coverage, merge `GOCOVERDIR`
into a profile before it is removed — `go tool covdata textfmt
-i=<GOCOVERDIR> -o=e2e.out`, comparable to the two profiles above with
`go tool cover -func=e2e.out | tail -1`.

### Benchmarks

`internal/diff/bench_test.go`'s `BenchmarkAttribution_200MemberClass` is a
regression gate, not a comparison: `specs/design.md` § Blob synthesis
measured a ~39× difference between re-parsing per declaration and holding
one parse open per side on a 200-member class, and the held-open path is
the only one that ships (`attributeSymbolsOpen`), so there is nothing left
to re-parse-and-compare against in-process. Run it and compare `ns/op`
against a prior run's own number:

```bash
go test -bench=Attribution -benchmem ./internal/diff/
```

cgo + tree-sitter makes absolute time machine-noisy — judge by ratio
against a checked-in or previously recorded baseline, not an absolute
threshold. Not run in CI: benchmarks are noisy on shared runners and a
flaky gate is worse than no gate; run it by hand before and after a change
to `internal/diff/attribute.go` or `internal/resolve`'s parse-caching path.

## Modernization

Go 1.26's `go fix` is an analysis-driven modernizer, not the old import
rewriter. Run it before opening a PR and commit what it changes:

```bash
go fix -diff ./...   # preview
go fix ./...         # apply, then run it again — fixes can unlock fixes
```

It only applies a fix where the `go` directive in `go.mod` (or a file's own
`//go:build` constraint) already permits the construct, so it cannot
introduce something this module's Go version does not have. Read what it
produces rather than committing it blind: the rewrites are correct but
occasionally clumsy, and a mechanical `a, b := before, after` is worth
collapsing by hand.

## Linting

```bash
make lint   # golangci-lint run ./...
```

[`.golangci.yml`](.golangci.yml) keeps the default linter set (errcheck,
govet, ineffassign, staticcheck, unused) with one deliberate tuning: an
errcheck exemption for the `fmt.Fprint*` calls that report an outcome to the
caller's own stdout/stderr, where a second failure has nowhere to go. The
reasoning is recorded there rather than repeated here.

## Dependencies

Binary size and dependency count are not constraints — but every dependency
needs a specific, measured justification, recorded in
[`specs/design.md`](specs/design.md#dependencies). "It is the standard choice
for this category" is not one.

Anything that reimplements what git already does is rejected on principle; see
the delegation boundary in [`AGENTS.md`](AGENTS.md#delegation-boundary).

## Documentation

This repo follows the tiered doc layout: README orients and links, `HUMANS.md`
introduces running and using `rgit`, `docs/` holds the authoritative reference
it points at, `AGENTS.md` holds internals, `CONTRIBUTING.md` holds process, and
`specs/` holds the design record.

Do not repeat content between tiers. If something belongs in two places, it
belongs in one and gets linked from the other.

Two repo conventions that are easy to undo by accident:

- **`docs/` is only for documentation shipped with the tool.** Design record and
  migration material go in `specs/`.
- **An `@-reference` in `AGENTS.md` is a budget line, not a link.** `CLAUDE.md`
  symlinks to `AGENTS.md`, so every `@path` there is pulled into *every* agent
  session whether or not the change touches that file — @-referencing all eight
  docs costs ~17k tokens a session to save a `Read` most sessions never need.
  Only `@CONTRIBUTING.md` keeps one, because its test and coverage rules bind
  changes that would not think to look them up. Everything else is a markdown
  link, which also renders properly for humans; add a new `@-ref` only by
  arguing the same way.

Maintainers additionally run a doc linter over any `.md` change; its
per-repo exemptions live in [`.doc-audit.json`](.doc-audit.json), which
records why each one is granted.
