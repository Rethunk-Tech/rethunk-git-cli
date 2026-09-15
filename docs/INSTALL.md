# Install

## Prerequisites

- **Go 1.27+** with cgo enabled — the tree-sitter grammars are C.
- **git 2.32+** on `PATH` — the newest behaviour any code path relies on
  (`git commit --trailer`, `internal/gitx.go`). `rgit doctor` checks the
  resolved version, not just presence, and reports it as informational
  rather than refusing to run below the floor.
- Optionally, a **language server** per language you want cross-checked
  (see [Language servers](#language-servers)).
- Optionally, the **tree-sitter CLI** for `.sql` anchors (see
  [SQL support](#sql-support)) — everything else builds and works without it.

The Go floor is not chosen — it tracks whatever the dependencies declare,
since `rgit` keeps them at their latest releases, and rises when one of them
raises its own.

## Build

Three ways to get a binary, in order of how much they do for you:

**`make install`** wraps [`cmd/rgit-install`](../cmd/rgit-install): it checks
prerequisites, generates the SQL parser when it can (see
[SQL support](#sql-support)), builds, installs, and reports each step:

```bash
make install                     # to $GOBIN, or $(go env GOPATH)/bin
make install PREFIX=~/.local/bin # anywhere else
```

**The default target is `$GOBIN`, falling back to `$(go env GOPATH)/bin`** —
usually `~/go/bin`. The binary lands there unless you pass `PREFIX` (or
`-prefix`), and that is the path the uninstall step below assumes.

Run the installer directly for its own flags, including a no-op preview:

```bash
go run ./cmd/rgit-install -dry-run
go run ./cmd/rgit-install -prefix ~/.local/bin
```

**`make build`** builds `./rgit` for the host only, no install step.

**Plain `go build`** needs no `make`:

```bash
go build -ldflags="-s -w" -o rgit ./cmd/rgit
```

The binary is ~13.5 MB stripped (13740 KB), or ~16 MB with `-tags rgit_sql`
(16156 KB); the grammars account for nearly all of it — Shell adds ~1332 KB
and Markdown ~768 KB, and cgo links per object file, so a grammar's unused
inline copy cannot be dropped.

Without `make install`, put the binary on `PATH` yourself — anywhere on
`PATH` works; this matches where `make install` would have put it:

```bash
dest="$(go env GOBIN)"; [ -n "$dest" ] || dest="$(go env GOPATH)/bin"
install -m 0755 rgit "$dest/rgit"
```

`go env GOBIN` prints an empty line and exits 0 when it is unset, so the
fallback has to test the value rather than the exit status.

## Makefile targets

`make help` — the default target — lists them all; the ones worth knowing:

| Target | Does |
| --- | --- |
| `build` | `go build` the host binary to `./rgit` |
| `install` | Build and install via `cmd/rgit-install` (`PREFIX=` to override) |
| `test`, `test-short`, `test-race` | Three `go test` invocations: full suite, unit lane alone (`-short`), full suite raced. `test`/`test-short` are the two lanes [`CONTRIBUTING.md`](../CONTRIBUTING.md#tests) documents; `test-race` is a separate concern, not a third lane |
| `cover`, `cover-short` | Coverage with `-coverpkg=./...`, as `CONTRIBUTING.md` requires |
| `fix-diff`, `fix` | `go fix` preview and apply |
| `cross` | Cross-compile linux/amd64, linux/arm64, windows/amd64 into `dist/`, with SQL when the tree-sitter CLI is present |
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
| darwin/amd64, darwin/arm64 | **no, not via zig** | needs a macOS SDK — not built or published |

```bash
make cross                 # all three zig-cross-compiled targets, into dist/
make cross-linux-arm64     # a single target
```

darwin fails at link time through zig with `unable to find dynamic system
library 'resolv'`: `net` is a real dependency (`go.lsp.dev/jsonrpc2` uses it
for the `gopls` socket), and linking it needs `-lresolv` and `-framework
CoreFoundation` from an actual macOS SDK — building with `-tags
netgo,osusergo` does not clear it, which is why darwin is deliberately not
in `make cross`'s own zig-based matrix.

**No darwin binaries are published.** Every CI and release job runs on a
Linux runner, so tagged releases ship exactly the three zig-built artifacts
above, covered by `SHA256SUMS` and its cosign signature. On macOS, build
from source (§ Build).

**Cross binaries carry SQL when the build host can generate the parser.**
`make cross` runs generation once through `cmd/rgit-install -generate-only`,
then passes `-tags rgit_sql` to every target. Generation is
host-independent — it turns `grammar.js` into C a single time — so the only
per-target cost is compiling that C, measured at ~6–7s each with `zig cc`
(a full three-target `make cross` from a clean tree, generation included,
measures ~28s).

Without the tree-sitter CLI on the build host, nothing is generated and
every target builds SQL-less instead of failing — the same fallback
`make install` makes, with `.sql` anchors resolving as any other
unsupported language does (exit 9, `docs/ANCHORS.md`).

`make cross` also regenerates `dist/SHA256SUMS` from that run's own
artifacts, overwriting rather than appending, so the checksums on disk
always match the binaries currently in `dist/`.

## SQL support

SQL is a second grammar behind the `rgit_sql` build tag: a plain `go build
./...` or `go install ./cmd/rgit` builds and works identically without it.
Its parser has no pre-built Go bindings — the grammar module gitignores its
own `parser.c` at every tag — so `rgit` generates that file at build time
instead of vendoring it: `tree-sitter generate` turns the module's
`grammar.js` into a working `parser.c`, copied into the SQL adapter
package's `csrc/` subdirectory. That directory is gitignored and never
committed; it regenerates on demand.

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
| Go | `gopls` | `go install golang.org/x/tools/gopls@v0.23.0` | Background daemon, reused |
| TypeScript/JavaScript | `vtsls` | `npm i -g @vtsls/language-server` | One-shot subprocess per query |
| Python | `pyright-langserver` | `npm i -g pyright` | One-shot subprocess per query |
| Rust | `rust-analyzer` | `rustup component add rust-analyzer` | One-shot subprocess per query |
| Shell | `bash-language-server` | `npm i -g bash-language-server` | One-shot subprocess per query |
| YAML | `yaml-language-server` | `npm i -g yaml-language-server` | One-shot subprocess per query |
| JSON | `vscode-json-language-server` | `npm i -g vscode-langservers-extracted` | One-shot subprocess per query |
| CSS | `vscode-css-language-server` | `npm i -g vscode-langservers-extracted` | One-shot subprocess per query |
| Markdown | `marksman` | [GitHub release binary](https://github.com/artempyanykh/marksman/releases) — no package manager publishes it | One-shot subprocess per query |
| HTML | `vscode-html-language-server` | `npm i -g vscode-langservers-extracted` | One-shot subprocess per query |

JSON, CSS, and HTML share one npm package: a single
`vscode-langservers-extracted` install produces all three binaries.

Only `gopls` has a listen mode, so Go is the only language with a reusable
daemon: `rgit` probes for one and starts it in the background if none answers.
That first invocation finishes in `[ts-only]` mode rather than blocking on a
cold index; later ones get the full cross-check. Every other server is
stdio-only, so `rgit` spawns one per query and kills it on close — nothing
persists, and the cross-check is live on the first invocation. The split is
the servers' own: `vtsls` and `pyright` dial out on `--socket=<port>`, while
`bash-language-server` has no transport flag at all.

**TOML and SQL stay `[ts-only]` permanently** — see
[`LIMITATIONS.md`](LIMITATIONS.md#language-server-coverage) for why. `rgit`
never dials a server for either, so neither has a row in the table above.
HTML is wired; void elements still need a
`declOnlyEndTrimmer` seam (same section), not a class-suffix mismatch.

## Environment variables

| Variable | Effect |
| --- | --- |
| `RGIT_LSP_SOCKET` | Path to an existing `gopls` socket. Checked before the default location. No effect on the stdio servers. |
| `RGIT_LSP_DIAL_TIMEOUT` | Overrides how long `rgit` waits to reach a live language-server daemon before falling back to `[ts-only]` (default `150ms`). A Go duration string (e.g. `500ms`, `1s`); unset, malformed, zero, or negative values keep the default. |
| `RGIT_LSP_QUERY_TIMEOUT` | Overrides the deadline for a single cross-check round trip once connected (default `2s`). Same duration-string and fail-closed rules as above. |
| `XDG_RUNTIME_DIR` | Where `rgit` creates its private `rgit-<uid>/` subdirectory, holding `rgit-gopls.sock` and its spawn lock. Falls back to the system temp dir. |
| `TMPDIR` | Consulted by the system-temp-dir fallback above when `XDG_RUNTIME_DIR` is unset (Go's own `os.TempDir()`, POSIX only), before it falls back further to `/tmp`. See § Uninstall for the exact lookup order. |
| `GIT_TERMINAL_PROMPT` | Set to `0` automatically when stdin is not a terminal. Set it yourself to override. |

Everything else is git's own configuration, honoured because `git commit` does
the committing. Which settings that covers, and the one exception, is in
[`USAGE.md`](USAGE.md#behaviour-inherited-from-git).

## Shell completion

What `rgit completion bash|zsh|fish|pwsh` completes, and how the dynamic part
works, is documented in [`USAGE.md`](USAGE.md#shell-completion).

**Load once per session:**

```bash
source <(rgit completion bash)   # bash
source <(rgit completion zsh)    # zsh, after compinit has run
rgit completion fish | source    # fish
rgit completion pwsh | Invoke-Expression  # PowerShell 7
```

**Persist across sessions:**

```bash
# bash
rgit completion bash > /etc/bash_completion.d/rgit                       # system-wide
rgit completion bash > ~/.local/share/bash-completion/completions/rgit   # per-user

# zsh -- write it anywhere already on $fpath, then start a new shell
rgit completion zsh > "$fpath[1]/_rgit"

# fish -- picked up automatically by every new fish session, no reload step
rgit completion fish > ~/.config/fish/completions/rgit.fish

# PowerShell 7 -- add this line to $PROFILE
Add-Content -Path $PROFILE -Value 'Invoke-Expression (rgit completion pwsh | Out-String)'
```

zsh's `compdef` needs `compinit` to already have run, so `autoload -Uz
compinit && compinit` must come first in `.zshrc` — the standard
precondition for any zsh completion, not one of `rgit`'s own.

## Install script

For a machine with only git — no Go toolchain, no zig — `scripts/install.sh`
downloads a release binary and verifies it against that release's own
`SHA256SUMS` before installing it:

```bash
curl -fsSL https://raw.githubusercontent.com/Rethunk-Tech/rethunk-git-cli/main/scripts/install.sh | sh
```

On Windows/amd64, `scripts/install.ps1` is the equivalent release-download
and checksum-verification path:

```powershell
$env:VERSION = 'v2.0.0'  # optional on a real install; defaults to latest
.\scripts\install.ps1
```

It installs `rgit.exe` to `$HOME\.local\bin` by default; set `$env:PREFIX` to
override. `-DryRun` prints the plan — download URL, checksum source, install
path, plus the planned signature source when `cosign` is available — without
touching the network, and requires an explicit `VERSION` tag; there is no
latest-tag lookup on that path:

```powershell
$env:VERSION = 'v2.0.0'
.\scripts\install.ps1 -DryRun
```

Every release also publishes `SHA256SUMS.sigstore.json`, a keyless
[cosign](https://docs.sigstore.dev/cosign/signing/overview/) signature over
`SHA256SUMS` itself -- verifying it proves the checksums came from this
repo's own release workflow, not just that a downloaded file matches *some*
checksum file:

```bash
cosign verify-blob \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp 'https://github.com/Rethunk-Tech/rethunk-git-cli/.github/workflows/release.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
```

When `cosign` is on `PATH`, `scripts/install.sh` and `scripts/install.ps1`
do this automatically: they download `SHA256SUMS.sigstore.json` beside
`SHA256SUMS` and verify the checksum file before checking the binary's
SHA256, and a failed verification stops the install. Without `cosign`, both
keep their SHA256-only paths. `--dry-run` and `-DryRun` exit before any
download, so neither fetches the signature bundle.

`PREFIX` (default `$HOME/.local/bin`) and `VERSION` (default `latest`) are
environment variables, not flags — `VERSION=v2.0.0 PREFIX=/usr/local/bin sh
install.sh` installs that exact tag system-wide. `--dry-run` prints the same
plan without touching the network at all, which is what CI runs to lint the
script's own control flow on every push.

**Scope is deliberately narrow.** `scripts/install.sh` supports
linux/amd64 and linux/arm64 and exits with an error on macOS, where no
release binary exists; `install.ps1` supports Windows/amd64. Linux verifies
the release line with `sha256sum`. Neither installer installs a language
server; both point at this documentation instead. There is no Homebrew
formula: `rgit` links tree-sitter through cgo, so a formula would need to
build from source per-platform (a bottle per target) rather than fetch one,
a materially bigger undertaking than this script covers.

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
rm "$(command -v rgit)"
rm -rf "${XDG_RUNTIME_DIR:-${TMPDIR:-/tmp}}/rgit-$(id -u)"
```

`command -v rgit` resolves whichever copy your shell actually runs — hardcoding
a path guesses wrong for anyone who installed to the default
`$GOBIN`/`$(go env GOPATH)/bin` rather than passing `PREFIX`.

That subdirectory exists only for `gopls`; the stdio servers leave nothing
behind, and a `gopls` daemon `rgit` started exits on its own idle timeout. The
lookup order above mirrors `rgit`'s own (`internal/lsp/dial.go`'s
`runtimeDir`): `$XDG_RUNTIME_DIR` first, then Go's `os.TempDir()`, which on
POSIX honours `$TMPDIR` before falling back to `/tmp` — a host with `$TMPDIR`
set and no `$XDG_RUNTIME_DIR` puts the socket there, not in `/tmp`. `rgit`
only ever creates or trusts the `rgit-<uid>/` subdirectory itself, never the
base directory directly — see `AGENTS.md` § State.
