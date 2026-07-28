# Usage

Command reference for `rgit`. For the reasoning behind these choices see
[`specs/design.md`](../specs/design.md); for anchor syntax see
[`ANCHORS.md`](ANCHORS.md).

## Commands

```console
$ rgit diff
  auth.go      ValidateToken       +12/-3
               oldHelper           DELETED  +0/-14
               @imports            +1/-0
               (unanchorable)      +2/-0   -> use --file auth.go
  README.md    (no symbols)        +2/-0
  newfile.go   (untracked)         +15/-0  -> use --sym newfile.go:NewFunc or --file newfile.go
  script.sh    (mode 644->755)     +0/-0   -> use rgit commit script.sh
  logo.png     (binary)            -/-

$ rgit commit -m "auth: reject expired tokens" \
    auth.go:ValidateToken auth.go:@imports
```

## Argument shape

Positional arguments carry everything: pathspecs, revisions, and symbol anchors.
`--file` and `--sym` exist as explicit equivalents for scripting and
disambiguation, never as requirements.

All git pathspec magic is **leading**-colon (`:(exclude)`, `:(glob)`, `:/`). An
*interior* colon is therefore free for `FILE:NAME` and needs no flag.

Resolution precedence, first match wins:

| # | Test | Result |
| --- | --- | --- |
| 1 | Appears after `--` | Pathspec, always |
| 2 | Starts with `:` | Git pathspec magic, passed through verbatim |
| 3 | *(`diff` only)* resolves via `git rev-parse --verify` | Revision or `rev:path` blob reference |
| 4 | Names a path existing in the worktree or HEAD | Pathspec |
| 5 | Splits at the last `:` into an existing path + a name | Symbol anchor |
| 6 | None of the above | Error listing each interpretation tried |

No escaping is ever needed. `src/notes:draft.md` is a legal path, so rule 4
claims it; `auth.go:ValidateToken` names nothing, so rule 5 splits it. Use
`--sym` or `--file` to force the reading when a repo genuinely has both.

**Paths are relative to the directory you run in, not the repository root** —
git's own rule. In `pkg/deep`, `rgit commit a.go` stages `pkg/deep/a.go`, and
naming `pkg/deep/a.go` from there looks for `pkg/deep/pkg/deep/a.go` and finds
nothing, exactly as `git add` behaves. Leading-colon magic is the exception git
already defines: `:/` and `:(top)` are root-relative wherever you stand.
Output is always root-relative, matching `git diff --numstat`.

Files and symbols mix freely in one invocation:

```console
rgit commit -m "chore: bump deps" package.json bun.lock
rgit commit -m "fix(auth): reject expired" auth.go:ValidateToken auth.go:@imports
rgit commit -m "docs: note expiry" README.md auth.go:ValidateToken
rgit diff main...HEAD -- src/
```

Pathspecs are passed to git verbatim, so every pathspec form works:

```console
rgit commit -m "chore: format" 'src/*.go'
rgit commit -m "chore: sweep" . ':(exclude)docs/*'
rgit diff ':/src'
```

## Diff scope

`rgit diff` with no arguments shows **everything committable**: staged +
unstaged versus `HEAD`, plus untracked files. That is deliberately
`git diff HEAD` ∪ untracked, **not** `git diff` — the default answers "what
would `rgit commit` pick up". Narrower scopes use git's own flag names:

| Scope | Flag | Git equivalent |
| --- | --- | --- |
| Everything committable (default) | — | `git diff HEAD` + untracked |
| Unstaged only | `--unstaged` | `git diff` |
| Staged only | `--staged` / `--cached` | `git diff --staged` |
| Revision range | positional or `--range` | `git diff A..B` / `A...B` |

`--sym` and `--file` filter output to specific targets; when filtered by
`--sym`, `(unanchorable)` hunks in that file are omitted.

On a repository with no commits yet there is no `HEAD` to compare against, and
`git diff HEAD` fails outright. The default scope falls back to the empty tree,
so everything staged or untracked lists as an addition — `rgit diff` answers
the same question in a fresh `git init` that it does anywhere else, and
`rgit commit` writes the root commit.

## Output

Plain text only in v1. The default is the aligned layout shown above.
`--porcelain` emits stable tab-separated records:

```text
FILE<TAB>SYMBOL<TAB>STATUS<TAB>ADDED<TAB>DELETED
auth.go<TAB>ValidateToken<TAB>MOD<TAB>12<TAB>3
auth.go<TAB>oldHelper<TAB>DELETED<TAB>0<TAB>14
auth.go<TAB><TAB>UNANCHORABLE<TAB>2<TAB>0
newfile.go<TAB><TAB>UNTRACKED<TAB>15<TAB>0
script.sh<TAB><TAB>MODE<TAB>0<TAB>0
logo.png<TAB><TAB>BINARY<TAB>-<TAB>-
```

Binary entries use `-` for both counts, matching `git diff --numstat`. Symbol
labels in both forms match exact `FILE:NAME` syntax for copy-paste.

**Ordering is alphabetical by path, then ascending by position within each
file** — source order, not alphabetical by symbol, so a file's own structure is
preserved. `rgit commit --dry-run` lists its targets the same way regardless of
the order they were named. Output is therefore stable between runs on an
unchanged tree, and greppable. A file's `(unanchorable)` row sorts last, since
it covers hunks spread across the file rather than any one position.

A `chmod +x` with no content edit produces no changed symbols, so `rgit diff`
lists it as a `MODE` entry — the file is never falsely reported clean. Stage it
with a pathspec (`rgit commit script.sh`); `--sym` cannot express a mode change.

