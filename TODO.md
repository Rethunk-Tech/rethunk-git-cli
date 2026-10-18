# TODO

Future work, and the decisions that closed the door on work not being done.
Behaviour that ships is documented in [`docs/`](docs/); how the code works is
the code. What lives here is only what neither of those can hold: a measurement
that already settled a question. It also holds a design that is accepted but
not built.

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not here.

## --source and --follow-rename cannot combine

[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md) “History across renames”
(lines 171–198) is the contract. `--follow-rename` re-resolves the anchor
with tree-sitter at each rename boundary, one parse per rename, not per
commit (178–181). `--source REV` resolves the extent in that revision under
the file’s current name and does not combine with `--follow-rename`, for
`log` (186–188), `blame` (192–194), and `show` (196–198).

The combination is allowed. A walk can start at an older revision and still
re-resolve across later renames. Argument classification must accept both
flags together. The worktree-bytes rule stays: a `--follow-rename` walk
reads committed blobs (`showHeadSource`, `internal/app/show.go:126`;
`blame` help at `internal/app/blame.go:35`). No new languages.

### Read

`parseAnchorCommandArgs` (`internal/app/shared.go:554`) already records
`--source` and `--follow-rename` as independent flags for `log`, `blame`,
and `show`. The pair is refused after that parse: `runLogAnchor`
(`internal/app/log.go:259`), `runBlame` (`internal/app/blame.go:65`),
`runShow` (`internal/app/show.go:80`).

`walkRenameSegments` (`internal/app/shared.go:433`) is the follow-rename
walk. It sets `rev := "HEAD"` (`shared.go:440`), then re-resolves the
pre-rename blob at each `FindRename` boundary (`shared.go:454`). Callers
are `runLogFollowRename` (`internal/app/log.go:340`),
`runBlameFollowRename` (`internal/app/blame.go:137`), and
`followRenameSymbol` (`internal/app/show.go:141`). `--source` changes the
starting blob only when follow-rename is off (`log.go:271`, `show.go:90`,
`blame.go:99`); with the flag, show and blame force the HEAD blob
(`show.go:91`, `blame.go:75`). The combined walk starts at the `--source`
revision instead of `HEAD` and keeps that same boundary re-resolve.

### Traps

The refusal is documented behaviour. `docs/USAGE.md` states the exit 129
pair at lines 259, 300, and 358, and in the flag table at 787–788.
`docs/LIMITATIONS.md` states it at 186–188, 192–194, and 196–198. Help
text repeats it (`show.go:28`, `blame.go:32`, `log.go:57`), and tests pin
the sentence (`show_test.go:266`, `log_test.go:232`, `blame_test.go:229`).
`USAGE.md` and `LIMITATIONS.md` must change in the same commit as the
code, later. Leave the worktree-bytes rule as it is, and add no languages.

### Acceptance

A rename-plus-reorder fixture of the shape in
`TestRun_LogFollowRenameCrossesARenameThatReordersTheSymbol`
(`internal/app/log_test.go:661`): `old.go` becomes `new.go` in the same
commit that moves `Foo`. `--source REV --follow-rename` on the current
name follows that boundary and reaches the pre-rename commit when `REV`
is at or after the rename. Each flag alone stays as documented.
`--follow-rename` without `--source` still starts at `HEAD` and
re-resolves at the boundary. `--source REV` without `--follow-rename`
still resolves under the current name and does not follow the rename.
The same split holds for `blame` and `show`; `show` still prints the
oldest segment’s bytes.

## CI cross can still skip SQL generation

`make cross` now fails when `-generate-only` does not produce the parser
(`cmd/rgit-install`). `make install` still falls back to an SQL-less host
binary. Keep the `rgit_sql` build tag. Add no languages.

### Read

`SQL_TAGS` (`Makefile:26`) is `-tags rgit_sql` only when
`internal/resolve/sqlgrammar/csrc/parser.c` exists (`Makefile:22–26`).
`sql-parser` (`Makefile:144–145`) runs
`go run ./cmd/rgit-install -generate-only`. `cross` and each
`cross-linux-*` / `cross-windows-*` target depend on it
(`Makefile:147`, `Makefile:170–186`).

`-generate-only` exits non-zero when `sql` is false
(`cmd/rgit-install/main.go`). `generateSQLParser` still reports the miss
and continues without SQL on the `make install` path
(`cmd/rgit-install/main.go`).

