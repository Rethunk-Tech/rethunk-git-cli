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

## make cross can ship SQL-less binaries without failing

[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md) “Build and platform limits”
(lines 86–100) is the contract that ships today. SQL ships only behind
the `rgit_sql` build tag (94–96). `make cross` carries SQL only when the
build host can generate the parser, which needs the tree-sitter CLI;
without it every `dist/` binary is SQL-less, the same fallback `make
install` makes (97–100).

`make cross` fails when the host cannot generate the SQL parser, so it
does not publish `dist/` binaries that lack SQL. A host with the CLI
keeps shipping SQL. Keep the `rgit_sql` build tag. Add no languages.
`make install`’s local fallback stays: a machine without the CLI still
gets a working `rgit` without SQL.

### Read

`SQL_TAGS` (`Makefile:26`) is `-tags rgit_sql` only when
`internal/resolve/sqlgrammar/csrc/parser.c` exists (`Makefile:22–26`).
`sql-parser` (`Makefile:144–145`) runs
`go run ./cmd/rgit-install -generate-only`. `cross` depends on it
(`Makefile:147`), then builds linux/amd64, linux/arm64, and
windows/amd64 with `$(SQL_TAGS)` (`Makefile:170–186`). The comment at
`Makefile:129–135` records the current choice: without the CLI,
`SQL_TAGS` is empty and every target builds SQL-less.

`-generate-only` returns after generation and exits 0 when the CLI is
absent (`cmd/rgit-install/main.go:98–107`). `generateSQLParser` reports
the miss and continues without SQL (`cmd/rgit-install/main.go:319–335`).

### Traps

A CI runner without the tree-sitter CLI must fail the job, not skip SQL
quietly. `.github/workflows/ci.yml` records the opposite at 103–104, and
the cross job installs the CLI only on a parser-cache miss
(`ci.yml:132–135`) before `make -j3 -o sql-parser cross` (`ci.yml:143`).
`release.yml` installs the CLI and then runs `make cross`
(`release.yml:54–60`). The later `languages | grep sql` check
(`release.yml:64–69`) is a publish-time net, not a substitute for failing
before any `dist/` binary is written.

`cross-linux-amd64`, `cross-linux-arm64`, and `cross-windows-amd64` do
not depend on `sql-parser` themselves (`Makefile:170–186`); only `cross`
lists it (`Makefile:147`). A parallel `make -j cross` can start those
recipes before `sql-parser` finishes, and each recipe `mkdir`s `dist/`
and writes its binary as soon as it runs. The failure has to land before
any of those recipes writes a `dist/` binary.

`make install`’s local fallback can stay. The installer retries without
SQL when the SQL build fails (`cmd/rgit-install/main.go:131–139`), and
`docs/INSTALL.md` promises that fallback at 119–122 and 144–146. Leave
that promise. Do not drop the `rgit_sql` tag, and add no languages.
`docs/LIMITATIONS.md` 97–100 and `docs/INSTALL.md` 111–122 describe the
silent cross fallback and change in the same commit as the code, later.

### Acceptance

`make cross` on a host without the tree-sitter CLI exits non-zero and
writes no `dist/` binary. `make cross` on a host with the CLI still
produces an SQL-capable binary (`languages` includes `sql`, built with
`-tags rgit_sql`). `make install` without the CLI still succeeds and
installs an SQL-less `rgit`.

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
