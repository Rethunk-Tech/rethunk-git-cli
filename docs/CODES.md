# Codes

The machine contract: what `rgit` exits with, and what it writes when asked for
machine-readable output. This file is authoritative for both — every other
document and every Go doc comment points here rather than restating it.

For the flags that produce these, see [`USAGE.md`](USAGE.md).

## Exit codes

| Exit | Condition |
| --- | --- |
| 0 | Success (possibly with stderr warnings for unchanged targets) |
| 1 | `diff` only, and only under `--exit-code`/`--quiet`: something is committable |
| 3 | Anchor unresolvable — missing in both worktree and HEAD; candidates listed |
| 4 | Ambiguous anchor — candidates listed |
| 5 | Contradictory anchors — one path named both as a path and as a symbol anchor |
| 6 | Normalized LSP ↔ tree-sitter extent mismatch (`commit` only) |
| 7 | Refused path — gitignored and untracked |
| 8 | Commit succeeded; `--push` failed |
| 9 | Unsupported / deferred language for a symbol anchor |
| 10 | Symbol anchor refused on a special path (`commit` only: symlink, gitlink, binary, unmerged, skip-worktree, assume-unchanged) |
| 11 | All named targets resolve but have no uncommitted changes |
| 12 | Symbol anchor refused on a structured-data file (JSON, YAML, TOML) (`commit` only) |
| 128 | Fatal git / system failure (includes hook rejection, GPG failure, a `-C` directory that cannot be entered) |
| 129 | Invalid usage (bad flags, missing message, no targets, path escape, malformed `-C`) |

128 and 129 follow git's own conventions. 1 is git's `--exit-code` convention
and is deliberately absent from the named constants: unlike every other status
here, its meaning is conditional on a flag rather than fixed.