### Traps

The remaining fail-open is CI, not Make’s parallel graph. The cross job
installs the tree-sitter CLI only on a parser-cache miss, then runs
`make -j3 -o sql-parser cross`, which never re-runs generation. A cache
hit with no `parser.c` still compiles SQL-less `dist/` binaries. The
cross-job comment records that skip. `release.yml` installs the CLI and
then runs `make cross`; its `languages | grep sql` check is a
publish-time net, not a substitute for failing before any `dist/` binary
is written.

`make install`’s local fallback stays. The installer retries without SQL
when the SQL build fails (`cmd/rgit-install/main.go`), and
`docs/INSTALL.md` promises that fallback. Do not drop the `rgit_sql`
tag, and add no languages.

### Acceptance

A CI cross run whose SQL parser cache is cold without the tree-sitter
CLI exits non-zero and writes no `dist/` binary. A cache hit that
restored no `parser.c` does the same, rather than compiling SQL-less
artifacts. `make install` without the CLI still succeeds and installs an
SQL-less `rgit`.

## `rgit help <command>` still prints top-level help

`rgit help` is a top-level alias of `--help`/`-h`: `Run` matches `help` as
`args[0]` and writes `topLevelHelp` (`internal/app/app.go:97–99`). Any
further words are ignored, so `rgit help diff` exits 0 with the command
list, not `diff`'s own help. Git's `git help <command>` shows that
command. `docs/USAGE.md` § Help (`:494`) names only the three top-level
spellings and `rgit <command> --help`.

Dispatch `rgit help <command>` to the same help that `rgit <command>
--help` already prints, exit 0. Bare `rgit help` stays top-level.
Unknown `rgit help frobnicate` is exit 129 with the existing unknown-
command sentence. Do not open a pager or man page. `rgit version` stays
the version report, not help.

This changes a currently successful `rgit help diff` (top-level text,
exit 0). Tests that only pin `[]string{"help"}` (`internal/app/app_test.go:97`)
do not cover the extra word; adding coverage is part of the work.

Rejected: treating `help` as unknown unless it is the sole argument.
Rejected: `git help`'s man-page/pager path.

### Read

1. `internal/app/app.go:97` `case "--help", "-h", "help"` — prints
   `topLevelHelp` and returns. Extra args are never inspected.
2. `internal/app/app.go:109` onward — the command switch `diff`,
   `commit`, `show`, `blame`, `log`, `context`, `languages`, `doctor`,
   `completion`, `symbols`. Each already accepts `--help`/`-h`.
3. `docs/USAGE.md:494` — documents the three top-level spellings and
   per-command `--help`/`-h`, not `help <command>`.
4. `internal/app/app_test.go:81` `TestRun_TopLevelDispatch` — `help` with
   no extra args. `cmd/rgit/rgit_e2e_test.go` loops `"--help", "-h",
   "help"` the same way.

### Traps

- **`rgit help` with no argument must stay top-level.** Routing through
  a missing-command error would change the documented exit 0.
- **`rgit help --help` is still top-level help**, not a nested usage
  error: `--help` as the second word is the command whose help was
  requested only if we treat it as a command name. Keep the first-word
  `help` alias: extra `--help`/`-h` still print `topLevelHelp`.
- **Do not send `help diff` through `runDiff` with rewritten args** if
  that would open a repository. Help must stay a no-repo path, matching
  `rgit diff --help` today (`parseFlagsOrHelp` / each command's `-h`
  branch).
- **Completion lists `help` as an alias**, not a command that takes a
  subcommand (`internal/app/completion.go` `rgitSubcommands`). Offering
  command names after `help` is in scope; do not invent a second
  completion word list.

### Acceptance

`rgit help` and `rgit help --help` still print `topLevelHelp` and exit 0.
`rgit help diff` (and each other dispatched command) prints that
command's `--help` text, byte-identical, exit 0, with no repository.
`rgit help frobnicate` is exit 129 and names the unknown command.
`rgit diff --help` is unchanged.

## `rgit diff --json` is missing beside `--porcelain`

`languages`, `doctor`, `symbols`, and `log` each pair `--porcelain` with
`--json` as one object whose fields are the porcelain columns in
lowercase. `rgit diff` has `--porcelain` (`internal/app/diff.go:48`) and
renders through `internal/diff/porcelain.go:11` `RenderPorcelain`, but
no `--json`. `--quiet` and `-p`/`--patch` already refuse `--porcelain`
(`diff.go:75–80`).

