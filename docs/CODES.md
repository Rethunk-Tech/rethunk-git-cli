# Codes

Authoritative exit codes and machine-readable output. Flags:
[`USAGE.md`](USAGE.md).

## Exit codes

| Exit | Condition |
| --- | --- |
| 0 | Success (stderr warnings for unchanged targets allowed) |
| 1 | `diff` only, under `--exit-code`/`--quiet`: something is committable |
| 3 | Anchor unresolvable — missing in worktree and HEAD; candidates listed |
| 4 | Ambiguous anchor — candidates listed |
| 5 | One path named both as a path and as a symbol anchor |
| 6 | Normalized LSP ↔ tree-sitter extent mismatch (`commit` only) |
| 7 | Gitignored and untracked |
| 8 | Commit succeeded; `--push` failed |
| 9 | Unsupported language for a symbol anchor |
| 10 | Symbol anchor refused on a special path (`commit` only) |
| 11 | All named targets resolve but have no uncommitted changes |
| 12 | Symbol anchor on JSON/JSONC/JSON5/YAML/TOML (`commit` only) |
| 128 | Fatal git / system failure (hook rejection, GPG, `-C` unenterable) |
| 129 | Invalid usage |

128 and 129 follow git. 1 is git's `--exit-code` convention and is not a
named constant — it depends on a flag. Bindings: `internal/exitcode`.

