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

## Tests

**Least tests, highest coverage. The suite stays under 30s** (ideally under
10). Each file holds one happy
path plus the edge cases that have actually bitten — no permutation laundry
lists.

There are two lanes, and which one a case belongs in is the first decision:

- **The unit lane is where the guarantee lives.** It runs under `-short`, and
  **a regression must fail here.** Anything only the end-to-end file proves is
  a gap, not coverage — measure it rather than assuming (see § Coverage).
  `internal/app/app_test.go` covers the whole command surface by calling
  `app.Run` directly with buffers, which is why `internal/app` exists outside
  `main` at all; `internal/cli/precedence_test.go` covers the six-rule
  argument table; other packages test their own internals beside them.
- **`rgit_e2e_test.go` is the slow lane.** It builds the binary and execs it,
  so `-short` skips the whole file. It earns its place by proving the assembled
  program behaves — argument precedence through a real process, hooks, the
  index, exit codes as a caller observes them — not by being the only thing
  that proves a behaviour at all.

The three files below, beside `main.go` in [`cmd/rgit`](cmd/rgit), remain the
home for the design's validated cases; a case one of them covers must not be
lost when it is refactored.

Temporary repositories come from [`internal/gittest`](internal/gittest/gittest.go)
rather than being hand-rolled per package — four packages had grown a
byte-identical copy of the same fifteen lines before it existed. It shells out
to the real git binary, like everything else here.

| File | Happy path | Critical edge cases |
| --- | --- | --- |
| `rgit_e2e_test.go` | Init repo → edit symbol → `rgit diff` → `rgit commit` → verify HEAD, clean index, hook ran, worktree preserved | Pre-staged sibling file comes along; hook rejection leaves staging intact (exit 128); resolve-all-before-stage leaves index untouched on failure; positional pathspec parity with `--file`; path escape and malformed `--sym` rejection (exit 129); exit 11 when every named target is unchanged; invocation from a subdirectory resolves paths relative to it; unborn branch lists everything committable |
| `index_test.go` | Single-symbol blob synthesis staged into the real index | Initial commit on an unborn branch, and its gitignore refusal (exit 7); no-newline-at-EOF preserved, and an appended symbol inheriting HEAD's EOF newline; rename staged as two paths yields git's `R100`; pathspec glob and `:(exclude)` pass through; mode-only change surfaces as `MODE`; submodule and symlink staging; `.gitattributes` clean filter (the `--path` requirement) |
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

Tests run in parallel — every top-level case in the three files above calls
`t.Parallel()`. A case that needs `t.Setenv` or `t.Chdir` cannot, and must say
so: `internal/app`'s cases change directory, because `openRepo` resolves the
repository from the working directory. Everything else builds its own temp
repository and shares nothing.

```bash
go test ./...          # full suite, end-to-end cases included
go test -short ./...   # unit lane: skips the built binary and the live server
go test -race ./...    # the concurrency that matters: jsonrpc2, the spawn lock
```

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
  docs cost ~17k tokens a session to save a `Read` that most sessions never
  needed. Only `@CONTRIBUTING.md` keeps one, because its test and coverage rules
  bind changes that would not think to look them up. Everything else is a
  markdown link, which also renders properly for humans; add a new `@-ref` only
  by arguing the same way.

Lint before opening a PR that touches any `.md`:

```bash
python3 ~/.claude/skills/doc-audit/scripts/doc-audit.py
```
