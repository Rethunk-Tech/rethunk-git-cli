# Install

## Prerequisites

- **Go 1.26+** with cgo enabled — the tree-sitter grammars are C.
- **git** on `PATH`. `rgit` shells out to it for everything git already does.
- Optionally, a **language server** per language you want cross-checked
  (see [Language servers](#language-servers)).
- Optionally, the **tree-sitter CLI** for `.sql` anchors (see
  [SQL support](#sql-support)) — everything else builds and works without it.

The Go floor is not chosen — it tracks whatever the dependencies declare, since
`rgit` keeps them at their latest releases, and rises whenever one of them
raises its own.

## Build

Three ways to get a binary, in order of how much they do for you:

**`make install`** builds and installs in one step, wrapping
[`cmd/rgit-install`](../cmd/rgit-install) — it checks prerequisites, generates
the SQL parser when it can (see [SQL support](#sql-support)), builds,
installs, and reports each step:

```bash
make install                     # to $GOBIN, or $(go env GOPATH)/bin
make install PREFIX=~/.local/bin
```

Run the installer directly for its own flags, including a preview that
changes nothing:

```bash
go run ./cmd/rgit-install -dry-run
go run ./cmd/rgit-install -prefix ~/.local/bin
```

It can also install or update the language servers `rgit`'s LSP cross-check
uses, opt-in via `-with-servers` — see
[Installing and updating servers automatically](#installing-and-updating-servers-automatically).

**`make build`** builds `./rgit` for the host only, no install step.

**Plain `go build`** needs no `make`:

```bash
go build -ldflags="-s -w" -o rgit ./cmd/rgit
```

The binary is ~11 MB stripped; what accounts for that is recorded in
[`specs/design.md`](../specs/design.md#dependencies).

Install any of the above onto `PATH` yourself if you didn't use `make install`:

```bash
install -m 0755 rgit ~/.local/bin/rgit
```

## Makefile targets

`make help` — the default target — lists them all; the ones worth knowing:

| Target | Does |
| --- | --- |
| `build` | `go build` the host binary to `./rgit` |
| `install` | Build and install via `cmd/rgit-install` (`PREFIX=` to override) |
| `test`, `test-short`, `test-race` | The three lanes [`CONTRIBUTING.md`](../CONTRIBUTING.md#tests) documents |
| `cover`, `cover-short` | Coverage with `-coverpkg=./...`, as `CONTRIBUTING.md` requires |
| `fix-diff`, `fix` | `go fix` preview and apply |
| `cross` | Cross-compile linux/amd64, linux/arm64, windows/amd64 into `dist/` |
| `clean` | Remove build outputs |

## Cross builds

`rgit` links tree-sitter through cgo, so `CGO_ENABLED=0` is not an option —
every cross target needs a matching C toolchain. Measured from a Linux host
with [zig](https://ziglang.org) as the single cross-compilation tool:

| Target | Works | `CC` |
| --- | --- | --- |
| linux/amd64 | yes | `zig cc -target x86_64-linux-gnu` |
| linux/arm64 | yes | `zig cc -target aarch64-linux-gnu` |
| windows/amd64 | yes | `zig cc -target x86_64-windows-gnu` |
| darwin/amd64, darwin/arm64 | **no** | needs a macOS SDK |

```bash
make cross                 # all three working targets, into dist/
make cross-linux-arm64     # a single target
```

darwin fails at link time with `unable to find dynamic system library
'resolv'`: `net` is a real dependency (`go.lsp.dev/jsonrpc2` uses it for the
`gopls` socket), and linking it needs `-lresolv` and `-framework
CoreFoundation` from an actual macOS SDK — building with `-tags
netgo,osusergo` does not clear it. Build darwin binaries on a Mac, or in CI
with a macOS runner; it is deliberately not in `make cross`'s default matrix.

**Cross binaries never carry SQL support.** `make cross` does not pass
`-tags rgit_sql` and does not generate the SQL parser, unlike `make install`
(see [SQL support](#sql-support) below). That is deliberate, not an
oversight: generation needs the tree-sitter CLI on the build host and a
~17 MB `parser.c` compile per target, which does not fit a cross matrix the
way it fits a single local `make install`. `.sql` anchors resolve as any
other unsupported language does (exit 9, `docs/ANCHORS.md`) in every `dist/`
binary; every other language is unaffected.

## SQL support

SQL is a second grammar behind the `rgit_sql` build tag: a plain `go build
./...` or `go install ./cmd/rgit` builds and works identically without it.
Its parser has no pre-built Go bindings — the grammar module gitignores its
own `parser.c` at every tag — so `rgit` generates that file at build time
instead of vendoring it: `tree-sitter generate` turns the module's
`grammar.js` into a working `parser.c`, which is copied into the SQL
adapter package's `csrc/` subdirectory. That directory is gitignored and
never committed; it regenerates on demand.

`cmd/rgit-install` does this automatically once the SQL adapter package
exists and the [tree-sitter CLI](https://github.com/tree-sitter/tree-sitter)
is on `PATH` — no separate Node.js install is needed even though `grammar.js`
is JavaScript: the CLI evaluates it with its own embedded JS engine, measured
directly by running `tree-sitter generate` with every `node`/`nodejs` binary
removed from `PATH`. Without the CLI, the installer installs `rgit` without
SQL support and says so plainly rather than failing; `.sql` anchors then
resolve as any other unsupported language does (exit 9, `docs/ANCHORS.md`),
and every other language is unaffected. What `.sql` addresses once built is
in [`ANCHORS.md`](ANCHORS.md#language-support).

## Language servers

`rgit` works without any language server — it falls back to tree-sitter alone
and prints `[ts-only]` on stderr. Installing one enables the extent
cross-check, which catches build-tag, macro, and type-level mismatches.

| Language | Server | Install | How `rgit` runs it |
| --- | --- | --- | --- |
| Go | `gopls` | `go install golang.org/x/tools/gopls@latest` | Background daemon, reused |
| TypeScript/JavaScript | `vtsls` | `npm i -g @vtsls/language-server` | One-shot subprocess per query |
| Python | `pyright-langserver` | `npm i -g pyright` | One-shot subprocess per query |
| Shell | `bash-language-server` | `npm i -g bash-language-server` | One-shot subprocess per query |
| YAML | `yaml-language-server` | `npm i -g yaml-language-server` | One-shot subprocess per query |
| JSON | `vscode-json-language-server` | `npm i -g vscode-langservers-extracted` | One-shot subprocess per query |
| CSS | `vscode-css-language-server` | `npm i -g vscode-langservers-extracted` | One-shot subprocess per query |
| Markdown | `marksman` | [GitHub release binary](https://github.com/artempyanykh/marksman/releases) — no package manager publishes it | One-shot subprocess per query |

JSON and CSS share one npm package, `vscode-langservers-extracted` — a single
install produces both binaries. `marksman` is the one server in this table
[`-with-servers`](#installing-and-updating-servers-automatically) does not
manage, for the same reason: nothing to shell out to.

Only `gopls` has a listen mode, so Go is the only language with a reusable
daemon: `rgit` probes for one and starts it in the background if none answers.
That first invocation finishes in `[ts-only]` mode rather than blocking on a
cold index; later ones get the full cross-check. Every other language's
server has no listen mode, so `rgit` spawns one over stdio per query and
kills it on close — nothing persists, and the cross-check is live on the
first invocation. The transport survey behind this split is in
[`specs/design.md`](../specs/design.md#transport-support-per-server).

**TOML and SQL stay `[ts-only]` permanently** — see
[`LIMITATIONS.md`](LIMITATIONS.md#language-server-coverage) for why. `taplo`
still completes the LSP handshake once built with `-with-servers`'
`--features lsp`, and other tooling can use it; it just never drives
`rgit`'s own cross-check.

### Installing and updating servers automatically

```bash
go run ./cmd/rgit-install -with-servers            # alongside a normal install
go run ./cmd/rgit-install -dry-run -with-servers   # preview only -- nothing runs
```

`-with-servers` is opt-in and off by default: a plain `rgit-install` never
touches anything beyond this repo's own build, exactly as before. Passed, it
additionally installs or updates every server it knows how to manage, by
shelling out to that ecosystem's own package manager rather than fetching
release binaries itself:

| Server | Manager | Command |
| --- | --- | --- |
| `gopls` | go | `go install golang.org/x/tools/gopls@latest` |
| `vtsls` | npm/bun | `npm install -g @vtsls/language-server` (`bun add -g` when bun is on `PATH`) |
| `pyright-langserver` | npm/bun | `npm install -g pyright` |
| `bash-language-server` | npm/bun | `npm install -g bash-language-server` |
| `yaml-language-server` | npm/bun | `npm install -g yaml-language-server` |
| `vscode-json-language-server`, `vscode-css-language-server` | npm/bun | `npm install -g vscode-langservers-extracted` (one package, both binaries) |
| `taplo` | cargo | `cargo install taplo-cli --locked --features lsp` |

`yaml-language-server`, `vscode-json-language-server`, and
`vscode-css-language-server` now match the table above — YAML, JSON, and CSS
are wired the same as the original four. `taplo` is the one row here that
`rgit` never dials for its own cross-check: TOML stays `[ts-only]`
permanently, on measured range disagreement rather than availability (see
[Language servers](#language-servers) above) — `-with-servers` still manages
`taplo` since other tooling can use it, but installing it will not turn on a
TOML cross-check.

**`taplo` needs the non-default `--locked --features lsp` explicitly.** A
bare `cargo install taplo-cli` and npm's `@taplo/cli` package both build a
`taplo` that answers on `PATH` while speaking no LSP at all — presence
without capability. `-with-servers` checks for taplo's own `lsp` subcommand,
not just that the binary exists, and reports which is missing when it isn't
there.

The same invocation both installs a missing server and updates a present
one: every manager above already resolves to the latest available version
and reinstalls only when something actually changed (cargo additionally
reinstalls when the requested `--features` differ from what's already
built), so running `-with-servers` again is always safe.

**`marksman` is deliberately out of scope.** No package manager publishes
it — only GitHub release binaries, platform-named per target. Teaching this
installer HTTP fetching and checksum verification for one server was judged
not worth it; `-with-servers` reports marksman as unmanaged and prints the
release URL instead.

**A server installed to a directory that isn't on `PATH` is still
invisible.** `cargo install` in particular writes to `$CARGO_HOME/bin`
(`~/.cargo/bin` by default), which is not on every system's `PATH`.
`-with-servers` checks every manager's bin directory against `PATH` after
each install and warns loudly, by name and directory, rather than reporting
success and leaving the binary unreachable.

## Environment variables

| Variable | Effect |
| --- | --- |
| `RGIT_LSP_SOCKET` | Path to an existing `gopls` socket. Checked before the default location. No effect on the stdio servers. |
| `XDG_RUNTIME_DIR` | Where `rgit` creates `rgit-gopls.sock` and its spawn lock. Falls back to the system temp dir. |
| `GIT_TERMINAL_PROMPT` | Set to `0` automatically when stdin is not a terminal. Set it yourself to override. |

Everything else is git's own configuration, honoured because `git commit` does
the committing. Which settings that covers, and the one exception, is in
[`USAGE.md`](USAGE.md#behaviour-inherited-from-git).

## Shell completion

What `rgit completion bash|zsh` completes, and how the dynamic part works, is
documented in [`USAGE.md`](USAGE.md#shell-completion).

**Load once per session:**

```bash
source <(rgit completion bash)   # bash
source <(rgit completion zsh)    # zsh, after compinit has run
```

**Persist across sessions:**

```bash
# bash
rgit completion bash > /etc/bash_completion.d/rgit                       # system-wide
rgit completion bash > ~/.local/share/bash-completion/completions/rgit   # per-user

# zsh -- write it anywhere already on $fpath, then start a new shell
rgit completion zsh > "$fpath[1]/_rgit"
```

zsh's `compdef` needs `compinit` to already have run, so `autoload -Uz
compinit && compinit` must come first in `.zshrc` — the standard
precondition for any zsh completion, not one of `rgit`'s own.

## Verify

```bash
rgit --version
cd /some/git/repo && rgit diff
```

A repo with no uncommitted changes prints nothing and exits 0. `rgit diff
--quiet` exits 1 when anything is committable, 0 when clean — the scriptable
form of the same check.

To confirm the cross-check is active rather than degraded, look for the absence
of `[ts-only]` on stderr:

```bash
rgit diff 2>&1 >/dev/null | grep -q 'ts-only' && echo "degraded" || echo "cross-check active"
```

## Uninstall

```bash
rm ~/.local/bin/rgit
rm -f "${XDG_RUNTIME_DIR:-/tmp}"/rgit-*.sock "${XDG_RUNTIME_DIR:-/tmp}"/rgit-*.lock
```

Those two files exist only for `gopls`; the stdio servers leave nothing behind.
A `gopls` daemon `rgit` started exits on its own idle timeout.
