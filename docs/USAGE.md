# Usage

Command reference. Internals: [`AGENTS.md`](../AGENTS.md). Anchors:
[`ANCHORS.md`](ANCHORS.md).

## Commands

```console
$ rgit diff
  auth.go      ValidateToken       +12/-3
               oldHelper           DELETED  +0/-14
               @imports            +1/-0
               (unanchorable)      +2/-0   -> use --file auth.go
  README.md    (no symbols)        +2/-0
  newfile.go   @header             +2/-0
               NewFunc             +13/-0
  config.ini   (untracked)         +4/-0   -> use --file config.ini
  script.sh    (mode 644->755)     +0/-0   -> use rgit commit script.sh
  logo.png     (binary)            -/-

$ rgit commit -m "auth: reject expired tokens" \
    auth.go:ValidateToken auth.go:@imports
```

An untracked file with a grammar attributes per symbol. `config.ini` and
binaries collapse to one `(untracked)` row.

## Global flags

`-C <path>`, `--version`, and `-h`/`--help`, all **before** the command.

`-C <path>` is `git -C`: discover the repo from there; relative pathspecs
and anchors resolve against it. Must precede the command — `git commit -C`
already means reuse a message. Repeats accumulate (`-C a -C b` is
`-C a/b`); `-C ""` is a no-op. Missing argument: 129. Unenterable: 128.
Glued `-C<path>` is 129, as in git.

## Argument shape

Positionals carry pathspecs, revisions, and anchors. `--file` and `--sym`
are explicit equivalents.

Git pathspec magic is **leading**-colon. An interior colon is free for
`FILE:NAME`.

**For `diff`, a bare `A..B` or `A...B` is pulled out before the table.**
`git rev-parse --verify` fails on range syntax. Only a token that is not
an existing worktree/index/HEAD path is a range — `../shared/util.go`
contains `..` and is a path. At most one range; a second is 129. `--range`
is the explicit form and exclusive with the positional.

First match wins:

| # | Test | Result |
| --- | --- | --- |
| 1 | After `--` | Pathspec, always |
| 2 | Starts with `:` | Git pathspec magic, verbatim |
| 3 | *(`diff` only)* `git rev-parse --verify` | Revision or `rev:path` |
| 4 | Path in worktree, index, or HEAD | Pathspec |
| 5 | Splits at some `:` into existing path + name, last colon first | Symbol anchor |
| 6 | None | Error listing each try |

`src/notes:draft.md` exists → rule 4. `auth.go:ValidateToken` does not →
rule 5. Force with `--sym` or `--file` when both exist.

Rule 5 tries the **last** colon first, then leftwards — a path containing
a colon still splits as before; a *name* containing one
(`s.css:a:hover`) still resolves. Rule 3 splits at the **first** colon
(`HEAD~1:f.go`).

**Exactly two `rev:path` positionals naming the same path** compare that
file across revisions: `rgit diff HEAD~1:auth.go HEAD:auth.go`. Combining
with `--file`, `--staged`/`--unstaged`, a range, or bare revisions is 129.
A single unpaired `rev:path` is 129.

**Paths are relative to the cwd, not the repo root.** Leading-colon magic
(`:/`, `:(top)`) is root-relative. Output is always root-relative.

```console
rgit commit -m "chore: bump deps" package.json bun.lock
rgit commit -m "fix(auth): reject expired" auth.go:ValidateToken auth.go:@imports
rgit commit -m "docs: note expiry" README.md auth.go:ValidateToken
rgit diff main...HEAD -- src/
rgit commit -m "chore: format" 'src/*.go'
rgit commit -m "chore: sweep" . ':(exclude)docs/*'
rgit diff ':/src'
```

## Diff scope

Default is **everything committable**: staged + unstaged vs `HEAD`, plus
untracked — `git diff HEAD` ∪ untracked, **not** `git diff`.

| Scope | Flag | Git equivalent |
| --- | --- | --- |
| Everything committable (default) | — | `git diff HEAD` + untracked |
| Unstaged only | `--unstaged` | `git diff` |
| Staged only | `--staged` / `--cached` | `git diff --staged` |
| Revision range | positional or `--range` | `git diff A..B` / `A...B` |

`--sym` / `--file` filter; `--sym` omits `(unanchorable)` in that file.
Filter matches the resolved canonical form.

No commits yet: default scope falls back to the empty tree; `commit`
writes the root commit.

## Output

