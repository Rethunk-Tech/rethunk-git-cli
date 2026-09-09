# TODO

Future work, and the decisions that closed the door on work not being done.
Behaviour that ships is documented in [`docs/`](docs/); how the code works is
the code. What lives here is only what neither of those can hold: a design
accepted but not built, and a measurement that already settled a question.

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not here.

## Accepted, not built

### `rgit restore FILE:SYMBOL`

Restore one symbol's bytes from a revision into the worktree.

- **Mechanism.** `git show REV:FILE` (default `HEAD`, `--source REV`), resolve
  the same anchor in both blobs, splice the source extent over the worktree
  copy through `internal/synth` in reverse. A new caller only — no new
  resolution or synthesis machinery.
- **Backup contract.** Before writing, emit the displaced worktree extent as a
  `git apply`-compatible unified diff (real path header, ±3 lines of context):
  a record in the stream under `--porcelain`, a fenced block on stderr
  otherwise. Never written to disk — `rgit` keeps no persistent state. Undo is
  `git apply`. `git stash` was rejected: pathspec-granular, and it mutates
  stash state.
- **Guardrails.** Resolve in both revisions before writing; a symbol absent at
  the source is an error, not a deletion; the backup is emitted before any byte
  is written; a failed splice leaves the file untouched.
- **Why held.** It would be the only command that rewrites worktree bytes.
  Holding it keeps the surface non-destructive by design.

### Orphan-gopls handshake cleanup

A managed socket whose handshake failed is unlinked
(`internal/lsp/dial.go`). A live-but-stuck `gopls` behind it is left to its own
`-listen.timeout=10m`. A shutdown RPC or kill-by-pid needs a PID `rgit` never
discovers and no server exposes, so neither is in scope.

### Not queued

`rgit context` staged/unstaged split — it would be a second `diffpkg.Run` for a
bit `STATUS` never carried. SCSS, zsh, JSONC, JSON5. Include-style pathspec
flags. Any web surface: this is a CLI.

## Settled by measurement — do not re-litigate

### Grammars

**Named nested declarations** (a function declared inside another) are not
addressable: 0.00% of Go declarations (structurally impossible), 1.98% of
TypeScript functions, 3.36% TSX, 9.26% Python — and repository-concentrated,
55%/60% of the TS and Python cases coming from one repository each. Anonymous
nesting outnumbers named by 6–30×, so the anchors would mostly not exist.

**Rust, C and C++** appeared in 0 of 51 surveyed repositories. **Vue and
Svelte** in 0 of 47, where 18 of 47 carry `.tsx`. A single-file component also
interleaves three grammars and does not fit the one-grammar-per-extension
adapter model — a composite-parsing design question, not a registration.

**Java, Kotlin, C#, Ruby, PHP, Swift, Terraform, Protobuf, GraphQL and Nix**
were counted the same way and are the same answer: the whole fleet holds 15
Kotlin files in one repository, 34 Protobuf in one, 7 Java in two, 7 C#, 6
`.tf`, 4 Swift, and zero Ruby, PHP, GraphQL or Nix. Rust re-measured at 24
files in one repository. Each is a single-repository concentration, the same
shape that closed the earlier survey, and none clears the demand bar that
ordered every shipped grammar.

### Cross-check servers deliberately not wired

**taplo** (TOML) disagrees with tree-sitter on real ranges, measured twice
independently: a dotted sub-table nests under `[server]` as L1..L7, and the
last table in a file extends to EOF. `configClient`
(`internal/lsp/client.go`) exists as protocol reserve for taplo alone.
**`vscode-markdown-language-server`** crashes at startup on an ESM/CJS
`vscode-uri` interop fault. **SQL** has no language server.

**Python multi-line assignments** are left disagreeing on purpose: `pyright`
names the binding's first line where tree-sitter names the statement.
Accepting any server range that is merely the anchor's first line would also
accept a genuine one-line extent bug.

### Dependencies

**`go-git` is not used**, and the reason is not obvious from the code that
shells out to `git`: it has no hooks, no filters, no pathspec matching, and no
credential or GPG prompting. Even read-only use would create a second source of
truth about the repository.

**pflag over the alternatives**, decided on interspersed parsing, which is a
correctness issue rather than a preference. Git accepts flags after
positionals, so `rgit commit auth.go:Foo -m msg` must parse; stdlib `flag` and
`ff/ffcli` stop at the first positional and yield *zero* messages with three
targets rather than erroring. Cobra hardcodes exit 1 and Kong exits 80, neither
of which `rgit` can use. Binary cost measured: stdlib 1644 KB, **pflag 1844
(+12%)**, ffcli 1812, Cobra 2500 (+52%), Kong 3656 (+122%). Completion is
hand-written because no framework shortens the dynamic `rgit symbols` half.

**A private index was rejected.** Seeding one from HEAD to exclude pre-staged
work needs snapshot, restore and rollback — roughly a third more mechanism —
for semantics that diverge from `git add && git commit`.

**The SQL parser is generated at build time, not vendored** (~27 s once, ~7.2 s
to compile the C). `tree-sitter.json` must be copied beside the grammar or ABI
14 is emitted silently, and the generated `.c` cannot sit beside the `.go` or
the symbol is defined twice. No Node is needed: the CLI evaluates `grammar.js`
with its own embedded engine.

### Sharp edges left in place

A CSS at-rule spelled literally `@header` is unreachable, because
`internal/resolve/resolver.go` tests for a pseudo-anchor first. HTML anchors are
element+id only — no class, nth-child, or combinator selectors, and no
`@imports` matcher for `<link>` or `<script src>`, because neither has per-parent
scoping to resolve against.

Whitespace changed between two unmodified tracked declarations is attributed as
`(unanchorable)` and must stay that way: no declaration owns it.