Add `--json` as that same object for the porcelain rows. Mutually
exclusive with `--porcelain` and with `-p`/`--patch`. `--quiet` stays
exclusive with `--porcelain`; decide `--json`+`--quiet` the same way
(`--porcelain`+`--quiet` is already exit 129). Default text and
`--porcelain` bytes stay. Field names follow
`docs/CODES.md` `rgit diff --porcelain` (`FILE`, `SYMBOL`, `STATUS`,
`ADDED`, `DELETED`) in lowercase. Empty `symbol` is `""`. STATUS tokens
stay the existing strings (`MOD`, `DELETED`, …), not a new vocabulary.

This is larger than log's two-field wrap: five columns, empty-symbol
rows, and the quiet/patch/exit-code matrix. Do not change `RenderPorcelain`.

### Read

1. `internal/app/diff.go:48` `--porcelain`, `:51` `-p`/`--patch`, `:75`
   porcelain/quiet, `:79` porcelain/patch, `:254` `RenderPorcelain`.
2. `internal/diff/porcelain.go:11` — TSV `FILE<TAB>SYMBOL<TAB>STATUS<TAB>ADDED<TAB>DELETED`.
3. `internal/app/log.go` `writeLogJSON` / `refuseLogFormatConflict` —
   the JSON-from-porcelain pattern to copy, not a second row walk.
4. `docs/CODES.md` `rgit diff --porcelain` and `rgit log --json` — the
   schema to extend and the exclusivity sentence.
5. `internal/app/completion.go` `rgitDiffFlags` — must gain `--json` in
   the same commit as help text, or `TestCompletionFlags_MatchLiveFlagSets`
   fails.

### Traps

- **Do not invent a "files" tree.** One object with a row array matching
  porcelain order. Nesting by path would disagree with `--porcelain`.
- **`--quiet` still implies `--exit-code` and suppresses stdout.** JSON
  is an encoding of the report, so `--json --quiet` is 129 like
  `--porcelain --quiet`, not silent JSON.
- **`--patch` after JSON is the same refusal as `--porcelain --patch`.**
  A JSON object plus a git patch body is two formats.
- **Unanchorable / empty symbol rows must keep `"symbol": ""`.** Omitting
  the key would break the "same fields as porcelain" rule `languages`
  already uses.

### Acceptance

`rgit diff --json` lists every `--porcelain` row as one JSON object,
same order, lowercase column names, empty symbol as `""`. Combined with
`--porcelain` or `-p`/`--patch` or `--quiet` is exit 129. Default text
and `--porcelain` are unchanged. `--exit-code` still exits 1 when
committable. Completion and `docs/CODES.md` name the flag in the same
commit.

## `rgit commit --json` is missing beside `--porcelain`

`rgit commit --porcelain` writes `H<TAB>SHA` then
`FILE<TAB>SYMBOL<TAB>ADDED<TAB>DELETED` (`internal/app/commit.go:489–504`,
`docs/CODES.md` `rgit commit --porcelain`). `languages`/`doctor`/`log`
already expose the same facts as `--json`. Commit does not.

Add `--json` as one object: the leading `H` as `sha` (absent on
`--dry-run`, present on `--allow-empty` even with no targets), then
`targets` with porcelain columns in lowercase. Mutually exclusive with
`--porcelain` and with `-q`/`--quiet` (those two already refuse each
other at `commit.go:163`). Human summary and `--porcelain` stay.

Larger than log JSON: dry-run has no `H`, allow-empty has `H` and no
targets, pathspecs expand to one record per staged file
(`writeTargetRecords`). The H/no-H rule must not drift from porcelain.

### Read

1. `internal/app/commit.go:124` `--porcelain`, `:163` porcelain/quiet,
   `:489` the `H` record, `writeTargetRecords`.
2. `docs/CODES.md` `rgit commit --porcelain` — `H` on real commits, none
   on `--dry-run`, `H` with no targets on `--allow-empty`.
3. `internal/app/log.go` `writeLogJSON` — encoder flags
   (`SetEscapeHTML(false)`, indent two spaces).
4. `internal/app/completion.go` `rgitCommitFlags`.

### Traps

