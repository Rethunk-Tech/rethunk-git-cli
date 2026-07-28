# Install

## Prerequisites

- **Go 1.26+** with cgo enabled — the tree-sitter grammars are C.
- **git** on `PATH`. `rgit` shells out to it for everything git already does.
- Optionally, a **language server** per language you want cross-checked
  (see [Language servers](#language-servers)).

The Go floor is not chosen — it tracks whatever the dependencies declare, since
`rgit` keeps them at their latest releases. The `go.lsp.dev` modules set it
today; it rises whenever a dependency raises its own.

## Build

```bash
go build -ldflags="-s -w" -o rgit .
```

The binary is ~11 MB stripped; what accounts for that is recorded in
[`specs/design.md`](../specs/design.md#dependencies).

Install it anywhere on `PATH`:

```bash
install -m 0755 rgit ~/.local/bin/rgit
```

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

Only `gopls` has a listen mode, so Go is the only language with a reusable
daemon: `rgit` probes for one and starts it in the background if none answers.
That first invocation finishes in `[ts-only]` mode rather than blocking on a
cold index; later ones get the full cross-check. The other three have no listen
mode, so `rgit` spawns one over stdio per query and kills it on close — nothing
persists, and the cross-check is live on the first invocation. The transport survey behind this split is in
[`specs/design.md`](../specs/design.md#transport-support-per-server).

## Environment variables

| Variable | Effect |
| --- | --- |
| `RGIT_LSP_SOCKET` | Path to an existing `gopls` socket. Checked before the default location. No effect on the stdio servers. |
| `XDG_RUNTIME_DIR` | Where `rgit` creates `rgit-gopls.sock` and its spawn lock. Falls back to the system temp dir. |
| `GIT_TERMINAL_PROMPT` | Set to `0` automatically when stdin is not a terminal. Set it yourself to override. |

Everything else is git's own configuration, honoured because `git commit` does
the committing. Which settings that covers, and the one exception, is in
[`USAGE.md`](USAGE.md#behaviour-inherited-from-git).

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