Every command can produce 128 and 129: global `-C` is validated before
dispatch ([`USAGE.md`](USAGE.md#global-flags)).

### Exit 6 is `commit`'s alone

`diff` reports the same disagreement as a `[warning]` and still exits 0
(or 1 under `--exit-code`).

### Exit 12 is `commit`'s alone

`diff --sym`, `show`, `blame`, and `log` resolve structured-data anchors.
Only `commit` would splice a blob. Name the path.

### A missing cross-check is never a failure

No language server → `[ts-only]` on stderr and proceed
([`AGENTS.md`](../AGENTS.md#resolution-model)). `--quiet` suppresses
stdout, not diagnostics.

### The cross-check compares lines, not columns

Both sides reduce to a 0-based start/end line (`lineOf` in
`internal/resolve/crosscheck.go`). Same-line column disagreement passes:
anchors are whole declarations.

### `show` shares the anchor codes, plus one of git's own

3, 4, 9 as usual. `--source`: unknown revision is **128**; path absent at
a known revision is **3**. `cat-file` reports both as "no such object".
A plain path or no anchor: **129**.

### `blame` shares the anchor codes, not the staging ones

3, 4, 9. Staging codes (1, 5–8, 10–12) do not apply. Past resolution,
`git blame`'s exit folds into 128. `--source` as `show`. Combined with
`--follow-rename`, the walk starts at that revision.

### `log` shares the anchor codes too, resolved against `HEAD`

Same as `blame`, but against `HEAD`. `--source` as above. Path form with
no anchor: 3/4/9 do not apply. Neither path, date, nor `--follow`: 129.
`--follow` needs exactly one path. See
[`USAGE.md`](USAGE.md#log-by-date-and-path).

### `context` has no anchor to resolve at all

3, 4, 9 never apply. Staging codes do not. `git log` / `git diff` failure
folds into 128.

## Output records

`--porcelain` is tab-separated records, no header. `--json` is one object
with `"schema": 1` first, then the payload (`languages`, `checks`,
`records`, `commits`, or `symbols`). Mutually exclusive with `--porcelain`
on `languages`, `doctor`, `symbols`, and `log`. `context` has no
`--porcelain`; `--json` encodes the same stream. Errors under `--json` stay
text on stderr; the exit code is the verdict. Reordering or adding a
column is a breaking change.

### The three `--porcelain` dialects

| Dialect | Commands | Record separator | Shape |
| --- | --- | --- | --- |
| TSV + newline | `diff`, `commit`, `languages`, `doctor`, `log` | newline | `FIELD<TAB>FIELD...` (`context` is this shape with a type tag) |
| NUL-terminated | `symbols` | NUL | Same fields; a symbol may contain any byte but NUL |
| Passthrough git format | `blame` | git's own | `git blame --porcelain`, unmodified |

`show --porcelain` is length-framed `FILE:ANCHOR<TAB>NBYTES` + `NBYTES`
bytes.

### `rgit diff --porcelain`

```text
FILE<TAB>SYMBOL<TAB>STATUS<TAB>ADDED<TAB>DELETED
auth.go<TAB>ValidateToken<TAB>MOD<TAB>12<TAB>3
auth.go<TAB>oldHelper<TAB>DELETED<TAB>0<TAB>14
auth.go<TAB><TAB>UNANCHORABLE<TAB>2<TAB>0
newfile.go<TAB>@header<TAB>MOD<TAB>2<TAB>0
config.ini<TAB><TAB>UNTRACKED<TAB>4<TAB>0
script.sh<TAB><TAB>MODE<TAB>0<TAB>0
logo.png<TAB><TAB>BINARY<TAB>-<TAB>-
```

| Token | Means |
| --- | --- |
| `MOD` | Changed symbol, or a changed file with no grammar |
| `DELETED` | In `HEAD`, gone from the worktree |
| `UNANCHORABLE` | Hunks no symbol owns |
| `UNTRACKED` | Untracked and unattributable (binary, or no grammar) |
| `MODE` | Permission change, no content edit |
| `BINARY` | Both counts `-` |

An untracked file with a grammar attributes per symbol as `MOD`.
`UNTRACKED` is the collapsed fallback.

### `rgit commit --porcelain`

```text
H<TAB>SHA
FILE<TAB>SYMBOL<TAB>ADDED<TAB>DELETED
H<TAB>a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2
auth.go<TAB>ValidateToken<TAB>12<TAB>3
package.json<TAB><TAB>4<TAB>1
```

`H` is the full commit object id after every successful real commit.
`--dry-run --porcelain` emits target rows only. `--allow-empty` still
emits `H`.

A pathspec produces **one record per file it stages**. A directory lists
each file; untracked files under it are listed; a rename is listed at its
new path; a binary has zero counts; a pathspec that matches nothing keeps
one record naming the pathspec. No `STATUS` column — unchanged targets
are omitted (stderr warning). `--porcelain` replaces `git commit`'s
summary.

### `rgit languages --porcelain`

```text
NAME<TAB>EXTENSIONS<TAB>GATED<TAB>CROSS-CHECK
css<TAB>.css<TAB>0<TAB>wired
go<TAB>.go<TAB>0<TAB>wired
html<TAB>.html .htm<TAB>0<TAB>wired
json<TAB>.json<TAB>0<TAB>wired
json5<TAB>.json5<TAB>0<TAB>ts-only
jsonc<TAB>.jsonc<TAB>0<TAB>wired
markdown<TAB>.md .markdown<TAB>0<TAB>wired
python<TAB>.py .pyi<TAB>0<TAB>wired
scss<TAB>.scss<TAB>0<TAB>wired
shell<TAB>.sh .bash<TAB>0<TAB>wired
sql<TAB>.sql<TAB>1<TAB>ts-only
toml<TAB>.toml<TAB>0<TAB>ts-only
tsx<TAB>.tsx .jsx .js .mjs .cjs<TAB>0<TAB>wired
typescript<TAB>.ts .mts .cts<TAB>0<TAB>wired
yaml<TAB>.yaml .yml<TAB>0<TAB>wired
zsh<TAB>.zsh .zshrc .zshenv .zprofile<TAB>0<TAB>wired
```

One record per compiled grammar, sorted by `NAME`. Sample is
`-tags rgit_sql`; a plain build has no `sql` row.

`EXTENSIONS`: leading-dot, space-joined. `GATED` is `1` or `0` on every
row. `CROSS-CHECK` is `wired` or `ts-only` (TOML, SQL, JSON5) — design-time
wiring, not reachability. `--in-repo` narrows rows, not shape.
[`USAGE.md`](USAGE.md#languages).

### `rgit languages --json`

```json
{
  "schema": 1,
  "languages": [
    {
      "name": "go",
      "extensions": ".go",
      "gated": "0",
      "cross-check": "wired"
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `name` | Porcelain `NAME` |
| `extensions` | Porcelain `EXTENSIONS` |
| `gated` | `1` or `0` |
| `cross-check` | `wired` or `ts-only` |

`--json` with `--porcelain` is 129.

### `rgit doctor --porcelain`

```text
env<TAB>git<TAB>ok<TAB>/usr/bin/git
env<TAB>git version<TAB>ok<TAB>2.43.0
env<TAB>tree-sitter CLI<TAB>MISSING<TAB>optional -- only needed to rebuild with SQL support, see docs/INSTALL.md § SQL support
server<TAB>gopls (go)<TAB>ok<TAB>/home/user/go/bin/gopls (reachable)
server<TAB>vtsls (typescript, tsx)<TAB>MISSING<TAB>not on PATH -- see docs/INSTALL.md § Language servers
```

| Field | Meaning |
| --- | --- |
| `KIND` | `env` or `server` |
| `NAME` | Check name; a server includes its languages |
| `STATUS` | `ok` or `MISSING` — `--deep` never changes this column |
| `DETAIL` | Path when `ok`, note when `MISSING`. `--deep` appends `(reachable)` or `(degraded -- handshake timed out or unanswered)` |

Grammars stay in `rgit languages --porcelain`. Missing git is still 128;
a missing server or CLI is still 0.

### `rgit doctor --json`

```json
{
  "schema": 1,
  "checks": [
    {
      "kind": "env",
      "name": "git",
      "status": "ok",
      "detail": "/usr/bin/git"
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `kind` | `env` or `server` |
| `name` | Porcelain `NAME` |
| `status` | `ok` or `MISSING` |
| `detail` | Porcelain `DETAIL`, including `--deep` suffixes |

`--json` with `--porcelain` is 129. Missing git is still 128; the object
is still written.

### `rgit blame --porcelain`

Passthrough to `git blame --porcelain`. See `git help blame`.

### `rgit context`

Always this stream — no aligned default, no `--porcelain`. Why:
[`AGENTS.md`](../AGENTS.md).

```text
B<TAB>main<TAB>origin/main<TAB>0<TAB>2
S<TAB>merge
W<TAB>ts-only
W<TAB>warning<TAB>extent disagreement reported on stderr
F<TAB>auth.go<TAB>ValidateToken<TAB>MOD<TAB>12<TAB>3
F<TAB>config.ini<TAB><TAB>UNTRACKED<TAB>4<TAB>0
T<TAB>auth.go<TAB>both
T<TAB>config.ini<TAB>untracked
C<TAB>a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2<TAB>fix(auth): reject expired tokens
C<TAB>9e8f7d6c5b4a9e8f7d6c5b4a9e8f7d6c5b4a9e8f<TAB>feat(auth): add ValidateToken
X<TAB>TRUNCATED<TAB>3
```

| Type | Fields after the type tag | Means |
| --- | --- | --- |
| `B` | `BRANCH`, `UPSTREAM`, `AHEAD`, `BEHIND` | Current branch. Empty `UPSTREAM` and `0`/`0` with no upstream. Absent on unborn; replaced by `H` when detached |
| `H` | `SHA` | Detached HEAD; absent on unborn |
| `S` | `OP` | `merge`, `cherry-pick`, `revert`, `rebase`, or `bisect` |
| `W` | `ts-only` | Cross-check wanted, no live server. `[ts-only]` also on stderr |
| `W` | `stash` | `refs/stash` exists |
| `W` | `sparse` | `core.sparseCheckout` is true |
| `W` | `warning`, `TEXT` | Non-fatal diff warning without the `[warning]` prefix |
| `F` | `FILE`, `SYMBOL`, `STATUS`, `ADDED`, `DELETED` | One `rgit diff --porcelain` row |
| `T` | `FILE`, `WHERE` | Per `F` file, in order: `staged`, `unstaged`, `both`, `untracked` |
| `C` | `HASH`, `SUBJECT` | Recent commits, newest first, last 20 |
| `X` | `TRUNCATED`, `COUNT` | Last when present: rows withheld for the 16 KiB budget |

Order: `B` or `H`, then `S`, then `W`, then `F`, then `T`, then `C`, then
`X`. Budget: [`USAGE.md`](USAGE.md#context). Records drop whole.

### `rgit context --json`

```json
{
  "schema": 1,
  "records": [
    {
      "ahead": "0",
      "behind": "2",
      "branch": "main",
      "type": "B",
      "upstream": "origin/main"
    },
    {
      "kind": "warning",
      "text": "extent disagreement reported on stderr",
      "type": "W"
    },
    {
      "added": "12",
      "deleted": "3",
      "file": "auth.go",
      "status": "MOD",
      "symbol": "ValidateToken",
      "type": "F"
    },
    {
      "file": "auth.go",
      "type": "T",
      "where": "both"
    },
    {
      "hash": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
      "subject": "fix(auth): reject expired tokens",
      "type": "C"
    }
  ]
}
```

Same order as the text stream, after the 16 KiB cap. Empty columns are
`""`.

| `type` | Fields |
| --- | --- |
| `B` | `branch`, `upstream`, `ahead`, `behind` |
| `H` | `sha` |
| `S` | `op` |
| `W` | `kind` (`ts-only`, `stash`, `sparse`, or `warning`). `text` only for `warning` |
| `F` | `file`, `symbol`, `status`, `added`, `deleted` |
| `T` | `file`, `where` |
| `C` | `hash`, `subject` |
| `X` | `count` (`TRUNCATED` is the label, not a field) |

`ahead`, `behind`, `added`, `deleted`, and `count` are strings. Any other
argument is 129.

### `rgit log --porcelain`

```text
HASH<TAB>SUBJECT
a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2<TAB>fix(auth): reject expired tokens
9e8f7d6c5b4a9e8f7d6c5b4a9e8f7d6c5b4a9e8f<TAB>feat(auth): add ValidateToken
```

Full object id. Newest first. Mutually exclusive with `-p`/`--patch`.
`rgit log -p` is git's own `git log -L` output.

### `rgit log --json`

```json
{
  "schema": 1,
  "commits": [
    {
      "hash": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
      "subject": "fix(auth): reject expired tokens"
    }
  ]
}
```

Empty history is `"commits": []`. `--json` with `--porcelain` or `-p` is
129.

### `rgit symbols --with-lines`

```text
START,END<TAB>SYMBOL
1,3 @imports
12,27 ValidateToken
```

Source order. `START,END` is git's 1-based inclusive `-L` range — the same
range `blame` and `log` use. `--porcelain` is NUL-terminated; fields
unchanged. `SYMBOL` is last (unbounded); split on the first tab.

### `rgit symbols --json`

```json
{
  "schema": 1,
  "symbols": [
    {
      "file": "auth.go",
      "start": "12",
      "end": "27",
      "symbol": "ValidateToken"
    }
  ]
}
```

`file` when `--with-filename` or several files. `start`/`end` with
`--with-lines`, as strings. `--json` with `--porcelain` is 129.

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

Every extent: `FILE:ANCHOR<TAB>NBYTES` then exactly `NBYTES` bytes.
Without the flag, one anchor is raw bytes. `--with-header` is a deprecated
alias.

## Rules the diff and commit forms obey

`SYMBOL` is empty when the row owns no anchor. A non-empty `SYMBOL` is
exactly the string an anchor accepts back.

Binary counts are `-`, matching `git diff --numstat`.

**Order: alphabetical by path, then source position.** `commit` lists
targets that way regardless of name order. `(unanchorable)` sorts last.

A wholly added or removed top-level symbol carries **one** blank separator
line. A container member carries none. Surplus blank lines are
`(unanchorable)` — PEP 8's two blanks between top-level defs leave one
behind if you stage the anchor alone.

Every per-symbol row agrees with `rgit commit --dry-run`. `(unanchorable)`
has no `--dry-run` counterpart: it is the part no anchor stages.