## Help

`rgit --help`, `rgit -h`, and `rgit help` print the top-level command list on
stdout and exit 0. `rgit commit --help` / `-h` prints that command's own flags
the same way. A bare `rgit` (no command at all) is a usage error, not a help
request — see § Exit codes.

## Flags

| Flag | Behavior |
| --- | --- |
| `--sym FILE:NAME` | Explicit anchor form; equivalent to a bare `FILE:NAME` positional. Repeatable. |
| `--file PATH` | Explicit pathspec form; equivalent to a bare positional. Repeatable. |
| `-m MSG`, `--message MSG` | Commit message. **Repeatable** — values join as blank-line-separated paragraphs, as git does. |
| `-F FILE`, `--message-file FILE` | Read the message from a file, or `-` for stdin. Mutually exclusive with `-m`. |
| `-s`, `--signoff` | Append `Signed-off-by:`. Forwarded to `git commit`. |
| `--trailer TOKEN:VALUE` | Append a trailer (`Refs:`, `Co-authored-by:`). Repeatable, forwarded. |
| `--amend` | Amend the previous commit. Anchors stage into it as they would a new commit. With neither `-m` nor `-F`, reuses HEAD's message unchanged (`--no-edit`) — `rgit` never opens an editor, so that is the only message an unattended `--amend` can have. Give `-m`/`-F` to replace it as usual. |
| `--allow-empty` | Permit a commit with no changes. Suppresses exit 11. |
| `--push` | Push upstream after a successful commit. No rollback on push failure. |
| `--dry-run` | Preview only. Writes no objects, stages nothing, runs no hooks. Lists each target it resolved with that symbol's `+N/-M`, using the same counts as `rgit diff`. |
| `--no-verify` | Skip git hooks (standard git meaning). Hooks run by default. |
| `--unstaged` | (`diff`) Worktree vs index — git's bare `diff`. |
| `--staged`, `--cached` | (`diff`) Index vs `HEAD`. Both spellings. |
| `--range REVS` | (`diff`) Explicit form of a positional revision range. |
| `--porcelain` | (`diff`) Stable tab-separated records. |
| `--exit-code` | (`diff`) Exit 1 when anything is committable, 0 when clean. |
| `--quiet` | (`diff`) Implies `--exit-code` and suppresses output. |

`commit` requires a message (`-m` or `-F`) and at least one target, unless
`--amend` is given with neither — then it reuses HEAD's message via `--no-edit`.

On success it relays `git commit`'s own summary — branch, new SHA, and the
changed/insertion/deletion counts — then lists each staged target with its
`+N/-M`, which git cannot report because git does not know about symbols. Hook
output is passed through too. `--dry-run` prints the same listing, so a preview
and the commit it previews are comparable line for line, and neither needs a
follow-up `git show` or `rgit diff` to interpret.

Repeatable `-m` gives subject and body without embedding newlines in one shell
argument:

```console
$ rgit commit -m "fix(auth): reject expired tokens" \
              -m "Tokens past exp were accepted because the clock check ran
before decode. Closes #42." \
              auth.go:ValidateToken
```

**Invalid combinations:** `--dry-run` + `--push`, `--staged` + `--range`,
`--staged` + `--unstaged`, `-m` + `-F` → exit 129. Naming one path both as a
path and as a symbol anchor → exit 5, in whichever spelling: `--file` with
`--sym`, or the positional forms `greet.go greet.go:A`.

A missing conventional-commit shape (`type(scope): subject`) warns on stderr;
the commit proceeds.

## Behaviour inherited from git

`rgit` is `git add <pathspec> && git commit` at symbol granularity, so:

- Work you staged before invoking `rgit` **comes along** with the commit.
- A hook rejecting the commit **leaves staging in place**; nothing is rolled back.
- Hooks are not policed — a hook may stage paths you did not name, exactly as
  under plain `git commit`. Use `--no-verify` to disable them.
- Merges and rebases are not special-cased.
- `commit.cleanup` and `commit.gpgsign` are honoured. `commit.template` is
  **not** — templates prefill an editor and `rgit` never opens one.

When stdin is not a terminal, `rgit` sets `GIT_TERMINAL_PROMPT=0` so a
credential or GPG prompt fails fast instead of hanging.

## Targets with nothing to commit

Naming a target that has no uncommitted changes is a warning, not a failure.
`rgit` prints one line per such target on stderr and commits the rest:

```text
[warning] target 'auth.go:oldHelper' has no uncommitted changes; skipping
```

Exit is **11** only when *every* named target turned out unchanged — that is,
when there is genuinely nothing to commit. `--allow-empty` suppresses it.

## Exit codes

| Exit | Condition |
| --- | --- |
| 0 | Success (possibly with stderr warnings for unchanged targets) |
| 3 | Anchor unresolvable — missing in both worktree and HEAD; candidates listed |
| 4 | Ambiguous anchor — candidates listed |
| 5 | Contradictory anchors — one path named both as a path and as a symbol anchor |
| 6 | Normalized LSP ↔ tree-sitter extent mismatch |
| 7 | Refused path — gitignored and untracked |
| 8 | Commit succeeded; `--push` failed |
| 9 | Unsupported / deferred language for a symbol anchor |
| 10 | Symbol anchor refused on a special path (symlink, gitlink, binary) |
| 11 | All named targets resolve but have no uncommitted changes |
| 128 | Fatal git / system failure (includes hook rejection, GPG failure) |
| 129 | Invalid usage (bad flags, missing message, no targets, path escape) |

128 and 129 follow git's own conventions.
