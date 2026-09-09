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
- **Its read half already shipped.** `rgit show FILE:SYMBOL --source REV`
  resolves the anchor in a revision's blob and prints that extent, which is
  every step of the mechanism above except the splice and the write. Building
  restore is now the backup contract and the guardrails, not the reading.

### Orphan-gopls handshake cleanup

A managed socket whose handshake failed is unlinked
(`internal/lsp/dial.go`). A live-but-stuck `gopls` behind it is left to its own
`-listen.timeout=10m`. A shutdown RPC or kill-by-pid needs a PID `rgit` never
discovers and no server exposes, so neither is in scope.

### Not queued

`rgit context` staged/unstaged split — it would be a second `diffpkg.Run` for a
bit `STATUS` never carried. SCSS, zsh, JSONC, JSON5. Include-style pathspec
flags. Any web surface: this is a CLI.

**Staging without committing** — `rgit commit --stage-only`, or a separate
`rgit add`. `internal/synth/stage.go` already stages before committing, so the
flag would be an early return, but there is no unstage counterpart and cannot
cheaply be one: `git reset` is pathspec-granular, so backing one symbol out of
a synthesized blob means re-synthesizing the blob without it. Leaving a symbol
staged would create a state `rgit` cannot undo. The batch case it would serve
is already served — `rgit commit a.go:X b.ts:Y c.py:Z` stages a whole set in
one call, and `--dry-run` previews that set without writing an object.

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

### Cross-check baseline

A 60-file fleet corpus across all ten wired grammars compares **885 symbols
with 0 disagreements**, and 32 anchors the servers do not name (20 CSS
at-rules and nested rules, 4 each in HTML, Python and shell). That is the
number a later run is measured against; `make xcheck` runs the same
comparison over the committed fixtures.

Before CSS was indexed per selector the same corpus compared 603 with 120
unnamed, so the difference is 282 anchors that were silently unverified
rather than any change in agreement.

### Cross-check servers deliberately not wired

**taplo** (TOML) disagrees with tree-sitter on real ranges, measured twice
independently: a dotted sub-table nests under `[server]` as L1..L7, and the
last table in a file extends to EOF. `configClient`
(`internal/lsp/client.go`) exists as protocol reserve for taplo alone.
**`vscode-markdown-language-server`** crashes at startup on an ESM/CJS
`vscode-uri` interop fault. **SQL** has no language server.

**Python multi-line assignments** were left disagreeing until the corpus
measurement was possible. `pyright` names the binding's first line where
tree-sitter names the statement, and once pyright was installed that was a
hard extent mismatch (exit 6) on an ordinary multi-line dict or tuple: the
anchor became unstageable, and 49 of 400 surveyed Python files carried at
least one. `cmd/xcheck` put numbers on it -- 4 of 6 symbols agreeing before,
6 of 6 after.

The fix is `pythonLanguage.trimDeclOnlyEnd` (`internal/resolve/lang_python.go`),
the same `declOnlyEndTrimmer` seam HTML and YAML already use: the extent the
cross-check compares ends with the binding's first line, while the extent that
gets staged is still the whole statement. Scoped to `expression_statement`, so
it is narrower than the blanket "accept any first-line server range" rejected
here -- a function or class keeps its full-extent comparison, and a genuine
one-line extent bug there still fails.

### Performance

`rgit` was measured across the fleet's two largest repositories rather than
tuned: on a 3,696-file repo, `context` is 0.03 s, `diff` under 0.01 s, and
`diff --range HEAD~50..HEAD` 0.37 s; a 1,846-file repo answers the same three
in 0.02 s, under 0.01 s, and 0.45 s. Startup is unmeasurable at 10 ms
resolution despite the 18 MB cgo binary. There is no caching, indexing, or
daemon work worth doing, and the absence of persistent state stays a design
property rather than a cost.

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
