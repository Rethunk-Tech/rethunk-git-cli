# Install

## Prerequisites

- **Go 1.27+** with cgo — the grammars are C.
- **git 2.32+** on `PATH` (`git commit --trailer`). `rgit doctor` reports
  the resolved version; it does not refuse below the floor.
- Optionally a **language server** per language ([Language servers](#language-servers)).
- Optionally the **tree-sitter CLI** for `.sql` ([SQL support](#sql-support)).

The Go floor tracks whatever the dependencies declare.

## Build

**`make install`** wraps [`cmd/rgit-install`](../cmd/rgit-install):

```bash
make install                     # $GOBIN, or $(go env GOPATH)/bin
make install PREFIX=~/.local/bin
go run ./cmd/rgit-install -dry-run
go run ./cmd/rgit-install -prefix ~/.local/bin
```

**`make build`** writes `./rgit` for the host.

**Plain `go build`:**

```bash
go build -ldflags="-s -w" -o rgit ./cmd/rgit
dest="$(go env GOBIN)"; [ -n "$dest" ] || dest="$(go env GOPATH)/bin"
install -m 0755 rgit "$dest/rgit"
```

`go env GOBIN` prints empty and exits 0 when unset — test the value, not
the status. Stripped binary is ~19.5 MB; the grammars are most of it.

## Makefile targets

`make help` lists them.

| Target | Does |
| --- | --- |
| `build` | Host binary to `./rgit` |
| `install` | `cmd/rgit-install` (`PREFIX=` to override) |
| `test`, `test-short`, `test-race` | Full suite, `-short`, raced. Lanes: [`CONTRIBUTING.md`](../CONTRIBUTING.md#tests) |
| `cover`, `cover-short` | `-coverpkg=./...` |
| `fix-diff`, `fix` | `go fix` preview / apply |
| `cross` | linux/amd64, linux/arm64, windows/amd64 into `dist/`, SQL when the CLI is present |
| `clean` | Remove build outputs |

## Cross builds

cgo is required. From Linux with [zig](https://ziglang.org):

| Target | Works | `CC` |
| --- | --- | --- |
| linux/amd64 | yes | `zig cc -target x86_64-linux-gnu` |
| linux/arm64 | yes | `zig cc -target aarch64-linux-gnu` |
| windows/amd64 | yes | `zig cc -target x86_64-windows-gnu` |
| darwin/* | **no via zig** | needs a macOS SDK |

```bash
make cross
make cross-linux-arm64
```

darwin fails linking `net` (`-lresolv`, CoreFoundation). `-tags
netgo,osusergo` does not clear it. **No darwin artifacts.** On macOS, build
from source.

`make cross` generates the SQL parser once (`-generate-only`), then
`-tags rgit_sql` on every target. Without the tree-sitter CLI,
`-generate-only` is non-zero and no `dist/` binary is written. `make
install` still falls back to an SQL-less host binary. It overwrites
`dist/SHA256SUMS` for that run and includes `rgit-completions-man.tar.gz`
(bash, zsh, fish, PowerShell completions and `rgit.1`, generated from
`rgit completion` / `rgit man`).

## SQL support

SQL is `-tags rgit_sql`. The grammar module gitignores `parser.c`;
`tree-sitter generate` writes it into the adapter's gitignored `csrc/`.
The CLI evaluates `grammar.js` with its own JS engine — no Node.js. Without
the CLI, the installer installs without SQL and says so; `.sql` is then
exit 9. What `.sql` addresses: [`ANCHORS.md`](ANCHORS.md#language-support).

## Language servers

No server → `[ts-only]` on stderr. A server enables the extent cross-check.

| Language | Server | Install | How `rgit` runs it |
| --- | --- | --- | --- |
| Go | `gopls` | `go install golang.org/x/tools/gopls@v0.23.0` | Background daemon, reused |
| TypeScript/JavaScript | `vtsls` | `npm i -g @vtsls/language-server` | One-shot per query |
| Python | `pyright-langserver` | `npm i -g pyright` | One-shot per query |
| Rust | `rust-analyzer` | `rustup component add rust-analyzer` | One-shot per query |
| Shell, Zsh | `bash-language-server` | `npm i -g bash-language-server` | One-shot per query |
| YAML | `yaml-language-server` | `npm i -g yaml-language-server` | One-shot per query |
| JSON, JSONC | `vscode-json-language-server` | `npm i -g vscode-langservers-extracted` | One-shot per query |
| CSS, SCSS | `vscode-css-language-server` | `npm i -g vscode-langservers-extracted` | One-shot per query |
| Markdown | `marksman` | [GitHub release](https://github.com/artempyanykh/marksman/releases) | One-shot per query |
| HTML | `vscode-html-language-server` | `npm i -g vscode-langservers-extracted` | One-shot per query |

JSON, CSS, and HTML share `vscode-langservers-extracted`.

Only `gopls` listens: first invocation is `[ts-only]` rather than blocking
on a cold index. Other servers are stdio. **TOML, SQL, JSON5 stay
`[ts-only]`** — [`LIMITATIONS.md`](LIMITATIONS.md#language-server-coverage).

## Environment variables

| Variable | Effect |
| --- | --- |
| `RGIT_LSP_SOCKET` | Existing `gopls` socket. Checked first. No effect on stdio servers. |
| `RGIT_LSP_DIAL_TIMEOUT` | Dial wait before `[ts-only]` (default `150ms`). Go duration; unset/malformed/≤0 keep the default. |
| `RGIT_LSP_QUERY_TIMEOUT` | Cross-check deadline (default `2s`). Same fail-closed rules. |
| `XDG_RUNTIME_DIR` | Private `rgit-<uid>/` (socket, spawn lock, pidfile). Else system temp. |
| `TMPDIR` | `os.TempDir()` fallback when `XDG_RUNTIME_DIR` is unset (POSIX), then `/tmp`. |
| `GIT_TERMINAL_PROMPT` | Set to `0` when stdin is not a terminal. Override yourself. |

Git's own config is honoured because `git commit` commits. Exception:
[`USAGE.md`](USAGE.md#behaviour-inherited-from-git).

## Shell completion

What completes: [`USAGE.md`](USAGE.md#shell-completion).

```bash
source <(rgit completion bash)
source <(rgit completion zsh)    # after compinit
rgit completion fish | source
rgit completion pwsh | Invoke-Expression
```

```bash
rgit completion bash > /etc/bash_completion.d/rgit
rgit completion bash > ~/.local/share/bash-completion/completions/rgit
rgit completion zsh > "$fpath[1]/_rgit"
rgit completion fish > ~/.config/fish/completions/rgit.fish
Add-Content -Path $PROFILE -Value 'Invoke-Expression (rgit completion pwsh | Out-String)'
```

zsh needs `compinit` first.

Releases also attach `rgit-completions-man.tar.gz` (in `SHA256SUMS`).
Install paths: [HUMANS.md](../HUMANS.md).

## Install script

Machine with only git:

```bash
curl -fsSL https://raw.githubusercontent.com/Rethunk-Tech/rethunk-git-cli/main/scripts/install.sh | sh
```

Windows/amd64:

```powershell
$env:VERSION = 'v2.0.0'  # optional; defaults to latest
.\scripts\install.ps1
# $HOME\.local\bin; $env:PREFIX overrides
$env:VERSION = 'v2.0.0'
.\scripts\install.ps1 -DryRun   # requires VERSION; no network
```

Releases also publish `SHA256SUMS.sigstore.json`:

```bash
cosign verify-blob \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp 'https://github.com/Rethunk-Tech/rethunk-git-cli/.github/workflows/release.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
```

With `cosign` on `PATH`, both installers verify the bundle before the
binary SHA256. Without it, SHA256-only. Dry-run fetches nothing.

`PREFIX` (default `$HOME/.local/bin`) and `VERSION` (default `latest`) are
env vars: `VERSION=v2.0.0 PREFIX=/usr/local/bin sh install.sh`.

`install.sh`: linux/amd64 and linux/arm64; errors on macOS. `install.ps1`:
Windows/amd64. Neither installs a language server. No Homebrew formula
(cgo per-platform bottles).

## Verify

```bash
rgit --version
cd /some/git/repo && rgit diff
rgit diff --quiet   # 1 if committable, 0 if clean
rgit diff 2>&1 >/dev/null | grep -q 'ts-only' && echo "degraded" || echo "cross-check active"
```

A clean repo prints nothing and exits 0.

## Uninstall

```bash
rm "$(command -v rgit)"
rm -rf "${XDG_RUNTIME_DIR:-${TMPDIR:-/tmp}}/rgit-$(id -u)"
```

`command -v` finds the running copy. The subdirectory is `gopls` only. A
daemon `rgit` started exits on idle timeout; one that fails handshake is
stopped on Linux (SIGTERM, then SIGKILL) after the command line matches.
Lookup: `$XDG_RUNTIME_DIR`, then `os.TempDir()` (`$TMPDIR`, then `/tmp`).
Only `rgit-<uid>/` is created or trusted — [`AGENTS.md`](../AGENTS.md) § State.
