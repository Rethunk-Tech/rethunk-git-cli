# TODO

Future work only — nothing here is done. Decisions already made, and the
measurements behind them, live in [`specs/design.md`](specs/design.md);
behaviour that ships lives in [`docs/`](docs/).

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are documented in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not listed here.

The residual queue is empty after index-as-existence, `--only`,
promisor-missing blobs, `--pathspec-from-file`, `--reuse-message` with
zero targets, and pwsh e2e completion pins. Deliberately not queued:
`rgit restore` (designed and held back —
[`specs/design.md`](specs/design.md#rgit-restore-filesymbol-an-accepted-design-deliberately-not-built)),
context staged/unstaged split, TOML taplo / SQL LSP cross-checks, Rust/C/C++/
Vue/Svelte grammars, SCSS/zsh, JSONC/JSON5, orphan-gopls handshake cleanup,
`--include`/`-a`, and any web/shadcn surface (this is a CLI;
`@shadcn/command` does not apply).
