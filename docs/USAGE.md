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
  newfile.go   @header             +2/-0
               NewFunc             +13/-0
  config.ini   (untracked)         +4/-0   -> use --file config.ini
  script.sh    (mode 644->755)     +0/-0   -> use rgit commit script.sh
  logo.png     (binary)            -/-

$ rgit commit -m "auth: reject expired tokens" \
    auth.go:ValidateToken auth.go:@imports
```

`newfile.go` is untracked but attributes per symbol exactly like a brand-new
tracked file — no HEAD blob to diff against, so every declared symbol is
wholly new. `config.ini` stays a single collapsed `(untracked)` row because it
has no grammar to attribute by; a binary untracked file collapses the same way.

## Global flags

Three, all given **before** the command: `-C <path>`, `--version`, and
`-h`/`--help` (the latter two under § Help).

`-C <path>` runs as if `rgit` had been started in `<path>`, exactly as
`git -C <path>` does — the repository is discovered from there, and a
relative pathspec or anchor resolves against it:

```bash
rgit -C ~/src/api diff --porcelain
rgit -C ~/src/api commit -m "fix(auth): reject expired" auth.go:ValidateToken
```

It must come before the command, because `git commit -C <commit>` already
means "reuse that commit's message" and the two spellings must not collide.
Repeats accumulate, each read relative to the last (`-C a -C b` is `-C a/b`,
an absolute path resetting), and `-C ""` is a no-op — git's own semantics in
each case.

The directory is checked before the command runs, so both refusals reach even
a command that never opens a repository: no directory argument is a usage
error (129), and a directory `rgit` cannot enter is fatal (128). The glued
`-C<path>` spelling is a usage error, as it is in git.

## Argument shape

Positional arguments carry everything: pathspecs, revisions, and symbol anchors.
`--file` and `--sym` exist as explicit equivalents for scripting and
disambiguation, never as requirements.

All git pathspec magic is **leading**-colon (`:(exclude)`, `:(glob)`, `:/`). An
*interior* colon is therefore free for `FILE:NAME` and needs no flag.

**For `diff`, a bare `A..B` or `A...B` positional is pulled out before the
precedence table below ever runs.** `git rev-parse --verify` (rule 3) fails
outright on range syntax, so a range token would otherwise fall through every
rule to an unresolvable-argument error. Only a token with no existing
worktree, index, or HEAD path of that exact name is treated as a range — git
forbids `..` in ref names, but a legitimate relative pathspec like
`../shared/util.go` also contains `..`, so path existence wins over the
heuristic. At most one such token is accepted per
invocation; a second is a usage error (exit 129). `--range` is the explicit
form of the same value and is mutually exclusive with the positional one.

Resolution precedence, first match wins:

| # | Test | Result |
| --- | --- | --- |
| 1 | Appears after `--` | Pathspec, always |
| 2 | Starts with `:` | Git pathspec magic, passed through verbatim |
| 3 | *(`diff` only)* resolves via `git rev-parse --verify` | Revision, or a `rev:path` blob reference — see below |
| 4 | Names a path existing in the worktree, index, or HEAD | Pathspec |
| 5 | Splits at the last `:` into an existing path + a name | Symbol anchor |
| 6 | None of the above | Error listing each interpretation tried |

No escaping is ever needed. `src/notes:draft.md` is a legal path, so rule 4
claims it; `auth.go:ValidateToken` names nothing, so rule 5 splits it. Use
`--sym` or `--file` to force the reading when a repo genuinely has both.

Rule 3 splits at the **first** colon (`HEAD~1:f.go` is revision `HEAD~1`,
path `f.go`), where rule 5 splits at the **last** — each rule uses the
split git's own syntax needs at that position, not a shared convention.
`rev:path` is real `git diff` syntax (`git diff HEAD~1:f.go HEAD:f.go`
compares two blobs directly), and rule 3 exists first so it is not misread as
a `FILE:NAME` anchor by rule 5.

**Exactly two `rev:path` positionals naming the identical path** compare
that one file across two revisions, attributed by symbol like any other
scope: `rgit diff HEAD~1:auth.go HEAD:auth.go`. `--sym` still narrows
rendering afterward. This is the one scope with no trailing pathspec of its
own — `git diff <blob> <blob>` takes none, and the two blob refs already name
the file completely — so combining it with `--file`/a pathspec,
`--staged`/`--unstaged`, a revision range, or bare revision positionals is
refused (exit 129). A single, unpaired `rev:path` positional is refused the
same way: comparing two *arbitrary* blobs by revision names no single changed
file to group rows under. Name the file directly when you only meant a plain
revision-scoped diff.

**Paths are relative to the directory you run in, not the repository root** —
git's own rule. From a subdirectory, naming a.go stages that directory's a.go
under the repo root, and naming the root-relative path from there doubles the
subdirectory prefix and finds nothing. Leading-colon magic is the exception
git already defines: `:/` and `:(top)` are root-relative wherever you stand.
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
so everything staged or untracked lists as an addition, and `rgit commit`
writes the root commit.

## Output

Plain text only. The default is the aligned layout shown above;
`--porcelain` replaces it with stable tab-separated records.

**The record layouts, the `STATUS` tokens, and the ordering guarantee are
specified in [`CODES.md`](CODES.md#output-records).**

A `chmod +x` with no content edit produces no changed symbols, so `rgit diff`
lists it as a `MODE` entry — the file is never falsely reported clean. Stage it
with a pathspec (`rgit commit script.sh`); `--sym` cannot express a mode change.

`--sym FILE:member` on a member of a class/container new to `HEAD` prints a
`[warning]` on stderr: `rgit commit` would stage the whole container, not just
the named member, since there is no way to add a member to a container that
does not exist yet (see [`ANCHORS.md`](ANCHORS.md#qualification)). The
unfiltered listing never warns — every sibling already has its own row there.

`-p`/`--patch` appends git's own real patch body after the aligned/porcelain
report, unmodified — not a second diff format `rgit` invents, the same
framing as `log -p` and `blame --porcelain`. Both come from the same
comparison, so it covers the identical scope and pathspec filter as the report
above it. Mutually exclusive with `--porcelain`.

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
There is no revision argument. When the worktree copy exists, blame reads it,
the same file a bare `git blame FILE` would; when it has been deleted, rgit
resolves the extent from `HEAD` and blames `HEAD`'s blob for that line range.

`--follow-rename` follows the symbol across rename boundaries using the same
rename-boundary re-resolution as `log --follow-rename`: when git identifies a
rename, rgit resolves the symbol once against the pre-rename blob and blames
that segment's line range. It does not re-parse once per commit. With this
flag, every resolution uses the `HEAD` blob, never the dirty worktree; without
it, blame keeps the behavior above, including the `HEAD` fallback.

An anchor that does not resolve is **never** silently widened to a whole-file
blame — it is exit 3 (unresolvable), 4 (ambiguous), or 9 (unsupported
language), the same codes `commit` and `diff --sym` already give the
identical anchor. See [`CODES.md`](CODES.md#exit-codes).

`-p` is accepted as an exact alias of `--porcelain`, matching git blame's own
flag: unlike `git log -p` or `git diff -p`, git blame's `-p` already means
`--porcelain`, not "patch" — blame annotates lines, it does not diff them, so
there is no separate patch mode to opt into.

`--porcelain` passes straight through to git's own `git blame --porcelain`
output, unmodified — not a second record format rgit invents. The default is
likewise git's own human-readable blame output. See
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

`--since=DATE` and `--until=DATE` may be added to this anchor form; the
`FILE:SYMBOL` positional keeps its symbol-scoped meaning, and the bounds are
forwarded to git's own `git log -L`. `-n N`/`--max-count=N` can be combined
with it as well. `--follow-rename` may be combined with either date bound;
each rename segment receives the same filters. Under `--follow-rename`,
`-n`/`--max-count` applies independently to each rename segment, so the total
can exceed `N` when history crosses multiple renames.

Unlike default `blame` (worktree first), the anchor is resolved against
**`HEAD`, not the worktree**: history is a question about what has already
been committed, and `git log -L` itself walks `HEAD`'s own history with no
notion of the worktree at all. (`blame --follow-rename` uses the same
`HEAD`-blob rule.) A symbol already deleted from the worktree, but still
present in `HEAD`, therefore keeps its history reachable.

`git log -L` already follows a rename on its own whenever git's own content
similarity detects one, the same as `git log --follow` — but it tracks the
*line range* through the rename by diff, not by re-parsing the old file, so
a rename that also reshuffles the symbol's position (or otherwise breaks the
line-level correspondence) can silently stop short. `--follow-rename`
re-resolves the anchor with tree-sitter against the pre-rename blob at each
rename boundary instead of trusting that line-tracking, and continues under
the pre-rename path from there. Either way, the anchor must resolve at `HEAD`
under the file's **current** name — querying by a prior name directly fails
at argument classification. See
[`LIMITATIONS.md`](LIMITATIONS.md#history-across-renames).

An anchor that does not resolve is exit 3 (unresolvable), 4 (ambiguous), or 9
(unsupported language) — the same codes `blame`, `commit`, and `diff --sym`
already give the identical anchor. See [`CODES.md`](CODES.md#exit-codes).

`--porcelain` lists stable tab-separated `HASH<TAB>SUBJECT` records instead of
the aligned `<abbrev-hash> <subject>` default, no header. Mutually exclusive
with `-p`/`--patch`. See [`CODES.md`](CODES.md#output-records).

### Log across renames

```console
$ rgit log new.go:Foo --follow-rename
b6f3975 edit
2793187 rename + reorder
c8fdd8a init
```

`--follow-rename` continues a symbol's history past a rename that plain `log
FILE:SYMBOL` stops at, one rename boundary at a time (not a per-commit
re-parse). Without the flag, behaviour is unchanged. See
[`LIMITATIONS.md`](LIMITATIONS.md#history-across-renames).

### Log by date and path

```console
$ rgit log --since=2024-01-01 -n 5 -- src/auth
a1b2c3d fix(auth): reject expired tokens
```

`--since=DATE` or `--until=DATE` (either alone, or together) selects ordinary
git history bounded by date only when no positional is a `FILE:SYMBOL`
anchor. With an anchor, the symbol-scoped form above remains selected.
Otherwise, one or more trailing positionals are pathspecs. Values are
forwarded to git's own `--since`/`--until` unparsed, so anything git accepts
there (`"2024-01-01"`, `"2 weeks ago"`) works here too. With no paths, the
unanchored form is the whole repository's history in that window, matching
plain `git log --since=DATE`.

`-n N`/`--max-count=N` limits either form to at most `N` commits, forwarding
git's own count limit. Without either spelling, the history remains unbounded.

This is the one `git log` carve-out `rgit`'s own "the tree is only ever
inspected through `rgit`" convention otherwise has to make for a plain
`git log --since=... -- <paths>` — closed by giving `rgit log` a second
invocation shape rather than a second command. `--porcelain` and `-p`/
`--patch` behave identically to the `FILE:SYMBOL` form above.

## Context

```text
B<TAB>main<TAB>origin/main<TAB>0<TAB>2
S<TAB>merge
W<TAB>ts-only
W<TAB>warning<TAB>extent disagreement reported on stderr
F<TAB>auth.go<TAB>ValidateToken<TAB>MOD<TAB>12<TAB>3
F<TAB>config.ini<TAB><TAB>UNTRACKED<TAB>4<TAB>0
C<TAB>a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2<TAB>fix(auth): reject expired tokens
C<TAB>9e8f7d6c5b4a9e8f7d6c5b4a9e8f7d6c5b4a9e8f<TAB>feat(auth): add ValidateToken
```

`rgit context` is one-call repository orientation for an agent's first turn:
the current branch and its upstream tracking status, then the same per-file,
per-symbol diffstat `rgit diff` itself reports for everything committable,
then recent commit subjects — as a single, fixed-shape record stream. It
replaces the separate `status`, `diff --stat`, `diff`, and `log` calls an
agent would otherwise make before editing, each billed as its own subprocess
call.

**The output shape is fixed and takes no flags beyond `--help`.** A command
with options becomes `git status` with extra steps — see
[`specs/design.md`](../specs/design.md#commands) for why the shape stays
fixed rather than growing one. Seven record types, tab-separated, no header:

| Record | Fields | Meaning |
| --- | --- | --- |
| `B` | `BRANCH`, `UPSTREAM`, `AHEAD`, `BEHIND` | At most one, first when present: the current branch. `UPSTREAM` is empty and `AHEAD`/`BEHIND` are both `0` with no upstream configured. Absent on an unborn branch and replaced by `H` on detached HEAD |
| `H` | `SHA` | One detached-HEAD record with the full commit object id; absent on an unborn branch |
| `S` | `OP` | One active sequencer operation: `merge`, `cherry-pick`, `revert`, `rebase`, or `bisect` |
| `W` | `ts-only` | One when at least one file had symbols to cross-check but no live language server was reached. The identical `[ts-only]` notice remains on stderr |
| `W` | `stash` | One when `refs/stash` exists. Absent when the stash is empty |
| `W` | `sparse` | One when `core.sparseCheckout` is true. Absent on a full checkout |
| `W` | `warning`, `TEXT` | One per non-fatal diff warning. `TEXT` is the warning body without the human `[warning]` prefix; the identical `[warning] TEXT` line remains on stderr |
| `F` | `FILE`, `SYMBOL`, `STATUS`, `ADDED`, `DELETED` | One per `rgit diff --porcelain` row — identical fields, plus this stream's own leading type tag |
| `C` | `HASH`, `SUBJECT` | One per recent commit, newest first, bounded to the last 20 |
| `X` | `TRUNCATED`, `COUNT` | At most one, always last: this many records were withheld to hold the byte budget |

The diff half is pure composition, not a second attribution path: `rgit
diff`'s own default scope, rendered through the same `--porcelain` records and
re-tagged per line.

**The whole stream is capped at 16 KiB.** `B` or `H`, when present, sorts first
— a single record that costs the budget almost nothing — then `S`, `W`
diagnostics and `F` rows. `F` rows survive truncation before `C` rows do: the
diff section is the unbounded, actionable half, where commits are already
bounded up front (the most recent 20, via git's own history limit) and cost
little to drop. Truncation happens at the byte boundary, with a trailing
`X` record naming how many rows were withheld. See
[`../specs/design.md`](../specs/design.md#commands) for the reasoning. See
[`CODES.md`](CODES.md#output-records) for the exact record grammar.

## Help

`rgit --help`, `rgit -h`, and `rgit help` print the top-level command list on
stdout and exit 0. `rgit diff --help` / `-h` and `rgit commit --help` / `-h`
print that command's own flags the same way, generated from the flag set itself
so the two cannot drift. `rgit blame --help`, `rgit log --help`, `rgit
context --help`, `rgit languages --help`, `rgit doctor --help`, `rgit
symbols --help`, and `rgit completion --help` (each also accepting `-h`)
print their own hand-written usage text instead — surfaces small enough that a
generated rendering was not worth building. A bare `rgit` (no command at all)
is a usage error, not a help request — see § Exit codes.

`rgit --version` prints `rgit <version>` on its first line and exits 0:

```console
$ rgit --version
rgit v2.0.0
optional grammars: sql
built with go1.27.1, linux/amd64
```

The version is stamped at build time (`-ldflags "-X main.version=vX.Y.Z"`),
which `make install` and `cmd/rgit-install` both do. Without it, the version
is recovered from what the toolchain embeds: a `go install …@vX.Y.Z` from the
module proxy reports that tag, and a plain `go build` inside a checkout
reports the abbreviated revision with `-dirty` where it applies — the same
shape `git describe --tags --always --dirty` produces. Only a build with
neither, such as one made outside a checkout or with `-buildvcs=false`, reads
`dev`.

That first line is the only one scripts should parse. The lines after it are
free to grow: a second reports which optional/gated grammars this exact binary
was compiled with (see § Languages below), so it can change independently of
the version, and a third names the Go toolchain and platform it was built
for — two binaries can report the same version and still differ there, which
is what a bug report needs.

## Languages

`rgit languages` lists every grammar compiled into the running binary: name,
file extensions, and whether it is present only because a build tag selected
it.

SQL is the first grammar gated this way — `-tags rgit_sql`, generated by
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

`--porcelain` replaces the aligned listing with stable
`NAME<TAB>EXTENSIONS<TAB>GATED<TAB>CROSS-CHECK` records; the first three
columns retain the meanings above.

`CROSS-CHECK` is `wired` for grammars with a language-server catalog entry and
`ts-only` for TOML and SQL. It reports compile-time design wiring, not whether
a server is reachable in this invocation; `rgit doctor` reports that
environment status.

A `.sql` anchor on a binary built without `rgit_sql` still fails with exit 9
("no grammar registered"), the same as any genuinely unsupported language —
but unlike one this resolver has never supported, the message also names the
build tag and points at rebuilding.

**`--in-repo` narrows the listing to grammars with at least one matching file
in the current repository** — advisory only, since the binary still contains
every compiled-in grammar regardless of what a given repo uses. A monorepo
with only `.go` files omits `python`, `css`, and the rest even though a Python
or CSS anchor would resolve fine elsewhere.

Detection reuses the same extension-then-shebang sequence every worktree file
gets; tracked files fall back to a bounded `HEAD` sample when their worktree
copy is absent, and untracked files (excluding ignored ones) count too.
Requires a git repo, unlike the plain form above.

## Doctor

`rgit doctor` reports environment health: git on `PATH` (the one thing rgit
cannot run without) and its resolved version against the floor
[`INSTALL.md`](INSTALL.md#prerequisites) documents, the optional tree-sitter
CLI, which language servers from
[`INSTALL.md`](INSTALL.md#language-servers) answer on `PATH` for the extent
cross-check, and the same grammar listing `rgit languages` prints.

It exits 0 unless rgit genuinely cannot function — a missing language server,
the tree-sitter CLI, or a below-floor git version is informational, since
degraded `[ts-only]` resolution is normal and documented, not an error (see
[`CODES.md`](CODES.md)).

`--porcelain` lists the environment and language-server checks as stable
tab-separated records instead, for agents and CI that want a parseable stream
rather than the aligned human report — see
[`CODES.md`](CODES.md#rgit-doctor---porcelain). It does not repeat the grammar
listing; `rgit languages --porcelain` already owns that.

**`--deep` dials each on-PATH server for real** — the identical
initialize/initialized handshake `rgit diff`/`rgit commit` already perform for
the cross-check — and appends `(reachable)` or `(degraded — handshake timed
out or unanswered)` to its detail column. Being on `PATH` is not proof a
binary answers; a stale daemon or a cold index both look identical to a plain
`LookPath` check. Slower for exactly that reason, and not the default: a cold
CI host with nothing installed should stay instant. A server not on `PATH` is
never dialed — `MISSING` already says everything `--deep` could add.

## Symbols

```console
$ rgit symbols auth.go
@imports
ValidateToken
```

`rgit symbols [--for-commit] [--with-lines] <file>` lists every declared symbol
that can be resolved from the worktree file, or from the file's `HEAD` blob when
the worktree copy has been deleted, one symbol per line. A path present in
neither the worktree nor `HEAD` still errors. `--for-commit` omits
structured-data symbols that `rgit commit` refuses; it still exits successfully
without output when the file is a supported structured-data file.

`--with-lines` emits each symbol's line range ahead of its anchor instead of the
bare name, and composes with `--for-commit`:

```console
$ rgit symbols --with-lines auth.go
1,3 @imports
12,27 ValidateToken
```

The range is git's own `-L start,end` range for that symbol — the same range
`rgit blame FILE:SYMBOL` and `rgit log FILE:SYMBOL` bound themselves to — so it
can be handed straight to a reader that takes a line range. The record format is
in [`CODES.md`](CODES.md#rgit-symbols---with-lines).

`--help`/`-h` prints the command's usage text and exits 0. Exactly one file
argument is required; a missing or extra argument prints the usage text to
stderr and exits with the invalid-usage code. A file that cannot be read from
either source or whose symbols cannot be resolved exits with the git-failure
code, while an unsupported language exits with the unsupported-language code.
See [`CODES.md`](CODES.md#exit-codes).

When `core.ignorecase` is true, extension lookup is case-insensitive for
`symbols`, `commit`, `diff`, `blame`, `log`, and `languages`; the path itself
remains unchanged.

## Shell completion

`rgit completion bash`, `rgit completion zsh`, `rgit completion fish`, and
`rgit completion pwsh` print a completion script for that shell to stdout;
nothing else is written.

PowerShell registration instructions are in
[`INSTALL.md`](INSTALL.md#shell-completion).

It completes subcommands, each subcommand's own flags, plain file paths, and —
the useful part — symbol names after `FILE:`, by shelling back out to `rgit
symbols FILE`. That read-only command reads the worktree file or the `HEAD`
blob (§ Symbols above), so completion works for clean, dirty, and
deleted-but-tracked files. If the call fails for any reason — the working
directory is not a repository, `rgit` is not on `PATH`, anything — completion
offers nothing rather than printing to the prompt.

Completion for `commit` uses `rgit symbols --for-commit FILE`, which omits
structured-data symbols that `commit` refuses; other commands use the complete
list.

The fish and pwsh scripts drive the identical logic through their shells' own
completion models — fish's dynamic candidate function registered with
`complete` and PowerShell's native completer, rather than bash's
`COMPREPLY`/`compgen` or zsh's `compadd` — but complete the same things the
same way, including the `-C <path>` walk and the `FILE:SYMBOL` lookup.

An unrecognized or missing shell argument is a usage error, same table as
everywhere else. Install instructions: [`INSTALL.md`](INSTALL.md#shell-completion).

## Flags

`commit` and `diff` document their flags in the table below; hand-parsed meta
commands carry smaller surfaces of their own: `blame` takes
`-p`/`--porcelain`, `--follow-rename`, and
`--help`; `languages` takes `--porcelain`, `--in-repo`, and `--help` (§
Blame and § Languages above, [`CODES.md`](CODES.md#output-records)); `log`
additionally takes `-p`/`--patch`, mutually exclusive with `--porcelain`,
`--follow-rename`, `-n`/`--max-count`, and — only in its
`--since`/`--until` shape — `--since`/`--until` themselves (§ Log and § Log
by date and path above); `symbols` takes `--for-commit`, `--with-lines`, and
`--help` (§ Symbols above); `doctor` takes `--porcelain`, `--deep`, and `--help`;
`completion` and `context` take no flags beyond `--help`/`-h`
(`completion` also takes its shell argument; `context`'s fixed output shape
is the point — § Context above).

| Flag | Behavior |
| --- | --- |
| `--sym FILE:NAME` | Explicit anchor form; equivalent to a bare `FILE:NAME` positional. Repeatable. |
| `--file PATH` | Explicit pathspec form; equivalent to a bare positional. Repeatable. |
| `--pathspec-from-file FILE` | (`commit`, `diff`) Read one target per line from `FILE`, or from stdin when `FILE` is `-`. Empty lines are skipped; targets use the same precedence rules as positionals. Mutually exclusive with `-F -` when both would consume stdin (exit 129). |
| `--pathspec-file-nul` | (`commit`, `diff`) Read NUL-delimited targets from `--pathspec-from-file` instead of newline-delimited lines. Empty records are skipped. |
| `-m MSG`, `--message MSG` | Commit message. **Repeatable** — values join as blank-line-separated paragraphs, as git does. |
| `-F FILE`, `--message-file FILE` | Read the message from a file, or `-` for stdin. Mutually exclusive with `-m`. |
| `-s`, `--signoff` | Append `Signed-off-by:`. Forwarded to `git commit`. |
| `--trailer TOKEN:VALUE` | Append a trailer (`Refs:`, `Co-authored-by:`). Repeatable, forwarded. |
| `--amend` | Amend the previous commit. Anchors stage into it as they would a new commit. With neither `-m` nor `-F`, reuses HEAD's message unchanged (`--no-edit`) — `rgit` never opens an editor, so that is the only message an unattended `--amend` can have. Give `-m`/`-F` to replace it as usual. With no targets, skips staging and amends the index as it stands. |
| `--allow-empty` | Permit a commit with no changes. Suppresses exit 11. With no targets, skips staging and commits the index as it stands; still requires `-m`/`-F` unless another auto-message flag is set. |
| `-o`, `--only` | Commit only the named targets, leaving other staged paths in the index. Requires at least one target unless combined with `--amend`; anchors still synthesize their named extents before the commit. |
| `--reuse-message=<commit>` | Reuse that commit's log message and authorship (`git commit --reuse-message`). Long form only — global `-C` is directory chdir and stays before the command. Mutually exclusive with `-m` the way git is (`-m` and `-C` cannot be used together). Does not require a separate `-m`. |
| `--reedit-message` | Refused (exit 129). `rgit` never opens an editor; use `--reuse-message`. |
| `--push` | Push upstream after a successful commit. No rollback on push failure. If the branch has no upstream configured, the exit-8 message names it and the fix (`git push -u origin <branch>`, or `push.autoSetupRemote`) — `rgit` never adds `-u` itself. |
| `--dry-run` | Preview only. Writes no objects, stages nothing, runs no hooks. Lists each target it resolved with that symbol's `+N/-M`, using the same counts as `rgit diff`. |
| `--no-verify` | Skip git hooks (standard git meaning). Hooks run by default. |
| `--fixup <commit>` | Autosquash fixup for `<commit>` (also accepts `amend:<commit>`/`reword:<commit>`, forwarded verbatim). Generates its own subject, so `-m`/`-F` are not required; either still appends as an extra body paragraph rather than conflicting. |
| `--squash <commit>` | Autosquash squash for `<commit>`. Same message rule as `--fixup`. |
| `--author <author>` | Override the commit author. Plain forwarding. |
| `--date <date>` | Override the commit date. Plain forwarding. |
| `--reset-author` | Take the author identity from the committer instead of carrying the original forward. Plain forwarding; git accepts it only with `--amend` or `--fixup=amend:`, and `rgit` does not police the combination. |
| `--porcelain` | (`commit`) After a successful real commit, emit a leading `H<TAB>SHA` record with the full commit object id, followed by stable tab-separated target records instead of the aligned listing. Replaces `git commit`'s own summary rather than adding to it, exactly as `git commit --porcelain` does. Works with `--dry-run`, which emits target records alone with no `H` record or preamble. |
| `-q`, `--quiet` | (`commit`) Suppress the summary and the target listing. stdout is empty; warnings, notices and hook output still go to stderr, as under git's own `-q`. |
| `-S`, `-S<key-id>`, `--gpg-sign`, `--gpg-sign=<key-id>` | GPG-sign the commit, with the configured default key or an explicit one. See the note below on how `-S` is parsed. |
| `--no-gpg-sign` | Do not GPG-sign, overriding `commit.gpgsign=true`. |
| `--unstaged` | (`diff`) Worktree vs index — git's bare `diff`. |
| `--staged`, `--cached` | (`diff`) Index vs `HEAD`. Both spellings. |
| `--range REVS` | (`diff`) Explicit form of a positional revision range. |
| `--porcelain` | (`diff`) Stable tab-separated records. |
| `--exit-code` | (`diff`) Exit 1 when anything is committable, 0 when clean. |
| `--quiet` | (`diff`) Implies `--exit-code` and suppresses output. |
| `-p`, `--patch` | (`diff`) Append git's own real patch body after the report. Suppressed by `--quiet`, mutually exclusive with `--porcelain`. |
| `--since DATE`, `--until DATE` | (`log`) Bound history by date. A `FILE:SYMBOL` positional keeps the anchor form; otherwise these select unanchored path-scoped history. Forwarded to git's own `--since`/`--until` unparsed. |
| `-n N`, `--max-count=N` | (`log`) Limit either history form to at most `N` commits; under `--follow-rename`, applies independently per rename segment, so the total may exceed `N`. Forwarded to git's own count limit; omitted by default, so history is unbounded. |

`commit` requires a message (`-m` or `-F`) unless `--amend`, `--fixup`,
`--squash`, or `--reuse-message` is given — each supplies its own message
(`--amend` reuses HEAD's via `--no-edit`; `--fixup`/`--squash` generate
`fixup!`/`squash! <subject>`; `--reuse-message` takes the named commit's,
exactly as plain `git commit` does). It also requires at least one target,
unless `--amend`, `--allow-empty`, `--fixup`, `--squash`, or `--reuse-message`
is set: zero targets means skip staging and operate on the index as it stands,
not `git add -A`. During an in-progress merge, cherry-pick, or revert,
omitting `-m` and `-F` also reuses Git's generated message via `--no-edit`.

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
output is passed through too. `--dry-run` prints the same listing, so a
preview and the commit it previews are comparable line for line.

`--porcelain` replaces both with stable tab-separated records — a successful
real commit starts with its full commit object id, then the target rows. The
schema and rationale are in [`CODES.md`](CODES.md#output-records):

```text
H<TAB>SHA
H<TAB>a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2
auth.go<TAB>ValidateToken<TAB>12<TAB>3
```

The target records are identical for `--dry-run` and for the commit it
previews. Dry-run output has no `H` record; an `--allow-empty` commit emits
`H` even when it has no target rows. `--quiet` continues to leave stdout empty.

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
`--porcelain` + `--quiet` (`diff`), `--porcelain` + `-p`/`--patch` (`diff`,
`log`) → exit 129.
Naming one path both as a path and as a symbol anchor → exit 5, in whichever
spelling: `--file` with `--sym`, or the positional forms `greet.go
greet.go:A`.

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

- Work you staged before invoking `rgit` **comes along** with the commit, unless `--only` is given.
- A hook rejecting the commit **leaves staging in place**; nothing is rolled back.
- Hooks are not policed — a hook may stage paths you did not name, exactly as
  under plain `git commit`. Use `--no-verify` to disable them.
- During an in-progress merge, cherry-pick, or revert, omitting `-m` and `-F`
  uses Git's `--no-edit` message reuse, which `-m` or `-F` overrides; rebase
  remains `git rebase --continue` and still requires a message.
- `commit.cleanup` and `commit.gpgsign` are honoured as configuration, and
  `--gpg-sign`/`--no-gpg-sign` override either. `commit.template` is **not**
  honoured — templates prefill an editor and `rgit` never opens one.
- `rgit` never adds `--set-upstream` to a push on its own initiative, even for
  a branch with none configured — see `--push` above.

When stdin is not a terminal, `rgit` sets `GIT_TERMINAL_PROMPT=0` so a
credential or GPG prompt fails fast instead of hanging.

## Ordinal-anchor warnings

An ordinal anchor (`auth.go:init#2`, [`ANCHORS.md`](ANCHORS.md#qualification))
is positional — inserting a symbol above it repoints which one it names.
Resolving one prints an advisory on stderr, never fails the command:

```text
[warning] anchor 'dup.go:init#2' is positional; inserting a symbol above it repoints it -- qualify it where the language allows
```

`rgit commit`, `rgit diff --sym`, `rgit blame`, and `rgit log` all print it
for the identical anchor — the same advisory everywhere an ordinal
resolves, not only at commit time. A bare or container-qualified anchor
that merely happens to match the same symbol never warns.

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
