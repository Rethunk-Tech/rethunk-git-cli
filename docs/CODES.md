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
| 10 | Symbol anchor refused on a special path (symlink, gitlink, binary) |
| 11 | All named targets resolve but have no uncommitted changes |
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
it dispatches too, so a broken `-C` cannot be silent on one command and
fatal on the next.

The numeric bindings live in `internal/exitcode`, which spells no meaning of
its own — the constant names carry it, and this table defines it.

### Exit 6 is `commit`'s alone

`rgit diff` reports the identical disagreement as a `[warning]` on stderr and
still exits 0 (or 1 under `--exit-code`). A diff is a read-only report, and the
point of surfacing it there is that the caller learns of it while reading the
diff rather than mid-commit.

### A missing cross-check is never a failure

When no language server is reached, any command that runs the diff
cross-check — `diff`, `commit`, and `context`, which composes `diff`'s own
default scope — prints `[ts-only]` on stderr and proceeds — degraded
resolution is normal, not an error
([`AGENTS.md`](../AGENTS.md#resolution-model)). `rgit diff --quiet` still
prints it, since `--quiet` suppresses the report on stdout, not
diagnostics.

### The cross-check compares lines, not columns

Both sides of the exit-6 comparison reduce to a 0-based start/end line pair
before comparing (`internal/resolve/crosscheck.go`'s `lineOf`); a language
server and tree-sitter agreeing on every line but disagreeing on a column
within one passes silently. Deliberate, not a gap: the anchors this
resolver stages are whole declarations, never sub-line ranges, so a
same-line disagreement has nothing narrower for either side to report
against.

### `blame` shares the anchor codes, not the staging ones

`rgit blame FILE:SYMBOL` resolves its one anchor exactly like `commit` and
`diff --sym` do, so 3 (unresolvable), 4 (ambiguous), and 9 (unsupported
language) mean the same thing there. It never stages or commits anything, so
1, 5, 6, 7, 8, 10, and 11 do not apply — a failure past resolution is `git
blame`'s own exit, folded into 128 the same way any other unexpected git
failure is.

### `log` shares the anchor codes too, resolved against `HEAD`

`rgit log FILE:SYMBOL` resolves its one anchor the same way `blame` does —
3 (unresolvable), 4 (ambiguous), and 9 (unsupported language) mean the same
thing — except against `HEAD`'s own blob rather than the worktree, since
history is a question about what has already been committed. It never
stages or commits anything either, so the same 1, 5, 6, 7, 8, 10, and 11
exclusions apply, and a failure past resolution is `git log`'s own exit,
folded into 128.

### `context` has no anchor to resolve at all

`rgit context` names no symbol, so 3, 4, and 9 never apply either. It never
stages or commits anything, so the same 1, 5, 6, 7, 8, 10, and 11 exclusions
as `blame` and `log` hold. A failure reaching `git log` or `git diff`
underneath it is folded into 128, same as everywhere else.

## Output records

The record formats defined here are for `--porcelain` output only; a
command's own default is git's or `rgit`'s aligned human-readable layout
(`diff`, `commit`, `blame`, `log`, `languages`). `--porcelain` replaces
that with stable tab-separated records, no header.

This is not a hypothetical contract: `rgit completion`'s own shell completion
scripts (`internal/app/completion.go`) shell out to `rgit diff --porcelain`
and cut its `FILE` and `SYMBOL` columns by position with `awk -F'\t'` to offer
symbol names after `FILE:`. Reordering or adding a column here is a breaking
change for that consumer, not just for external scripts.

### `rgit diff --porcelain`

```text
FILE<TAB>SYMBOL<TAB>STATUS<TAB>ADDED<TAB>DELETED
auth.go<TAB>ValidateToken<TAB>MOD<TAB>12<TAB>3
auth.go<TAB>oldHelper<TAB>DELETED<TAB>0<TAB>14
auth.go<TAB><TAB>UNANCHORABLE<TAB>2<TAB>0
newfile.go<TAB><TAB>UNTRACKED<TAB>15<TAB>0
script.sh<TAB><TAB>MODE<TAB>0<TAB>0
logo.png<TAB><TAB>BINARY<TAB>-<TAB>-
```

`STATUS` is one of:

| Token | Means |
| --- | --- |
| `MOD` | A changed symbol, or a changed file whose language has no grammar |
| `DELETED` | A symbol present in `HEAD` and gone from the worktree |
| `UNANCHORABLE` | Hunks in a supported file that no symbol owns |
| `UNTRACKED` | A file git does not track; its symbols are never split out |
| `MODE` | A permission change with no content edit |
| `BINARY` | A binary file; both counts are `-` |

### `rgit commit --porcelain`

```text
FILE<TAB>SYMBOL<TAB>ADDED<TAB>DELETED
auth.go<TAB>ValidateToken<TAB>12<TAB>3
package.json<TAB><TAB>4<TAB>1
```

**A pathspec produces one record per file it stages, not one for the
pathspec.** Naming a directory stages everything under it, so `rgit commit
apps/auth` lists `apps/auth/a.go`, `apps/auth/b.go` and so on, each with its
own counts — the same breakdown `rgit diff` gives, rather than a single total
that says something moved without saying what. Untracked files under that
pathspec are listed too, since `git add` stages them as well, and a renamed
file is listed at its new path. A binary file is listed with zero counts: it is
being staged, and git reports no line counts for it. A pathspec that matches
nothing keeps one record naming the pathspec itself, so `git add`'s own "did
not match any files" is still what answers for it.

There is no `STATUS` column: an unchanged target is omitted from the listing
entirely (it gets its own stderr warning instead), so every record would carry
the same value. Records are identical for `--dry-run` and for the commit it
previews, and `--porcelain` replaces `git commit`'s own summary rather than
adding to it — exactly as `git commit --porcelain` does.

Because unchanged targets are omitted, `--porcelain --allow-empty` writes **no
records at all** while still creating a commit and exiting 0: nothing was
staged, so there is nothing to report. A caller that needs to distinguish that
from "no commit happened" should read the exit code, or `git rev-parse HEAD`
before and after — empty output on its own does not mean nothing was done.

### `rgit languages --porcelain`

```text
NAME<TAB>EXTENSIONS<TAB>GATED
css<TAB>.css<TAB>0
go<TAB>.go<TAB>0
html<TAB>.html .htm<TAB>0
json<TAB>.json<TAB>0
markdown<TAB>.md .markdown<TAB>0
python<TAB>.py .pyi<TAB>0
shell<TAB>.sh .bash<TAB>0
sql<TAB>.sql<TAB>1
toml<TAB>.toml<TAB>0
tsx<TAB>.tsx .jsx .js .mjs .cjs<TAB>0
typescript<TAB>.ts .mts .cts<TAB>0
yaml<TAB>.yaml .yml<TAB>0
```

Sampled from a `-tags rgit_sql` build; a plain build has no `sql` row (see
[`INSTALL.md`](INSTALL.md#sql-support)). One record per grammar compiled into
this binary, sorted alphabetically by `NAME`. `EXTENSIONS` is every extension the grammar claims, leading dot
included on each, joined with a single space — unambiguous, since a real
extension is always `.something` and never itself contains whitespace.
`GATED` is `1` when the grammar exists in this binary only because a build
tag selected it (SQL alone, `-tags rgit_sql`; see
[`INSTALL.md`](INSTALL.md#sql-support)) and `0` otherwise, present on every
row rather than only the gated ones, so a reader always gets a definite
answer instead of inferring "not gated" from an absent column.

There is no row at all for a grammar this build was not compiled with: a
plain build's records have no `sql` line, matching `rgit languages`'s own
human output and `rgit --version`'s second line.

### `rgit blame --porcelain`

Not a new record shape: `--porcelain` passes straight through to git's own
`git blame --porcelain` output, unmodified. See `git help blame` for that
format — rewrapping it in a second, rgit-specific shape would be exactly the
kind of duplication this file exists to avoid, for a fact git already
establishes on its own.

### `rgit context`

Unlike every other command in this file, `rgit context` has no aligned
human default to alternate with: its one output shape is always this
tab-separated record stream, no header, no `--porcelain` flag to ask for it
— see [`specs/design.md`](../specs/design.md#commands) for why.

```text
C<TAB>a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2<TAB>fix(auth): reject expired tokens
C<TAB>9e8f7d6c5b4a9e8f7d6c5b4a9e8f7d6c5b4a9e8f<TAB>feat(auth): add ValidateToken
F<TAB>auth.go<TAB>ValidateToken<TAB>MOD<TAB>12<TAB>3
F<TAB>newfile.go<TAB><TAB>UNTRACKED<TAB>15<TAB>0
X<TAB>TRUNCATED<TAB>3
```

Three record types, distinguished by the first field:

| Type | Fields after the type tag | Means |
| --- | --- | --- |
| `C` | `HASH`, `SUBJECT` | One recent commit, newest first, bounded to the last 20 |
| `F` | `FILE`, `SYMBOL`, `STATUS`, `ADDED`, `DELETED` | One `rgit diff --porcelain` row, identical fields — `STATUS` is the same six tokens § Output records defines above |
| `X` | `TRUNCATED`, `COUNT` | At most one, always last: `COUNT` records were withheld to hold the 16 KiB byte budget |

`C` records always precede `F` records, and an `X` record — when present —
is always the last line. See [`USAGE.md`](USAGE.md#context) for the byte
budget and [`../specs/design.md`](../specs/design.md#commands) for why it is
16 KiB and what happens at the boundary.

### `rgit log --porcelain`

```text
HASH<TAB>SUBJECT
a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2<TAB>fix(auth): reject expired tokens
9e8f7d6c5b4a9e8f7d6c5b4a9e8f7d6c5b4a9e8f<TAB>feat(auth): add ValidateToken
```

One record per commit whose own diff touched the named symbol's current
extent, newest first — the same ordering `git log`'s own default gives.
`HASH` is the full commit object id, never abbreviated (unlike the aligned
default's `<abbrev-hash> <subject>`, which is for a human to read, not to
paste elsewhere). Patch-free: this is the one record shape in this file that
is never emitted alongside `-p`/`--patch`, since asking for both would mean
asking for a record format and a patch dump at once. `rgit log -p` instead
prints git's own `git log -L` output unmodified, the same "pass through
git's own format rather than inventing a second one" choice `rgit blame
--porcelain` already makes.

## Rules the diff and commit forms obey

These rules are specific to `SYMBOL`-bearing records — `rgit languages
--porcelain` has no symbol, count, or ordering concept to share with them,
and its own rules are stated in full above.

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
