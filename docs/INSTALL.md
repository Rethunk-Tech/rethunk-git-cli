# Install

## Prerequisites

- **Go 1.21+** with cgo enabled — the tree-sitter grammars are C.
- **git** on `PATH`. `rgit` shells out to it for everything git already does.
- Optionally, a **language server** per language you want cross-checked
  (see [Language servers](#language-servers)).

## Build

```bash
go build -ldflags="-s -w" -o rgit .
```

The binary is ~5.3 MB; three vendored tree-sitter grammars account for nearly
all of it.

Install it anywhere on `PATH`:

```bash
install -m 0755 rgit ~/.local/bin/rgit
```

## Language servers

`rgit` works without any language server — it falls back to tree-sitter alone
and prints `[ts-only]` on stderr. Installing one enables the extent
cross-check, which catches build-tag, macro, and type-level mismatches.

| Language | Server | Install |
| --- | --- | --- |
| Go | `gopls` | `go install golang.org/x/tools/gopls@latest` |
| TypeScript/JavaScript | `vtsls` | `npm i -g @vtsls/language-server` |
| Python | `pyright` | `npm i -g pyright` |

`rgit` starts a server as a background daemon on first use and reuses it
afterwards. The current invocation completes in `[ts-only]` mode rather than
blocking on a cold index; the next one gets the full cross-check.

## Environment variables

| Variable | Effect |
| --- | --- |
| `RGIT_LSP_SOCKET` | Path to an existing language-server socket. Checked before the default location. |
| `XDG_RUNTIME_DIR` | Where `rgit` creates `rgit-<server>.sock` and its spawn lock. Falls back to the system temp dir. |
| `GIT_TERMINAL_PROMPT` | Set to `0` automatically when stdin is not a terminal, so credential and GPG prompts fail fast instead of hanging. Set it yourself to override. |

Everything else is git's own configuration, honoured because `git commit` does
the committing — including `commit.cleanup`, `commit.gpgsign`, `core.hooksPath`,
and `.gitattributes` filters. `commit.template` is the one exception: it
prefills an editor, and `rgit` never opens one.

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

Any language-server daemons `rgit` started exit on their own idle timeout.
