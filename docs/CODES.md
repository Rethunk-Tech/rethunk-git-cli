# Codes

Authoritative exit codes and machine-readable output. Flags:
[`USAGE.md`](USAGE.md).

## Exit codes

| Exit | Condition |
| --- | --- |
| 0 | Success (stderr warnings for unchanged targets allowed). `land --dry-run` always exits 0 once its arguments parse; skips show in the plan, not the exit code |
| 1 | `diff` only, under `--exit-code`/`--quiet`: something is committable. `land` (plain and `--sha`): a repo is red, including a red gate or push; `--branch`: the push is red or origin moved (a red gate is 3). `find`: no match |
| 2 | `land --branch`: the rebase conflicted |
| 3 | Anchor unresolvable — missing in worktree and HEAD; candidates listed. `land --branch`: the gate was red |
| 4 | Ambiguous anchor — candidates listed. `land` (plain, `--sha`, `--branch --push`): a PR is neither queued nor set to auto-merge, or its state is unknown |
| 5 | One path named both as a path and as a symbol anchor. `land`: `--install` (or a catch-up install) failed; also `land --branch --install` |
| 6 | Normalized LSP ↔ tree-sitter extent mismatch (`commit` only) |
| 7 | Gitignored and untracked |
| 9 | Unsupported language for a symbol anchor |
| 10 | Symbol anchor refused on a special path (`commit` only) |
| 11 | All named targets resolve but have no uncommitted changes |
| 12 | Symbol anchor on JSON/JSONC/JSON5/YAML/TOML (`commit` only) |
| 13 | `land --branch` (without `--push`) could not fast-forward: the checkout's own uncommitted edits touch files the branch changes. Retry later |
| 14 | `land` skipped a repo that needs action: dirty tracked files (`--branch`: the branch's worktree is dirty, or the gate edited tracked files), wrong branch, behind origin with a conflicting rebase, no upstream, not a git repo, or an umbrella held back by a red submodule repo. Nothing to land (not ahead, already upstream) stays 0. `--json` still prints status and detail |
| 128 | Fatal git / system failure (hook rejection, GPG, `-C` unenterable). `land --branch` setup: not a git repo, no default branch, worktree add failed; later: cache or log write failed, fast-forward refused without overlap, HEAD not at the branch tip afterwards |
| 129 | Invalid usage. `land --branch` setup: not the main checkout, branch missing, main checkout on another branch |
| 130 | `land` stopped by SIGINT or SIGTERM; the line names the step (gate, git, gh, or wait) |

128 and 129 follow git. `diff`'s 1 is git's `--exit-code` convention and is not
a named constant — it depends on a flag. Bindings: `internal/exitcode`.

Every command can produce 128 and 129: global `-C` is validated before
dispatch ([`USAGE.md`](USAGE.md#global-flags)).

### Exit 6 is `commit`'s alone

`diff` does not cross-check, so it never reports the disagreement.

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
with `"schema": 1` first, then the payload (`diffs`, `sha`/`targets`,
`languages`, `checks`, `records`, `commits`, `symbols`, or `hits`). Mutually
exclusive with `--porcelain` on `diff`, `commit`, `languages`, `doctor`,
`symbols`, and `log`. `context` has no `--porcelain`; `--json` encodes the
same stream. Errors under `--json` stay text on stderr; the exit code is
the verdict. Reordering or adding a column is a breaking change.

### The three `--porcelain` dialects

| Dialect | Commands | Record separator | Shape |
| --- | --- | --- | --- |
| TSV + newline | `diff`, `commit`, `languages`, `doctor`, `log` | newline | `FIELD<TAB>FIELD...` (`context` is this shape with a type tag) |
| NUL-terminated | `symbols` | NUL | Same fields; a symbol may contain any byte but NUL |
| Passthrough git format | `blame` | git's own | `git blame --porcelain`, unmodified |

`show --porcelain` is length-framed `FILE:ANCHOR<TAB>NBYTES` + `NBYTES`
bytes. `show` has no `--json`; `--porcelain` is its machine form.

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

### `rgit diff --json`

```json
{
  "schema": 1,
  "diffs": [
    {
      "file": "auth.go",
      "symbol": "ValidateToken",
      "status": "MOD",
      "added": "12",
      "deleted": "3"
    },
    {
      "file": "auth.go",
      "symbol": "",
      "status": "UNANCHORABLE",
      "added": "2",
      "deleted": "0"
    }
  ]
}
```

Same rows as `--porcelain`, same order. Empty `SYMBOL` is `""`. STATUS
tokens are unchanged. Empty report is `"diffs": []`. `--json` with
`--porcelain`, `-p`/`--patch`, or `--quiet` is 129. `--exit-code` still
exits 1 when committable.

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

### `rgit commit --json`

```json
{
  "schema": 1,
  "sha": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
  "targets": [
    {
      "file": "auth.go",
      "symbol": "ValidateToken",
      "added": "12",
      "deleted": "3"
    },
    {
      "file": "package.json",
      "symbol": "",
      "added": "4",
      "deleted": "1"
    }
  ],
  "left": {
    "dirty": ["README.md"],
    "upstream": "origin/main",
    "ahead": 1
  }
}
```

`left` is what remains after the commit: `dirty` (paths), `upstream`
(omitted with none) and `ahead` (a number). It is absent when the commit
leaves the state unreported (`--dry-run`, `--stage-only`).

`sha` is the full commit object id porcelain would print as `H`. It is
omitted when porcelain would print no `H` (`--dry-run`, `--stage-only`).
`--allow-empty` still names `sha` with `"targets": []`. No `status`
field. `--json` with `--porcelain` or `-q`/`--quiet` is 129.

### `rgit languages --porcelain`

```text
NAME<TAB>EXTENSIONS<TAB>GATED<TAB>CROSS-CHECK
css<TAB>.css<TAB>0<TAB>wired
go<TAB>.go<TAB>0<TAB>wired
html<TAB>.html .htm<TAB>0<TAB>wired
json<TAB>.json<TAB>0<TAB>wired
json5<TAB>.json5<TAB>0<TAB>ts-only
jsonc<TAB>.jsonc<TAB>0<TAB>wired
markdown<TAB>.md .markdown .mdx<TAB>0<TAB>wired
python<TAB>.py .pyi<TAB>0<TAB>wired
rust<TAB>.rs<TAB>0<TAB>wired
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
env<TAB>gate<TAB>ok<TAB>/home/user/go/bin/gate
env<TAB>gh<TAB>ok<TAB>/usr/bin/gh (authenticated)
env<TAB>tree-sitter CLI<TAB>MISSING<TAB>optional -- only needed to rebuild with SQL support, see docs/INSTALL.md § SQL support
server<TAB>gopls (go)<TAB>ok<TAB>/home/user/go/bin/gopls (reachable)
server<TAB>vtsls (typescript, tsx)<TAB>MISSING<TAB>not on PATH -- see docs/INSTALL.md § Language servers
```

| Field | Meaning |
| --- | --- |
| `KIND` | `env` or `server` |
| `NAME` | Check name; a server includes its languages |
| `STATUS` | `ok` or `MISSING` — `--deep` never changes this column |
| `DETAIL` | Path when `ok`, note when `MISSING`. `gh` appends ` (authenticated)` or ` (not authenticated -- rgit land cannot read branch rules; run gh auth login)`. `--deep` appends `(reachable)` or `(degraded -- handshake timed out or unanswered)` |

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
W<TAB>stash
W<TAB>warning<TAB>non-fatal diff diagnostic
F<TAB>auth.go<TAB>ValidateToken<TAB>MOD<TAB>12<TAB>3
F<TAB>config.ini<TAB><TAB>UNTRACKED<TAB>4<TAB>0
T<TAB>auth.go<TAB>both
T<TAB>config.ini<TAB>untracked
C<TAB>a1b2c3d4e5f6<TAB>fix(auth): reject expired tokens
C<TAB>9e8f7d6c5b4a<TAB>feat(auth): add ValidateToken
X<TAB>TRUNCATED<TAB>3
```

| Type | Fields after the type tag | Means |
| --- | --- | --- |
| `B` | `BRANCH`, `UPSTREAM`, `AHEAD`, `BEHIND` | Current branch. Empty `UPSTREAM` and `0`/`0` with no upstream. Absent on unborn; replaced by `H` when detached |
| `H` | `SHA` | Detached HEAD; absent on unborn |
| `B`/`H` with `--brief` | the record's fields, then `DIRTY`, `UNTRACKED` | `--brief` only: dirty and untracked file counts appended (json: `dirty`, `untracked`). Unborn prints an empty `B` with `0`/`0`; detached prints `H` |
| `S` | `OP` | `merge`, `cherry-pick`, `revert`, `rebase`, or `bisect` |
| `W` | `stash` | `refs/stash` exists |
| `W` | `sparse` | `core.sparseCheckout` is true |
| `W` | `warning`, `TEXT` | Non-fatal diff warning without the `[warning]` prefix |
| `F` | `FILE`, `SYMBOL`, `STATUS`, `ADDED`, `DELETED` | One `rgit diff --porcelain` row |
| `T` | `FILE`, `WHERE` | Per `F` file, in order: `staged`, `unstaged`, `both`, `untracked` |
| `C` | `HASH`, `SUBJECT` | Recent commits, newest first, last 20. Text HASH is 12 hex characters; `--json` carries the full 40 |
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
| `W` | `kind` (`stash`, `sparse`, or `warning`). `text` only for `warning` |
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
1,3	@imports
12,27	ValidateToken
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

### `rgit find --json`

```json
{
  "schema": 1,
  "hits": [
    {
      "file": "auth.go",
      "start": 12,
      "end": 27,
      "kind": "function",
      "name": "ValidateToken",
      "body": "func ValidateToken(tok string) error {\n ... \n}"
    }
  ]
}
```

`body` is present on the top `--bodies` hits (default 3) and omitted for
the rest and under `--names`. `start`/`end` are 1-based inclusive line
numbers.

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
Without the flag, one anchor is raw bytes.

### `rgit land --json`

One object, `{"repos": [...]}`, in run order. It has no `schema` field.

```json
{
  "repos": [
    { "path": "/src/a", "status": "pushed", "sha": "a1b2c3d4e5f6" },
    { "path": "/src/b", "status": "pr", "url": "https://github.com/o/b/pull/7", "state": "open" },
    { "path": "/src/c", "status": "red", "detail": "gate failed", "log": "/var/tmp/gate/c.log" },
    { "path": "/src/d", "status": "skipped", "detail": "not ahead" }
  ]
}
```

`status` is `pushed`, `pr`, `red` or `skipped`. `url`, `state`, `detail`,
`sha`, `log` and `watch` appear only when set. With `--dry-run` each repo
is `{path, order, status, detail, route, gate, ahead}` instead: `status`
is `would-land` or `skipped`, and `ahead` is the array of commit lines
that would land.

### `rgit land --branch BRANCH --json`

One object, also on failure: `{"repo", "branch", "sha", "log", "route", "code"}`.
`repo` is the absolute path of the repository, `route` is `local` with
`--no-push` or the landing route otherwise, `log` is the gate log, and `code`
is the exit code. `sha`, `log` and `route` are empty strings when the land
stopped before them.

## Rules the diff and commit forms obey

`SYMBOL` is empty when the row owns no anchor. A non-empty `SYMBOL` is
exactly the string an anchor accepts back.

In `diff` records binary counts are `-`, matching `git diff --numstat`; `commit` records print `0`/`0`.

**Order: alphabetical by path, then source position.** `commit` lists
targets that way regardless of name order. `(unanchorable)` sorts last.

A wholly added or removed top-level symbol carries **one** blank separator
line. A container member carries none. Surplus blank lines are
`(unanchorable)` — PEP 8's two blanks between top-level defs leave one
behind if you stage the anchor alone.

Every per-symbol row agrees with `rgit commit --dry-run`. `(unanchorable)`
has no `--dry-run` counterpart: it is the part no anchor stages.