Aligned layout by default. `--porcelain` is
[`CODES.md`](CODES.md#output-records).

A `chmod +x` with no content edit is `MODE`. Stage with a pathspec;
`--sym` cannot express it.

`--sym FILE:member` on a member of a container new to `HEAD` warns:
`commit` would stage the whole container
([`ANCHORS.md`](ANCHORS.md#qualification)). The unfiltered listing does
not.

`-p`/`--patch` appends git's own patch after the report. Exclusive with
`--porcelain`.

## Show

```console
$ rgit show auth.go:ValidateToken
func ValidateToken(tok string) error {
 if tok == "" {
  return ErrEmpty
 }
 return nil
}

$ rgit show --source v1.4.0 auth.go:ValidateToken | diff - <(rgit show auth.go:ValidateToken)
```

Writes the resolved extent, no added newline. One anchor, no
`--porcelain`/`--with-header`: raw bytes. Several: `FILE:ANCHOR<TAB>NBYTES`
then exactly those bytes. `--porcelain` forces framing for one too
([`CODES.md`](CODES.md#rgit-show---porcelain)). `--with-header` is a
deprecated alias. All anchors resolve before any write.

Default source: worktree, else `HEAD`. `--source <rev>` reads `<rev>:FILE`
(both spellings). Unknown revision: 128. Path absent at a known
revision: 3. Current name only unless `--follow-rename`, which prints the
oldest segment (same walk as `blame`/`log`). `--source` +
`--follow-rename`, a plain path, or zero anchors: 129. Unresolvable
anchor: 3/4/9, never a whole-file dump. Nothing is written. One
`--source` per invocation.

## Blame

```console
$ rgit blame auth.go:ValidateToken
^1a2b3c4 (Alice Example 2024-01-15 10:00:00 -0800  9) func ValidateToken(tok string) error {
 5d6e7f8 (Bob Example   2024-03-02 14:22:11 -0800 10)     if tok == "" {
 ...

$ rgit blame --source v1.4.0 auth.go:ValidateToken
```

`git blame -L start,end -- FILE` on the resolved extent. Worktree when
present, else `HEAD`. `--source` as `show`. `--follow-rename` uses the
rename-boundary walk and always reads `HEAD`. `--source` +
`--follow-rename`: 129. Unresolvable: 3/4/9, never a whole-file blame.

`-p` is `--porcelain` (git blame's meaning, not patch). `--porcelain`
passes through to `git blame --porcelain`.

## Log

```console
$ rgit log auth.go:ValidateToken
a1b2c3d fix(auth): reject expired tokens
9e8f7d6 feat(auth): add ValidateToken
```

`git log -L start,end:FILE` — one record per touching commit, newest
first. **Patch-free by default**; `-p`/`--patch` is git's own `-L` output.
`--since`/`--until`, `-n`/`--max-count`, and `--follow-rename` combine;
each rename segment gets the same filters.

Resolved against **`HEAD`**, not the worktree. `--source <rev>` resolves
at that revision and starts `git log -L` there. `--source` +
`--follow-rename`: 129. `git log -L` can lose a rename that also moves
the symbol; `--follow-rename` re-resolves at each boundary. Query the
current name. [`LIMITATIONS.md`](LIMITATIONS.md#history-across-renames).

`--porcelain`: `HASH<TAB>SUBJECT`. `--json`: one object. Each exclusive
with the other and with `-p`. [`CODES.md`](CODES.md#output-records).

### Log across renames

```console
$ rgit log new.go:Foo --follow-rename
b6f3975 edit
2793187 rename + reorder
c8fdd8a init
```

Continues past a rename that plain `log FILE:SYMBOL` stops at. One parse
per rename. [`LIMITATIONS.md`](LIMITATIONS.md#history-across-renames).

### Log by date and path

```console
$ rgit log bun.lock
a1b2c3d chore: bump lockfile
9e8f7d6 chore: add lockfile

$ rgit log --since=2024-01-01 -n 5 -- src/auth
a1b2c3d fix(auth): reject expired tokens
```

Path positionals (with or without `--since`/`--until`) and no
`FILE:SYMBOL` select ordinary git history — same six-rule table, so
`src/notes:draft.md` stays a pathspec. Dates are forwarded unparsed.
Neither path, date, nor `--follow`: 129.

`--follow` is git's `--follow` on exactly one path (else 129). A
`FILE:SYMBOL` does not accept `--follow`; that form uses `--follow-rename`.
`-n`/`--max-count` limits either form.

## Context

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
```

One-call orientation. **The records are fixed** — a command with options
becomes `git status` with extra steps. `--json` is that stream as one
object ([`CODES.md`](CODES.md#rgit-context---json)). Any other argument
is 129. Grammar: [`CODES.md`](CODES.md#rgit-context).

**Capped at 16 KiB.** `B`/`H`, then `S`, `W`, `F` (survive first), then
`T`, then `C` (last 20). Trailing `X` names withheld rows. `--json`
encodes what the cap kept; the JSON text is not measured against 16 KiB.

## Help

`rgit --help` / `-h` / `help` print the command list on stdout and exit 0.
`diff` and `commit` `--help`/`-h` are generated from the flag set. Other
commands print hand-written usage. A bare `rgit` is 129, not help.

`rgit --version` and `rgit version` print `rgit <version>` first and exit 0:

```console
$ rgit --version
rgit v2.2.3
optional grammars: sql
built with go1.27.1, linux/amd64
```

Stamped via `-ldflags "-X main.version=vX.Y.Z"`. Else the toolchain
embed: `go install …@vX.Y.Z` reports the tag; `go build` in a checkout
reports `git describe --tags --always --dirty`. Neither: `dev`. Scripts
parse the first line only. Line 2 is gated grammars; line 3 is
toolchain/platform.

## Languages

Lists every compiled grammar. SQL is `-tags rgit_sql`
([`INSTALL.md`](INSTALL.md#sql-support)). `--version` line 2 names the
same gated grammars.

```console
$ rgit languages
css         .css
go          .go
html        .html .htm
json        .json
json5       .json5
jsonc       .jsonc
markdown    .md .markdown .mdx
python      .py .pyi
rust        .rs
scss        .scss
shell       .sh .bash
sql         .sql                     (build-tag gated)
toml        .toml
tsx         .tsx .jsx .js .mjs .cjs
typescript  .ts .mts .cts
yaml        .yaml .yml
zsh         .zsh .zshrc .zshenv .zprofile
```

Sample is a `-tags rgit_sql` build. `--porcelain` /
`--json`: [`CODES.md`](CODES.md#rgit-languages---porcelain). Exclusive
(129). `CROSS-CHECK` is compile-time wiring (`wired` / `ts-only` for
TOML, SQL, JSON5). A `.sql` anchor without the tag is exit 9 and names
the tag.

**`--in-repo`** lists grammars with a matching file in this repo
(advisory). Extension-then-shebang; tracked files sample `HEAD` when the
worktree copy is gone; untracked (not ignored) count. Requires a git
repo.

## Doctor

Reports git on `PATH` and its version vs
[`INSTALL.md`](INSTALL.md#prerequisites), the tree-sitter CLI, language
servers on `PATH`, and the `languages` listing. Exits 0 unless rgit
cannot function — a missing server, CLI, or below-floor git is
informational.

`--porcelain` / `--json`: [`CODES.md`](CODES.md#rgit-doctor---porcelain).
Exclusive (129). No grammar rows — `languages --porcelain` owns those.

**`--deep`** dials each on-PATH server (same handshake as `diff`/`commit`)
and appends `(reachable)` or `(degraded — handshake timed out or
unanswered)`. Not the default. A server not on `PATH` is never dialed.

## Symbols

```console
$ rgit symbols auth.go
@imports
ValidateToken

$ rgit symbols --with-lines auth.go
1,3 @imports
12,27 ValidateToken
```

Lists resolvable symbols from the worktree, or `HEAD` if the worktree
copy is gone. Path in neither: error. `--for-commit` omits structured-data
symbols `commit` refuses; a supported structured-data file still exits 0
with no output.

`--with-lines` is git's `-L` range
([`CODES.md`](CODES.md#rgit-symbols---with-lines)). Several files prefix
`FILE<TAB>` as `grep` does; `--with-filename` forces it for one.
`--porcelain` is NUL-terminated. `--json`:
[`CODES.md`](CODES.md#rgit-symbols---json). Exclusive with `--porcelain`
(129). All files resolve before any write.

No file argument: usage on stderr, 129. Unreadable: 128. Unsupported
language: 9.

When `core.ignorecase` is true, extension lookup is case-insensitive for
`symbols`, `commit`, `diff`, `blame`, `log`, and `languages`; the path is
unchanged.

## Shell completion

`rgit completion bash|zsh|fish|pwsh` prints a script to stdout.

Completes subcommands, flags, paths, and names after `FILE:` via `rgit
symbols FILE` (or `--for-commit` for `commit`). Failure (no repo, not on
`PATH`) offers nothing. Install: [`INSTALL.md`](INSTALL.md#shell-completion).

## Flags

`blame`: `-p`/`--porcelain`, `--follow-rename`, `--source`, `--help`.
`languages`: `--porcelain`, `--json`, `--in-repo`, `--help`.
`log`: `--porcelain`, `--json`, `-p`/`--patch`, `--follow`,
`--follow-rename`, `--source`, `-n`/`--max-count`, `--since`/`--until`,
`--help`.
`show`: `--source`, `--follow-rename`, `--porcelain`, `--with-header`,
`--help`.
`symbols`: `--for-commit`, `--with-lines`, `--with-filename`,
`--porcelain`, `--json`, `--help`.
`doctor`: `--porcelain`, `--json`, `--deep`, `--help`.
`context`: `--json`, `--help`/`-h`.
`completion`: `--help`/`-h` plus the shell argument.

| Flag | Behavior |
| --- | --- |
| `--sym FILE:NAME` | Explicit anchor. Repeatable. |
| `--file PATH` | Explicit pathspec. Repeatable. |
| `--pathspec-from-file FILE` | (`commit`, `diff`) One target per line, or stdin when `-`. Exclusive with `-F -` when both would take stdin (129). |
| `--pathspec-file-nul` | NUL-delimited `--pathspec-from-file`. |
| `-m MSG`, `--message MSG` | Repeatable; values join as blank-line paragraphs. |
| `-F FILE`, `--message-file FILE` | Message from file or stdin. Exclusive with `-m`. |
| `-s`, `--signoff` | `Signed-off-by:`. Forwarded. |
| `--trailer TOKEN:VALUE` | Repeatable, forwarded. |
| `--amend` | Amend previous. No `-m`/`-F`: reuse HEAD (`--no-edit`). No targets: amend the index as it stands. |
| `--allow-empty` | Permit no changes; suppresses exit 11. No targets: commit the index; still needs a message unless another auto-message flag is set. |
| `-i`, `--include` | Stage named targets into the index, then commit it (git's `--include`). Needs a target. Exclusive with `--only` and `--stage-only`. |
| `-o`, `--only` | Commit only the named targets; other staged paths stay. Needs a target unless `--amend`. Private index + `rgit-commit.lock`. Another writer moving HEAD mid-commit: 128. |
| `--reuse-message=<commit>` | Git's `--reuse-message`. Long form only. Exclusive with `-m`. |
| `--reedit-message` | Refused (129). |
| `--push` | Push after a successful commit. No rollback. No upstream: exit-8 names the branch and `git push -u origin <branch>` / `push.autoSetupRemote`. Never adds `-u`. |
| `--stage-only` | Stage into the real index and stop: no message, no hooks. Needs a target. Exclusive with `--include`, `--only`, `--push`, `--amend`, `--allow-empty`, `-m`/`-F`, `--fixup`, `--squash`, `--reuse-message`. No unstage of one symbol — `git restore --staged FILE` takes the whole file. |
| `--dry-run` | Preview. No objects, no staging, no hooks. Same `+N/-M` as `diff`. |
| `--no-verify` | Skip hooks. |
| `--fixup <commit>` | Autosquash (`amend:`/`reword:` forwarded). Generates its subject; `-m`/`-F` append. |
| `--squash <commit>` | Same message rule as `--fixup`. |
| `--author <author>` | Forwarded. |
| `--date <date>` | Forwarded. |
| `--reset-author` | Forwarded; git accepts it only with `--amend` or `--fixup=amend:`. |
| `--porcelain` | (`commit`) `H<TAB>SHA` then target records. `--dry-run`: targets only. |
| `-q`, `--quiet` | (`commit`) Empty stdout; stderr still carries warnings and hooks. |
| `-S`, `-S<key-id>`, `--gpg-sign`, `--gpg-sign=<key-id>` | GPG-sign. See `-S` parsing below. |
| `--no-gpg-sign` | Override `commit.gpgsign=true`. |
| `--unstaged` | (`diff`) Worktree vs index. |
| `--staged`, `--cached` | (`diff`) Index vs `HEAD`. |
| `--range REVS` | (`diff`) Explicit range. |
| `--porcelain` | (`diff`) TSV records. |
| `--exit-code` | (`diff`) 1 if committable, 0 if clean. |
| `--quiet` | (`diff`) Implies `--exit-code`, suppresses output. |
| `-p`, `--patch` | (`diff`) Git's patch after the report. Exclusive with `--porcelain`. |
| `--with-header` | (`show`) Deprecated `--porcelain` alias. |
| `--porcelain` | (`show`) Length-framed extents. |
| `--with-filename` | (`symbols`) `FILE<TAB>` prefix. |
| `--porcelain` | (`symbols`) NUL records. |
| `--json` | (`symbols`) One object. Exclusive with `--porcelain`. |
| `--follow-rename` | (`show`, `blame`, `log`) Re-resolve at each rename. Exclusive with `--source` (129). Plain path: 129. |
| `--source REV` | (`show`, `blame`, `log`) Read/blame/walk `REV:FILE`. Unknown rev: 128. Absent path: 3. |
| `--since DATE`, `--until DATE` | (`log`) Date bounds. Forwarded unparsed. |
| `--follow` | (`log`, path form) Exactly one path (else 129). Not on `FILE:SYMBOL`. |
| `-n N`, `--max-count=N` | (`log`) Cap; under `--follow-rename`, per segment (total may exceed `N`). |
| `--json` | (`log`) `hash`/`subject` object. Exclusive with `--porcelain` and `-p`. |

`commit` needs `-m` or `-F` unless `--amend`, `--fixup`, `--squash`, or
`--reuse-message`. It needs a target unless those or `--allow-empty`.
Zero targets means skip staging, not `git add -A`. During merge,
cherry-pick, or revert, omitting `-m`/`-F` reuses Git's message.

`-S` is rewritten to the long form before pflag: an optional-value
shorthand would misread `-SDEADBEEF` as a flag chain. Everything after
`-S` in the same token is the key id (`-Ss` is key `s`). `-sS` is
refused by name.

Success relays `git commit`'s summary, then each target's `+N/-M`.
`--porcelain`: [`CODES.md`](CODES.md#output-records).

```console
$ rgit commit -m "fix(auth): reject expired tokens" \
              -m "Tokens past exp were accepted because the clock check ran
before decode. Closes #42." \
              auth.go:ValidateToken
```

**Invalid combinations:** `--include` + `--only`; `--stage-only` with any
of its exclusions (including `--include`); `--dry-run` + `--push`;
`--staged`/`--unstaged` + a revision; `--staged` + `--unstaged`; `--range`

+ a positional range; `-m` + `-F`; `--porcelain` + `--quiet` (`diff`);
`--porcelain` + `-p` (`diff`, `log`); `--json` + `-p` (`log`);
`--porcelain` + `--json` (`languages`, `doctor`, `symbols`, `log`);
`--source` + `--follow-rename` (`show`, `blame`, `log`) → 129. One path
as both path and symbol → 5 (`greet.go greet.go:A`).

`FILE:SYMBOL` on JSON/JSONC/JSON5/YAML/TOML → 12. Name the path.
[`CODES.md`](CODES.md#exit-12-is-commits-alone).

A missing conventional-commit shape warns; the commit proceeds.

## Behaviour inherited from git

`rgit` is `git add <pathspec> && git commit` at symbol granularity:

+ Pre-staged work **comes along**, unless `--only`. `-i`/`--include` is
  that default made explicit.
+ A hook rejecting the commit rolls staging back to its pre-commit state
  (under `--only` nothing was staged). Snapshot, temp index, restore on
  failure. Index entries only — no worktree file is written.
+ Hooks are not policed. `--no-verify` disables them.
+ Merge / cherry-pick / revert without `-m`/`-F` uses `--no-edit`. Rebase
  remains `git rebase --continue`.
+ `commit.cleanup` and `commit.gpgsign` are honoured.
  `commit.template` is **not** — `rgit` never opens an editor.
+ `--push` never adds `--set-upstream`.

When stdin is not a terminal, `GIT_TERMINAL_PROMPT=0`.

## Ordinal-anchor warnings

An ordinal (`auth.go:init#2`) is positional. Resolving one prints:

```text
[warning] anchor 'dup.go:init#2' is positional; inserting a symbol above it repoints it -- qualify it where the language allows
```

`commit`, `diff --sym`, `show`, `blame`, and `log` all print it. A bare
or qualified anchor that matches the same symbol does not.

## Targets with nothing to commit

Warning, not failure, per unchanged target:

```text
[warning] target 'auth.go:oldHelper' has no uncommitted changes; skipping
```

Exit **11** only when every named target is unchanged. `--allow-empty`
suppresses it.

## Exit codes

[`CODES.md`](CODES.md#exit-codes).
