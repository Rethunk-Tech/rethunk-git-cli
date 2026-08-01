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

## Global flags

Three, all given **before** the command: `-C <path>`, `--version`, and
`-h`/`--help` (the latter two under § Help).

`-C <path>` runs as if `rgit` had been started in `<path>`, exactly as
`git -C <path>` does — the repository is discovered from there, and a
relative pathspec or anchor resolves against it, so passing `-C` is
indistinguishable from having stood there:

```bash
rgit -C ~/src/api diff --porcelain
rgit -C ~/src/api commit -m "fix(auth): reject expired" auth.go:ValidateToken
```

It must come before the command, because `git commit -C <commit>` already
means "reuse that commit's message" and the two spellings must not collide.
Repeats accumulate, each read relative to the last (`-C a -C b` is `-C a/b`,
an absolute path resetting), and `-C ""` is a no-op — git's own semantics in
each case.

The directory is checked before the command runs, so both refusals reach
even a command that never opens a repository: no directory argument at all
is a usage error (129), and a directory `rgit` cannot enter is fatal (128).
The glued `-C<path>` spelling is a usage error, as it is in git.

## Argument shape

Positional arguments carry everything: pathspecs, revisions, and symbol anchors.
`--file` and `--sym` exist as explicit equivalents for scripting and
disambiguation, never as requirements.

All git pathspec magic is **leading**-colon (`:(exclude)`, `:(glob)`, `:/`). An
*interior* colon is therefore free for `FILE:NAME` and needs no flag.

**For `diff`, a bare `A..B` or `A...B` positional is pulled out before the
precedence table below ever runs.** `git rev-parse --verify` (rule 3) fails
outright on range syntax, so a range token would otherwise fall through
every rule to an unresolvable-argument error instead of selecting a scope.
Only a token with no existing worktree/HEAD path of that exact name is
treated as a range — git forbids `..` in ref names, but a legitimate
relative pathspec like `../shared/util.go` also contains `..`, and path
existence wins over the heuristic. At most one such token is accepted per
invocation; a second is a usage error (exit 129). `--range` is the explicit
form of the same value and is mutually exclusive with the positional one.

Resolution precedence, first match wins:

| # | Test | Result |
| --- | --- | --- |
| 1 | Appears after `--` | Pathspec, always |
| 2 | Starts with `:` | Git pathspec magic, passed through verbatim |
| 3 | *(`diff` only)* resolves via `git rev-parse --verify` | Revision, or a `rev:path` blob reference — classified, then refused as a diff scope |
| 4 | Names a path existing in the worktree or HEAD | Pathspec |
| 5 | Splits at the last `:` into an existing path + a name | Symbol anchor |
| 6 | None of the above | Error listing each interpretation tried |

No escaping is ever needed. `src/notes:draft.md` is a legal path, so rule 4
claims it; `auth.go:ValidateToken` names nothing, so rule 5 splits it. Use
`--sym` or `--file` to force the reading when a repo genuinely has both.

Rule 3 splits at the **first** colon (`HEAD~1:f.go` is revision `HEAD~1`,
path `f.go`), where rule 5 splits at the **last** — each rule uses the
split git's own syntax needs at that position, not a shared convention.
`rev:path` is real `git diff` syntax (`git diff HEAD~1:f.go HEAD:f.go`
compares two blobs directly), and rule 3 exists so it classifies correctly
rather than being misread as a `FILE:NAME` anchor by rule 5 — not to add a
`rev:path` diff scope of its own. A positional that classifies as one is
refused outright (exit 129): comparing two arbitrary blobs by revision
names no single changed file for `rgit diff` to group rows under. Name the
file directly instead.

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

## Blame

```console
$ rgit blame auth.go:ValidateToken
^1a2b3c4 (Alice Example 2024-01-15 10:00:00 -0800  9) func ValidateToken(tok string) error {
 5d6e7f8 (Bob Example   2024-03-02 14:22:11 -0800 10)     if tok == "" {
 ...
```

`rgit blame FILE:SYMBOL` resolves the anchor exactly like `commit` and `diff
--sym` do, then runs `git blame -L start,end -- FILE` bounded to just that
symbol's own extent in the current worktree file — never the whole file.
There is no revision argument: blame always reads the worktree copy, the same
file a bare `git blame FILE` would.