- **`--dry-run --json` must not invent a sha.** Porcelain emits target
  rows only. JSON omits `sha` or uses `""` only if porcelain would have
  printed `H`; it does not, so omit `sha` with `json:",omitempty"` or
  emit `"sha": ""` — pick the one that still round-trips "no H record"
  and document it in `CODES.md` in the same commit.
- **`--porcelain --quiet` is already 129.** `--json --quiet` is the
  same, not empty stdout.
- **Do not add a STATUS column.** Porcelain has none; JSON must not.

### Acceptance

A real commit's `--json` object names the full HEAD sha and one target
per porcelain target row. `--dry-run --json` has those target rows and
no sha fact porcelain would not have printed. `--allow-empty --json`
still names the sha with `"targets": []`. `--json` with `--porcelain` or
`-q` is exit 129. The human summary and `--porcelain` are unchanged.

## `rgit show --json` needs a byte-safe encoding, not a TSV wrap

`show --porcelain` length-frames extents (`FILE:ANCHOR<TAB>NBYTES` plus
exactly `NBYTES` bytes, `internal/app/show.go:33–36`) because a symbol
body can contain any byte, including a header-shaped line. `--json` as
used on `log`/`symbols` is UTF-8 text fields. Putting the extent in a
JSON string would require an encoding choice (UTF-8 with replacement,
base64, or `\u` escapes) that `--porcelain` does not need.

Do not add `--json` until that encoding is decided in `docs/CODES.md`
and a consumer exists. `--porcelain` / `--with-header` stay the
machine-readable path. Default one-anchor output stays raw bytes.

Rejected: JSON that assumes the extent is valid UTF-8. Rejected: wrapping
only the header and leaving the body on stdout after the object.

### Read

1. `internal/app/show.go:21` help, `:33` `--porcelain`, `:111` `framed`.
2. `docs/CODES.md` `rgit show --porcelain` — length-framed dialect, not
   TSV+newline.
3. `docs/CODES.md:137` — `--json` is defined as one object for
   languages/doctor/context/symbols/log, none of which carry arbitrary
   file bytes.

### Traps

- **A Go `json.Encoder` will `\u`escape `<` if `SetEscapeHTML` stays
  default-true.** Even with it off, invalid UTF-8 becomes U+FFFD.
  Base64 avoids that and is a new dialect, not "the porcelain fields."
- **Single-anchor default is unframed bytes for piping.** JSON would
  always frame, like `--porcelain`, and must not change the no-flag
  path.

### Acceptance

No `--json` on `show` until `CODES.md` names the byte encoding and a
test pins that a fixture with non-UTF-8 bytes round-trips. `--porcelain`
remains the scripting form.

## `rgit context --porcelain` would alias the default stream

`docs/CODES.md:144` states `context` has no `--porcelain`; `--json`
encodes the same record stream. The default output is already TSV with a
leading type tag (`internal/app/context.go:42`). `--porcelain` as an
unknown flag is exit 129 (`context.go:125–130`,
`internal/app/context_test.go` `--json --porcelain`). Adding
`--porcelain` as a no-op alias of the default, or as a second encoding
beside `--json`, either changes that 129 or invents a dialect the
default already is.

Leave the refusal. If a caller wants TSV, they omit `--json`. If a
caller wants an explicit flag name, that is a contract change to document
in `CODES.md` (the three `--porcelain` dialects table) and in completion
(`rgitContextFlags`), not a silent alias.

### Read

1. `docs/CODES.md:144` — "`context` has no `--porcelain`".
2. `internal/app/context.go:27` help (`[--json]` only), `:125` any other
   argument is extra.
3. `internal/app/context_test.go` the `--json --porcelain` 129 case.
4. `internal/app/completion.go` `rgitContextFlags = "--json -h --help"`.

### Traps

- **Making `--porcelain` succeed as a synonym of the default changes
  exit 129 tests and the "records are fixed; any other argument is
  refused" rule in `docs/USAGE.md` § Context.**
- **Do not emit a headerless copy that drops the type tags.** `F` rows
  would then collide with `rgit diff --porcelain`, which is a different
  dialect on purpose.

### Acceptance

`rgit context --porcelain` remains exit 129. `--json` and the text
stream stay as documented. No new flag.

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
`.tf`, 4 Swift, and zero Ruby, PHP, GraphQL or Nix. Each is a
single-repository concentration, the same shape that closed the earlier
survey, and none clears the demand bar that ordered every shipped grammar.

