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
`--sym`, `(unanchorable)` hunks in that file are omitted. Filtering matches the
anchor's resolved canonical form, so a gopls-style `(*A).Get` or a heading's
raw text filters correctly even though `rgit diff` itself only ever emits the
canonical spelling.

On a repository with no commits yet there is no `HEAD` to compare against, and
`git diff HEAD` fails outright. The default scope falls back to the empty tree,
so everything staged or untracked lists as an addition — `rgit diff` answers
the same question in a fresh `git init` that it does anywhere else, and
`rgit commit` writes the root commit.

## Output

Plain text only. The default is the aligned layout shown above;
`--porcelain` replaces it with stable tab-separated records.

**The record layouts, the `STATUS` tokens, and the ordering guarantee are
specified in [`CODES.md`](CODES.md#output-records).**

A `chmod +x` with no content edit produces no changed symbols, so `rgit diff`
lists it as a `MODE` entry — the file is never falsely reported clean. Stage it
with a pathspec (`rgit commit script.sh`); `--sym` cannot express a mode change.

## Help

`rgit --help`, `rgit -h`, and `rgit help` print the top-level command list on
stdout and exit 0. `rgit diff --help` / `-h` and `rgit commit --help` / `-h`
print that command's own flags the same way, generated from the flag set itself
so the two cannot drift. A bare `rgit` (no command at all) is a usage error, not
a help request — see § Exit codes.

`rgit --version` prints `rgit <version>` on its first line and exits 0. The
version is stamped at build time (`-ldflags "-X main.version=vX.Y.Z"`) and
reads `dev` in a build that did not set one. That first line is the only one
scripts should parse — a second line reports which optional/gated grammars
this exact binary was compiled with (see § Languages below), so it can
change independently of the version itself.

## Languages

`rgit languages` lists every grammar compiled into the running binary: name,
file extensions, and whether it is present only because a build tag selected
it. SQL is the first grammar gated this way — `-tags rgit_sql`, generated by
`cmd/rgit-install` when the tree-sitter CLI is on `PATH`, see
[`INSTALL.md`](INSTALL.md#sql-support) — so the identical binary can answer
"what do you support" two different ways depending on how it was built.
`rgit --version`'s second line names the same gated grammars, so telling a
SQL-enabled binary from a plain one never needs a separate command.

```console
$ rgit languages
css           .css
go            .go
json          .json
markdown      .md
python        .py
shell         .sh
sql           .sql   (build-tag gated)
toml          .toml
typescript    .ts .mts .cts
yaml          .yaml .yml
```

A `.sql` anchor on a binary built without `rgit_sql` still fails with exit 9
("no grammar registered"), the same as any genuinely unsupported language —
but unlike one this resolver has never supported, the message also names the
build tag and points at rebuilding.

## Doctor

`rgit doctor` reports environment health: git on `PATH` (the one thing rgit
cannot run without), the optional tree-sitter CLI, which language servers
from [`INSTALL.md`](INSTALL.md#language-servers) answer on `PATH` for the
extent cross-check, and the same grammar listing `rgit languages` prints. It
exits 0 unless rgit genuinely cannot function — a missing language server or
the tree-sitter CLI is informational, since degraded `[ts-only]` resolution
is normal and documented, not an error (see [`CODES.md`](CODES.md)).

## Shell completion

`rgit completion bash` and `rgit completion zsh` print a completion script
for that shell to stdout; nothing else is written. It completes subcommands,
each subcommand's own flags, plain file paths, and — the useful part —
symbol names after `FILE:`, by shelling back out to `rgit diff --porcelain`
and matching its `SYMBOL` column against `FILE` (record shape:
[`CODES.md`](CODES.md#output-records)). If that call fails for any reason —
the working directory is not a repository, `rgit` is not on `PATH`, anything
— completion offers nothing rather than printing to the prompt.

An unrecognized or missing shell argument is a usage error, same table as
everywhere else. Install instructions: [`INSTALL.md`](INSTALL.md#shell-completion).

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
| `--push` | Push upstream after a successful commit. No rollback on push failure. If the branch has no upstream configured, the exit-8 message names it and the fix (`git push -u origin <branch>`, or `push.autoSetupRemote`) — `rgit` never adds `-u` itself. |
| `--dry-run` | Preview only. Writes no objects, stages nothing, runs no hooks. Lists each target it resolved with that symbol's `+N/-M`, using the same counts as `rgit diff`. |
| `--no-verify` | Skip git hooks (standard git meaning). Hooks run by default. |
| `--fixup <commit>` | Autosquash fixup for `<commit>` (also accepts `amend:<commit>`/`reword:<commit>`, forwarded verbatim). Generates its own subject, so `-m`/`-F` are not required; either still appends as an extra body paragraph rather than conflicting. |
| `--squash <commit>` | Autosquash squash for `<commit>`. Same message rule as `--fixup`. |
| `--author <author>` | Override the commit author. Plain forwarding. |
| `--date <date>` | Override the commit date. Plain forwarding. |
| `--reset-author` | Take the author identity from the committer instead of carrying the original forward. Plain forwarding; git accepts it only with `--amend` or `--fixup=amend:`, and `rgit` does not police the combination. |
| `--porcelain` | (`commit`) List staged targets as stable tab-separated records instead of the aligned listing. Replaces `git commit`'s own summary rather than adding to it, exactly as `git commit --porcelain` does. Works with `--dry-run`, which then emits records alone with no preamble. |
| `-q`, `--quiet` | (`commit`) Suppress the summary and the target listing. stdout is empty; warnings, notices and hook output still go to stderr, as under git's own `-q`. |
| `-S`, `-S<key-id>`, `--gpg-sign`, `--gpg-sign=<key-id>` | GPG-sign the commit, with the configured default key or an explicit one. See the note below on how `-S` is parsed. |
| `--no-gpg-sign` | Do not GPG-sign, overriding `commit.gpgsign=true`. |
| `--unstaged` | (`diff`) Worktree vs index — git's bare `diff`. |
| `--staged`, `--cached` | (`diff`) Index vs `HEAD`. Both spellings. |
| `--range REVS` | (`diff`) Explicit form of a positional revision range. |
| `--porcelain` | (`diff`) Stable tab-separated records. |
| `--exit-code` | (`diff`) Exit 1 when anything is committable, 0 when clean. |
| `--quiet` | (`diff`) Implies `--exit-code` and suppresses output. |

`commit` requires a message (`-m` or `-F`) and at least one target, unless
`--amend`, `--fixup`, or `--squash` is given with neither — each generates its
own message (`--amend` reuses HEAD's via `--no-edit`; `--fixup`/`--squash`
generate `fixup!`/`squash! <subject>`, exactly as plain `git commit` does).

`-S` is accepted in git's own spellings: bare `-S`, or `-S<key-id>` with the
key attached. It is rewritten to the long form before the flag parser runs,
because `pflag` resolves an optional-value shorthand's default before checking
for an attached value, so a registered `-S` would read `-SDEADBEEF` as a chain
of nonexistent single-letter flags. Following getopt (and git), everything
after `-S` in the same token is the key id — `-Ss` means the key `s`. Packing
`S` into a chain behind other shorthands (`-sS`) is not supported and is
refused by name rather than misread.

On success it relays `git commit`'s own summary — branch, new SHA, and the
changed/insertion/deletion counts — then lists each staged target with its
`+N/-M`, which git cannot report because git does not know about symbols. Hook
output is passed through too. `--dry-run` prints the same listing, so a preview
and the commit it previews are comparable line for line, and neither needs a
follow-up `git show` or `rgit diff` to interpret.

`--porcelain` replaces both with stable tab-separated records:

```text
FILE<TAB>SYMBOL<TAB>ADDED<TAB>DELETED
auth.go<TAB>ValidateToken<TAB>12<TAB>3
package.json<TAB><TAB>4<TAB>1
```

`SYMBOL` is empty for a pathspec target, as in `rgit diff --porcelain`. There
is no `STATUS` column: an unchanged target is omitted from the listing
entirely, so every record would carry the same value. The records are
identical for `--dry-run` and for the commit it previews.

Repeatable `-m` gives subject and body without embedding newlines in one shell
argument:

```console
$ rgit commit -m "fix(auth): reject expired tokens" \
              -m "Tokens past exp were accepted because the clock check ran
before decode. Closes #42." \
              auth.go:ValidateToken
```

**Invalid combinations:** `--dry-run` + `--push`, `--staged` + `--range`,
`--staged` + `--unstaged`, `-m` + `-F`, `--porcelain` + `--quiet` → exit 129.
Naming one path both as a path and as a symbol anchor → exit 5, in
whichever spelling: `--file` with
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
- `commit.cleanup` and `commit.gpgsign` are honoured as configuration, and
  `--gpg-sign`/`--no-gpg-sign` override either. `commit.template` is **not**
  honoured — templates prefill an editor and `rgit` never opens one.
- `rgit` never adds `--set-upstream` to a push on its own initiative, even for
  a branch with none configured — see `--push` above.

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

**Specified in [`CODES.md`](CODES.md#exit-codes)**, along with which of them
each command can produce.