An anchor that does not resolve is **never** silently widened to a whole-file
blame — it is exit 3 (unresolvable), 4 (ambiguous), or 9 (unsupported
language), the same codes `commit` and `diff --sym` already give the
identical anchor. See [`CODES.md`](CODES.md#exit-codes).

`--porcelain` passes straight through to git's own `git blame --porcelain`
output, unmodified — not a second record format rgit invents. The default is
likewise git's own human-readable blame output, unmodified. See
[`CODES.md`](CODES.md#output-records).

## Log

```console
$ rgit log auth.go:ValidateToken
a1b2c3d fix(auth): reject expired tokens
9e8f7d6 feat(auth): add ValidateToken
```

`rgit log FILE:SYMBOL` resolves the anchor exactly like `commit`, `diff
--sym`, and `blame` do, then runs `git log -L start,end:FILE` bounded to that
symbol's own extent — one record per commit whose own diff touched it, newest
first. **Patch-free by default**: plain `git log -L` always prints the full
patch body for every touching commit, which is precisely the flood this
command exists to avoid. Pass `-p`/`--patch` to see it anyway — git's own
`git log -L` output, unmodified, not a second patch format rgit invents.

Unlike `blame`, the anchor is resolved against **`HEAD`, not the worktree**:
history is a question about what has already been committed, and `git log -L`
itself walks `HEAD`'s own history with no notion of the worktree at all. This
also means a symbol already deleted from the worktree, but still present in
`HEAD`, keeps its history reachable — there is nothing to open on disk, so
`rgit log` never needs to.

A symbol's history stops at the commit that renamed its file: `git log -L`
does not follow renames the way `git log --follow` does for a whole file.
Query it under its current name; see
[`LIMITATIONS.md`](LIMITATIONS.md#history-across-renames).

An anchor that does not resolve is exit 3 (unresolvable), 4 (ambiguous), or 9
(unsupported language) — the same codes `blame`, `commit`, and `diff --sym`
already give the identical anchor. See [`CODES.md`](CODES.md#exit-codes).

`--porcelain` lists stable tab-separated `HASH<TAB>SUBJECT` records instead of
the aligned `<abbrev-hash> <subject>` default, no header. Mutually exclusive
with `-p`/`--patch`. See [`CODES.md`](CODES.md#output-records).

### Log by date and path

```console
$ rgit log --since=2024-01-01 -- src/auth
a1b2c3d fix(auth): reject expired tokens
```

`--since=DATE` or `--until=DATE` (either alone, or together) switches `log`
to a second, unanchored shape: ordinary git history bounded by date and,
optionally, one or more trailing path positionals — no `FILE:SYMBOL` at all.
Values are forwarded to git's own `--since`/`--until` unparsed, so anything
git accepts there (`"2024-01-01"`, `"2 weeks ago"`) works here too. With no
paths, it is the whole repository's history in that window, matching plain
`git log --since=DATE`.

This is the one `git log` carve-out `rgit`'s own "the tree is only ever
inspected through `rgit`" convention otherwise has to make for a plain
`git log --since=... -- <paths>` — closed by giving `rgit log` a second
invocation shape rather than a second command. `--porcelain` and `-p`/
`--patch` behave identically to the `FILE:SYMBOL` form above: patch-free
aligned records by default, `--porcelain`'s tab-separated form on request,
and the real patch body only when `-p`/`--patch` is given, mutually
exclusive with `--porcelain`.

## Context

```text
C<TAB>a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2<TAB>fix(auth): reject expired tokens
C<TAB>9e8f7d6c5b4a9e8f7d6c5b4a9e8f7d6c5b4a9e8f<TAB>feat(auth): add ValidateToken
F<TAB>auth.go<TAB>ValidateToken<TAB>MOD<TAB>12<TAB>3
F<TAB>newfile.go<TAB><TAB>UNTRACKED<TAB>15<TAB>0
```

`rgit context` is one-call repository orientation for an agent's first turn:
recent commit subjects, then the same per-file, per-symbol diffstat `rgit
diff` itself reports for everything committable — as a single, fixed-shape
record stream. It replaces the separate `status`, `diff --stat`, `diff`, and
`log` calls an agent would otherwise make before editing, each billed as its
own subprocess call.

**The output shape is fixed and takes no flags beyond `--help`.** A command
with options becomes `git status` with extra steps — see
[`specs/design.md`](../specs/design.md#commands) for why the shape stays
fixed rather than growing one. Three record types, tab-separated, no header:

| Record | Fields | Meaning |
| --- | --- | --- |
| `C` | `HASH`, `SUBJECT` | One per recent commit, newest first, bounded to the last 20 |
| `F` | `FILE`, `SYMBOL`, `STATUS`, `ADDED`, `DELETED` | One per `rgit diff --porcelain` row — identical fields, plus this stream's own leading type tag |
| `X` | `TRUNCATED`, `COUNT` | At most one, always last: this many records were withheld to hold the byte budget |

The diff half is pure composition, not a second attribution path: it is
literally `rgit diff`'s own default scope (everything committable), rendered
through the same `--porcelain` records and re-tagged per line.

**The whole stream is capped at 16 KiB.** Commits are bounded up front (the
most recent 20, via git's own history limit); the diff section, which has no
such natural bound, is truncated at the byte boundary instead, with a
trailing `X` record naming how many rows were withheld. See
[`../specs/design.md`](../specs/design.md#commands) for the reasoning. See
[`CODES.md`](CODES.md#output-records) for the exact record grammar.

## Help

`rgit --help`, `rgit -h`, and `rgit help` print the top-level command list on
stdout and exit 0. `rgit diff --help` / `-h` and `rgit commit --help` / `-h`
print that command's own flags the same way, generated from the flag set itself
so the two cannot drift. `rgit blame --help`, `rgit log --help`, `rgit
context --help`, `rgit languages --help`, `rgit doctor --help`, and `rgit
completion --help` (each also accepting `-h`) print their own hand-written
usage text instead — surfaces small enough that a generated rendering was
not worth building. A bare `rgit` (no command at all)
is a usage error, not a help request — see § Exit codes.

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
css         .css
go          .go
html        .html .htm
json        .json
markdown    .md .markdown
python      .py .pyi
shell       .sh .bash
sql         .sql                     (build-tag gated)
toml        .toml
tsx         .tsx .jsx .js .mjs .cjs
typescript  .ts .mts .cts
yaml        .yaml .yml
```

This sample is from a `-tags rgit_sql` build; a plain `go build`/`go install`
omits the `sql` row entirely (see [`INSTALL.md`](INSTALL.md#sql-support)).

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

`commit` and `diff`'s own flags — the two subcommands with a real flag
surface. `blame` and `languages` each take only `--porcelain`/`--help` (§
Blame and § Languages above, [`CODES.md`](CODES.md#output-records)); `log`
additionally takes `-p`/`--patch`, mutually exclusive with `--porcelain`, and
— only in its `--since`/`--until` shape — `--since`/`--until` themselves (§
Log and § Log by date and path above); `doctor`, `completion`, and `context`
take no flags beyond `--help`/`-h` (`completion` also takes its shell
argument; `context`'s fixed output shape is the point — § Context above).

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
| `--since DATE`, `--until DATE` | (`log`) Switch to date-bounded, unanchored history; presence of either selects this shape over `FILE:SYMBOL`. Forwarded to git's own `--since`/`--until` unparsed. |

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

`--porcelain` replaces both with stable tab-separated records — schema and
rationale in [`CODES.md`](CODES.md#output-records):

```text
auth.go<TAB>ValidateToken<TAB>12<TAB>3
```

The records are identical for `--dry-run` and for the commit it previews.

Repeatable `-m` gives subject and body without embedding newlines in one shell
argument:

```console
$ rgit commit -m "fix(auth): reject expired tokens" \
              -m "Tokens past exp were accepted because the clock check ran
before decode. Closes #42." \
              auth.go:ValidateToken
```

**Invalid combinations:** `--dry-run` + `--push`, `--staged`/`--unstaged` +
a revision argument (a range, or one or two bare revisions), `--staged` +
`--unstaged`, `--range` + a positional `A..B`/`A...B` range, `-m` + `-F`,
`--porcelain` + `--quiet` → exit 129.
Naming one path both as a path and as a symbol anchor → exit 5, in
whichever spelling: `--file` with
`--sym`, or the positional forms `greet.go greet.go:A`.

A `FILE:SYMBOL` anchor into a structured-data file (JSON, YAML, TOML) → exit
12: a spliced extent is not guaranteed to agree with the file's own grammar,
and nothing would fail at commit time to say so. Name the path instead —
`rgit commit package.json` stages the whole file, unaffected; `rgit diff`,
`rgit blame`, and `rgit log` still resolve the identical anchor, since none
of them writes a blob (see [`CODES.md`](CODES.md#exit-12-is-commits-alone)).

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