Those two are the only codes **every** command can produce, `languages`,
`doctor` and `completion` included, because the global `-C <path>`
([`USAGE.md`](USAGE.md#global-flags)) is validated before dispatch: a
missing directory argument is 129 and an unenterable directory is 128 even
for a command that would never have opened a repository. Git chdirs before
dispatch too, so a broken `-C` cannot be silent on one command and fatal on
the next.

The numeric bindings live in `internal/exitcode`, which spells no meaning of
its own — the constant names carry it, and this table defines it.

### Exit 6 is `commit`'s alone

`rgit diff` reports the identical disagreement as a `[warning]` on stderr and
still exits 0 (or 1 under `--exit-code`). A diff is a read-only report, and the
point of surfacing it there is that the caller learns of it while reading the
diff rather than mid-commit.

### Exit 12 is `commit`'s alone

`rgit diff --sym`, `rgit show`, `rgit blame`, and `rgit log` all resolve a
`FILE:SYMBOL`
anchor into JSON, YAML, or TOML exactly like any other anchor — none writes a
blob, so there is nothing for the guard to protect. Only `rgit commit` would
splice a synthesized extent into a blob and stage it, so only it refuses. Name
the path instead: `rgit commit config.yaml` stages the whole file, unaffected.

### A missing cross-check is never a failure

When no language server is reached, any command that runs the diff
cross-check — `diff`, `commit`, and `context`, which composes `diff`'s own
default scope — prints `[ts-only]` on stderr and proceeds; degraded resolution
is normal, not an error ([`AGENTS.md`](../AGENTS.md#resolution-model)).
`rgit diff --quiet` still prints it, since `--quiet` suppresses the report on
stdout, not diagnostics.

### The cross-check compares lines, not columns

Both sides of the exit-6 comparison reduce to a 0-based start/end line pair
before comparing (`internal/resolve/crosscheck.go`'s `lineOf`); a language
server and tree-sitter agreeing on every line but disagreeing on a column
within one passes silently. Deliberate, not a gap: the anchors this
resolver stages are whole declarations, never sub-line ranges, so a
same-line disagreement has nothing narrower for either side to report
against.

### `show` shares the anchor codes, plus one of git's own

`rgit show FILE:SYMBOL` resolves its one anchor exactly like `commit` does, so
3, 4, and 9 carry their usual meanings and nothing is ever widened to a
whole-file dump. Its one addition is `--source`: an unknown revision is **128**
(`GitFailure`), matching git's own exit for a revision it cannot parse, while a
path that is simply absent at a revision git does know stays **3**
(`AnchorUnresolvable`). The two are distinguished deliberately — `cat-file`
reports both as "no such object", and collapsing them would report a mistyped
branch as a missing symbol.

### `blame` shares the anchor codes, not the staging ones

`rgit blame FILE:SYMBOL` resolves its one anchor exactly like `commit` and
`diff --sym` do, so 3 (unresolvable), 4 (ambiguous), and 9 (unsupported
language) mean the same thing there. It never stages or commits anything, so
1, 5, 6, 7, 8, 10, 11, and 12 do not apply — a failure past resolution is `git
blame`'s own exit, folded into 128 the same way any other unexpected git
failure is.

### `log` shares the anchor codes too, resolved against `HEAD`

`rgit log FILE:SYMBOL` behaves as `blame` does — 3, 4, and 9 carry the same
meanings, the same 1, 5, 6, 7, 8, 10, 11, and 12 exclusions apply, and a
failure past resolution folds into 128 — except that it resolves against
`HEAD`'s own blob rather than the worktree, since history is a question about
what has already been committed.

`rgit log --since`/`--until` with no `FILE:SYMBOL` positional is the second,
unanchored shape (see [`USAGE.md`](USAGE.md#log-by-date-and-path)); it resolves
no anchor, so none of 3, 4, or 9 apply to it. When a `FILE:SYMBOL` positional
is present, the anchor shape remains selected and the date bounds are passed
to `git log -L`. In either shape, 129 covers bad flags, missing values, and a
path that escapes the repository root; 128 covers an unwalkable path or bad
date, folded from `git log`'s own exit.

### `context` has no anchor to resolve at all

`rgit context` names no symbol, so 3, 4, and 9 never apply either, and it
stages nothing, so the same 1, 5, 6, 7, 8, 10, 11, and 12 exclusions hold. A
failure reaching `git log` or `git diff` underneath it folds into 128.

## Output records

The record formats defined here are for `--porcelain` output only; a
command's own default is git's or `rgit`'s aligned human-readable layout
(`diff`, `commit`, `blame`, `log`, `languages`). `--porcelain` replaces
that with stable tab-separated records, no header.

This is not a hypothetical contract: shell completion scripts call `rgit
symbols` and `rgit symbols --for-commit` (`internal/app/completion.go`), while
`rgit context`'s `F` records reuse the same field layout as `rgit diff
--porcelain` (`internal/app/context.go`). Reordering or adding a column is a
breaking change in-tree, not just for external scripts.

### The three `--porcelain` dialects

`--porcelain` is not one format but three dialects, fixed per command so
scripts can rely on them without probing:

| Dialect | Commands | Record separator | Shape |
| --- | --- | --- | --- |
| TSV + newline | `diff`, `commit`, `languages`, `doctor`, `log` | newline | `FIELD<TAB>FIELD...` records, no header — see each section below (`context`'s fixed record stream is the same TSV + newline shape, with a leading type tag per line) |
| NUL-terminated | `symbols` | NUL | Identical fields and order to the newline form; only the record separator moves, so a symbol may contain any byte but NUL |
| Passthrough git format | `blame` | git's own | No rgit record shape at all: `--porcelain` passes straight through to git's own `git blame --porcelain` output, unmodified |

`rgit show --porcelain` is the one further shape: length-framed
`FILE:ANCHOR<TAB>NBYTES` headers followed by exactly `NBYTES` bytes (see
`rgit show --porcelain` below), because a symbol's own bytes can contain
anything, including a line that looks like a header. Existing bytes are
unchanged by any of this: adding `--porcelain` never moves a default
human-readable layout or an existing `--porcelain` record.

### `rgit diff --porcelain`

```text
FILE<TAB>SYMBOL<TAB>STATUS<TAB>ADDED<TAB>DELETED
auth.go<TAB>ValidateToken<TAB>MOD<TAB>12<TAB>3
auth.go<TAB>oldHelper<TAB>DELETED<TAB>0<TAB>14
auth.go<TAB><TAB>UNANCHORABLE<TAB>2<TAB>0
newfile.go<TAB>@header<TAB>MOD<TAB>2<TAB>0
newfile.go<TAB>NewFunc<TAB>MOD<TAB>13<TAB>0
config.ini<TAB><TAB>UNTRACKED<TAB>4<TAB>0
script.sh<TAB><TAB>MODE<TAB>0<TAB>0
logo.png<TAB><TAB>BINARY<TAB>-<TAB>-
```

`STATUS` is one of:

| Token | Means |
| --- | --- |
| `MOD` | A changed symbol, or a changed file whose language has no grammar |
| `DELETED` | A symbol present in `HEAD` and gone from the worktree |
| `UNANCHORABLE` | Hunks in a supported file that no symbol owns |
| `UNTRACKED` | A file git does not track *and* cannot attribute by symbol — binary, or a language with no grammar |
| `MODE` | A permission change with no content edit |
| `BINARY` | A binary file; both counts are `-` |

An untracked file with a supported grammar (`newfile.go` above) attributes
per symbol like a brand-new tracked file — there is no `HEAD` blob to diff
against, so every declared symbol is wholly new and rows read `MOD`, not a
distinct "new" token. `--sym` filters it like any other file. `UNTRACKED`
survives only as the collapsed fallback for a binary file or one whose
language has no grammar to attribute by (`config.ini` above), where
`HintSymbol`, when resolvable, still points a caller at `--sym`/`--file`.

### `rgit commit --porcelain`

```text
H<TAB>SHA
FILE<TAB>SYMBOL<TAB>ADDED<TAB>DELETED
H<TAB>a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2
auth.go<TAB>ValidateToken<TAB>12<TAB>3
package.json<TAB><TAB>4<TAB>1
```

The leading `H` record is emitted after every successful real commit and
contains the full commit object id. It precedes all target rows, so a caller
can learn the committed revision without a follow-up `git rev-parse HEAD`.
`--dry-run --porcelain` emits target rows only and never invents an `H` record.
An `--allow-empty` commit still emits `H` even when no target rows follow it.

**A pathspec produces one record per file it stages, not one for the
pathspec.**

- Naming a directory stages everything under it, so `rgit commit
  apps/auth` lists `apps/auth/a.go`, `apps/auth/b.go` and so on, each with its
  own counts — the same breakdown `rgit diff` gives, rather than a single total
  that says something moved without saying what.
- Untracked files under that pathspec are listed too, since `git add` stages
  them as well, and a renamed file is listed at its new path.
- A binary file is listed with zero counts: it is being staged, and git reports
  no line counts for it.
- A pathspec that matches nothing keeps one record naming the pathspec itself,
  so `git add`'s own "did not match any files" is still what answers for it.

There is no `STATUS` column: an unchanged target is omitted from the listing
entirely (it gets its own stderr warning instead), so every target record would
carry the same value.

- `--porcelain` replaces `git commit`'s own summary rather than
  adding to it — exactly as `git commit --porcelain` does.
- Because unchanged targets are omitted, `--porcelain --allow-empty` writes only
  the leading `H` record while still creating a commit and exiting 0.

### `rgit languages --porcelain`

```text
NAME<TAB>EXTENSIONS<TAB>GATED<TAB>CROSS-CHECK
css<TAB>.css<TAB>0<TAB>wired
go<TAB>.go<TAB>0<TAB>wired
html<TAB>.html .htm<TAB>0<TAB>wired
json<TAB>.json<TAB>0<TAB>wired
markdown<TAB>.md .markdown<TAB>0<TAB>wired
python<TAB>.py .pyi<TAB>0<TAB>wired
shell<TAB>.sh .bash<TAB>0<TAB>wired
sql<TAB>.sql<TAB>1<TAB>ts-only
toml<TAB>.toml<TAB>0<TAB>ts-only
tsx<TAB>.tsx .jsx .js .mjs .cjs<TAB>0<TAB>wired
typescript<TAB>.ts .mts .cts<TAB>0<TAB>wired
yaml<TAB>.yaml .yml<TAB>0<TAB>wired
```

Sampled from a `-tags rgit_sql` build. One record per grammar compiled into
this binary, sorted alphabetically by `NAME`.

`EXTENSIONS` is every extension the grammar claims, leading dot
included on each, joined with a single space — unambiguous, since a real
extension is always `.something` and never itself contains whitespace.

`GATED` is `1` when the grammar exists in this binary only because a build
tag selected it (SQL alone, `-tags rgit_sql`; see
[`INSTALL.md`](INSTALL.md#sql-support)) and `0` otherwise, present on every
row rather than only the gated ones, so a reader always gets a definite
answer instead of inferring "not gated" from an absent column.

`CROSS-CHECK` is `wired` when the language has a compile-time entry in the
language-server catalog and `ts-only` when it does not (currently TOML and
SQL). This is design-time wiring, not reachability: a `wired` language still
degrades to `[ts-only]` when its server is missing, cold, or unreachable; see
`rgit doctor` for environment and server status.

There is no row at all for a grammar this build was not compiled with: a
plain build's records have no `sql` line, matching `rgit languages`'s own
human output and `rgit --version`'s second line.

`--in-repo` narrows the same rows to grammars with a matching tracked file in
the current repository (requires a git repo); only which rows appear changes,
never the record shape.

See [`USAGE.md`](USAGE.md#languages).

### `rgit doctor --porcelain`

```text
env<TAB>git<TAB>ok<TAB>/usr/bin/git
env<TAB>git version<TAB>ok<TAB>2.43.0
env<TAB>tree-sitter CLI<TAB>MISSING<TAB>optional -- only needed to rebuild with SQL support, see docs/INSTALL.md § SQL support
server<TAB>gopls (go)<TAB>ok<TAB>/home/user/go/bin/gopls (reachable)
server<TAB>vtsls (typescript, tsx)<TAB>MISSING<TAB>not on PATH -- see docs/INSTALL.md § Language servers
```

One record per check, tab-separated, no header:

| Field | Meaning |
| --- | --- |
| `KIND` | `env` for git presence, git's own version, and the optional tree-sitter CLI; `server` for a language server |
| `NAME` | The check's own name — a server's includes the languages it covers, matching the human report |
| `STATUS` | `ok` or `MISSING`, the same two spellings the human `[ok]`/`MISSING` report uses -- unaffected by `--deep`, which only ever adds detail, never changes this column |
| `DETAIL` | The resolved path when `ok`, or a caller-facing note when `MISSING`. Under `--deep`, an `ok` server's path gains a trailing `(reachable)` or `(degraded -- handshake timed out or unanswered)` from a real dial (`internal/lsp.Dial`) |

Grammars are not repeated here: `rgit languages --porcelain` above already
owns that listing, and `rgit doctor` reusing a second copy would be one more
place for the two to drift. Exit code is unaffected by `--porcelain` — a
missing git is still fatal (exit 128), a missing language server or the
tree-sitter CLI is still exit 0 with `MISSING` in its own record.

### `rgit blame --porcelain`

Not a new record shape: `--porcelain` passes straight through to git's own
`git blame --porcelain` output, unmodified. See `git help blame` for that
format — rewrapping it in a second, rgit-specific shape would be exactly the
duplication this file exists to avoid, for a fact git already establishes.

### `rgit context`

Unlike every other command in this file, `rgit context` has no aligned
human default to alternate with: its one output shape is always this
tab-separated record stream, no header, no `--porcelain` flag to ask for it
— see [`AGENTS.md`](../AGENTS.md) for why.

```text
B<TAB>main<TAB>origin/main<TAB>0<TAB>2
S<TAB>merge
W<TAB>ts-only
W<TAB>warning<TAB>extent disagreement reported on stderr
F<TAB>auth.go<TAB>ValidateToken<TAB>MOD<TAB>12<TAB>3
F<TAB>config.ini<TAB><TAB>UNTRACKED<TAB>4<TAB>0
C<TAB>a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2<TAB>fix(auth): reject expired tokens
C<TAB>9e8f7d6c5b4a9e8f7d6c5b4a9e8f7d6c5b4a9e8f<TAB>feat(auth): add ValidateToken
X<TAB>TRUNCATED<TAB>3
```

Seven record types, distinguished by the first field:

| Type | Fields after the type tag | Means |
| --- | --- | --- |
| `B` | `BRANCH`, `UPSTREAM`, `AHEAD`, `BEHIND` | At most one, first when present: the current branch. `UPSTREAM` is empty and `AHEAD`/`BEHIND` are both `0` when no upstream is configured — a definite answer, not inferred from an absent column. Absent on an unborn branch and replaced by `H` on detached HEAD |
| `H` | `SHA` | One detached-HEAD record with the full commit object id; absent on an unborn branch |
| `S` | `OP` | One active sequencer operation: `merge`, `cherry-pick`, `revert`, `rebase`, or `bisect` |
| `W` | `ts-only` | One when at least one file had symbols to cross-check but no live language server was reached. The identical `[ts-only]` notice remains on stderr |
| `W` | `stash` | One when the repository has a `refs/stash` ref |
| `W` | `sparse` | One when `core.sparseCheckout` is true |
| `W` | `warning`, `TEXT` | One per non-fatal diff warning. `TEXT` is the warning body without the human `[warning]` prefix; the identical `[warning] TEXT` line remains on stderr |
| `F` | `FILE`, `SYMBOL`, `STATUS`, `ADDED`, `DELETED` | One `rgit diff --porcelain` row, identical fields — `STATUS` is the same six tokens § Output records defines above |
| `C` | `HASH`, `SUBJECT` | One recent commit, newest first, bounded to the last 20 |
| `X` | `TRUNCATED`, `COUNT` | At most one, always last: `COUNT` records were withheld to hold the 16 KiB byte budget |

**A `B` or `H` record, when present, always sorts first, `S` follows it, `W`
diagnostics follow that, and `F` records always precede `C` records** — a breaking change from the
original commits-first order — **while an `X` record, when present, is always
the last line.**

- `B` is a single record and costs the budget almost nothing.
- Diagnostics are emitted before the actionable, unbounded diff rows; commit
  history is already bounded to 20 and cheap to drop, and is one `git log` call
  away if the caller needs it back.
- See [`USAGE.md`](USAGE.md#context) for the byte budget. 16 KiB is the
  point past which a caller is reading a diff rather than orienting; records
  are dropped whole at the boundary, never truncated mid-record.

### `rgit log --porcelain`

```text
HASH<TAB>SUBJECT
a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2<TAB>fix(auth): reject expired tokens
9e8f7d6c5b4a9e8f7d6c5b4a9e8f7d6c5b4a9e8f<TAB>feat(auth): add ValidateToken
```

For the anchor form, one record per commit whose own diff touched the named
symbol's current extent, newest first — the same ordering `git log`'s own
default gives. For the unanchored form, one record per matching commit in the
date/path scope, same ordering.

`HASH` is the full commit object id, never abbreviated (unlike the aligned
default's `<abbrev-hash> <subject>`, which is for a human to read, not to
paste elsewhere).

Patch-free: this is the one record shape in this file that is never emitted
alongside `-p`/`--patch`, since asking for both would mean asking for a record
format and a patch dump at once. `rgit log -p` instead prints git's own
`git log -L` output unmodified, the same "pass through git's own format rather
than inventing a second one" choice `rgit blame --porcelain` already makes.

### `rgit symbols --with-lines`

```text
START,END<TAB>SYMBOL
1,3 @imports
12,27 ValidateToken
```

One record per declared symbol, in source order — the same symbols and order
the bare `rgit symbols` form prints, unchanged by this flag. `START,END` is
git's own `-L` range grammar: 1-based and inclusive on both ends, over the
source the symbols were resolved from (the worktree file, or its `HEAD` blob
when the worktree copy is gone).

The range is the one `rgit blame FILE:SYMBOL` and `rgit log FILE:SYMBOL` bound
themselves to — all three convert the identical resolved extent, so a range
that disagreed with what blame blames would be a resolver bug, not a formatting
difference.

`--porcelain` terminates each record with NUL instead of a newline, leaving
every field and its order unchanged. That is what makes the format total: a
symbol may then contain any byte but NUL, which source text cannot carry (a
file holding one is binary, and refused). Use it wherever the listing is
parsed rather than read — the newline form is only safe while no grammar
admits a newline into a name, which CSS grouped selectors once did.

`SYMBOL` is last because it is the unbounded field: an anchor may carry
container qualification, an ordinal (`init#2`), or a gopls-spelled receiver,
while the range never contains a tab. Splitting on the first tab is therefore
always correct.

### `rgit show --porcelain`

```text
auth.go:ValidateToken 118
func ValidateToken(tok string) error {
 ...
}auth.go:@imports 34
import (
 "errors"
)
```

Under `--porcelain`, every extent is framed by a header line
`FILE:ANCHOR<TAB>NBYTES` followed by exactly `NBYTES` bytes — the same
framing multi-anchor output already uses, now applied unconditionally, so a
script need not special-case an argument list that happens to hold one.
Single-anchor and multi-anchor output are both framed; without the flag,
single-anchor output stays raw bytes, byte-identical to before.

The length, not a delimiter, is what makes the stream unambiguous — a
symbol's own bytes can contain anything, including a line that looks like a
header, so a reader consumes `NBYTES` bytes after each header without
scanning for a separator at all.

`--with-header` is a deprecated alias for `--porcelain`: identical framing,
retained so existing scripts keep working. Prefer `--porcelain` in new
scripts.

## Rules the diff and commit forms obey

These rules are specific to `SYMBOL`-bearing records; `rgit languages
--porcelain` has no symbol, count, or ordering concept to share with them.

`SYMBOL` is empty for every row or record that owns no anchor. A non-empty
`SYMBOL` is always exactly the string a symbol anchor accepts back, so output
copy-pastes into the next invocation.

Binary entries use `-` for both counts, matching `git diff --numstat`.

**Ordering is alphabetical by path, then ascending by position within each
file** — source order, not alphabetical by symbol, so a file's own structure is
preserved. `rgit commit` lists its targets the same way regardless of the order
they were named. Output is therefore stable between runs on an unchanged tree,
and greppable. A file's `(unanchorable)` row sorts last, since it covers hunks
spread across the file rather than any one position.

A symbol that is wholly added or removed carries **one** blank separator line
with it, because that is what staging it actually moves — a top-level
declaration is spliced in with one blank line before it and excised with that
gap collapsed again. A container member carries none: members sit flush
against their siblings, separated by a single newline their own extent already
covers.

Where a formatter writes more than one blank line, the surplus belongs to
nobody and shows up as `(unanchorable)`. PEP 8 writes two between top-level
definitions, so adding a Python function reports the symbol plus one
unanchorable line — and staging the anchor alone really does leave exactly
that line behind. In Go and TypeScript, where one blank line is the whole
separator, there is no remainder and no such row.

**Every per-symbol row therefore agrees with `rgit commit --dry-run`, in every
language.** An `(unanchorable)` row is the one thing `--dry-run` has no
counterpart for, by definition: it is the part of the change no anchor will
stage. It appears only when that part is real.