**Rust was counted the same way and shipped anyway**, which is what the file
count alone got wrong. 26 files in one repository is the same
single-repository shape as the rest, but `rgit symbols` is the designated
tool for symbol lookup, so "no grammar" is not a smaller feature set for a
Rust repository -- it is the whole workflow refusing, 11,812 lines of it. A
grammar is ordered by whether a fleet repository needs it, not by how many
files carry the extension.

### Cross-check baseline

A 264-file fleet corpus (2026-10-09: 22 files in each of eleven wired grammars,
21 `.jsonc`, 1 `.scss`; the fleet holds no `.zsh` file) compares **5,240 symbols
with 2 disagreements** (4 before the Rust fix below; 5,157 before Rust impl members were compared).
That is the number a later run is measured against;
`make xcheck` runs the same comparison over the committed fixtures, and
`SYNTH_CORPUS` points the write-side property test at the same kind of list.
The files are every Nth file of each extension from the fleet checkouts
(300 bytes to 150 kB, no vendored, built or `testdata` paths), and one run takes
68 s.

Both disagreements are CSS error-recovery fixtures inside the vendored
`AlbinoGeek/biome` checkout (`declaration_emty.css`, an empty declaration
`a { ; }`, and `casing.css`, one upper-cased `.attr` rule), where the CSS
server ends the extent early. Two Rust files in the same checkout disagreed
until a leading `//!` inner doc comment stopped being folded into the first
`mod x;` extent (`commentBinder`); rust-analyzer now agrees on all 25 symbols
in those two files.

424 anchors go unnamed (507 before rust-analyzer's `impl Type` containers were
read as the type, which left every impl member unnamed). By group:

- CSS, 161: expected. 99 are one error-recovery fixture (`casing.css`) of
  repeated and upper-cased selectors, 44 are `@custom-variant` / `@theme`
  at-rules the server does not report, 18 are in other stylesheets
  (`globals.css` 8), mostly selectors heading several rules (`sameRange`).
- `.jsonc`, 242: all in one 4,000-line `biome.jsonc` (the other 20 files agree on
  all 794). Not broken down: the likely cause is repeated key names such as
  `level` under many rules, which the ordinal guard (below) refuses to pair.
- Shell, 15: expected, variable assignments the server never names (below).
- TSX, 3: `actual`, `useNavigate` and `skip` in one test file, locals and
  destructured names; not examined.
- HTML, 2: ids containing a dot (`a#toc-mod.io`). The corpus left them unnamed
  because a `.` in the id was read as the language server's class separator.
  Matching now keeps the full `tag#id` and treats only a further `.class`
  suffix as that separator, so the same ids cross-check.
- Go, 1: the blank identifier `_`; expected.

An earlier 220-file run compared 4,920 symbols with 0 disagreements and 158
unnamed; the ratios move with the corpus.

### Ordinal anchors go unpaired when the counts differ

An anchor like `body#2` pairs with a server symbol only when the server
reported the same number of same-named symbols the resolver found
(`matchLSPSymbol`). Measured on real Python, it routinely does not: two
module-level `body = ...` statements are two tree-sitter declarations but one
pyright symbol, because a rebinding is not a second symbol to a type checker.
The guard is correct as written -- indexing the server's list by an ordinal
when the counts disagree pairs two unrelated declarations and reports a
disagreement in which neither side is wrong.

The shell half of the same column is a server limitation with nothing to fix
here: `bash-language-server` reports no symbol at all for a variable
assignment, so `f`, `err` and `DRY_RUN` are never named. Together these are
the whole `notNamed` column: 32 of 917 anchors fleet-wide.

### Cross-check servers deliberately not wired

**taplo** (TOML) disagrees with tree-sitter on real ranges, measured twice
independently: a dotted sub-table nests under `[server]` as L1..L7, and the
last table in a file extends to EOF.
**`vscode-markdown-language-server`** crashes at startup on an ESM/CJS
`vscode-uri` interop fault. **SQL** has no language server.

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

2026-10-14: bypass-150327Z-2 left: worktree-then-HEAD reads in show, blame, and symbols stay separate because the errors and headOnly differ.
2026-10-14: bypass-150327Z-6 left: readInstallerFile stays; a missing installer file must remain an error.
