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
| 128 | Fatal git / system failure (includes hook rejection, GPG failure) |
| 129 | Invalid usage (bad flags, missing message, no targets, path escape) |

128 and 129 follow git's own conventions. 1 is git's `--exit-code` convention
and is deliberately absent from the named constants: unlike every other status
here, its meaning is conditional on a flag rather than fixed.

The numeric bindings live in `internal/exitcode`, which spells no meaning of
its own — the constant names carry it, and this table defines it.

### Exit 6 is `commit`'s alone

`rgit diff` reports the identical disagreement as a `[warning]` on stderr and
still exits 0 (or 1 under `--exit-code`). A diff is a read-only report, and the
point of surfacing it there is that the caller learns of it while reading the
diff rather than mid-commit.

### A missing cross-check is never a failure

When no language server is reached, both commands print `[ts-only]` on stderr
and proceed — degraded resolution is normal, not an error
([`AGENTS.md`](../AGENTS.md#resolution-model)). `rgit diff --quiet` still prints
it, since `--quiet` suppresses the report on stdout, not diagnostics.

## Output records

Both commands emit plain text only. `--porcelain` replaces the aligned
human layout with stable tab-separated records, no header.

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

There is no `STATUS` column: an unchanged target is omitted from the listing
entirely (it gets its own stderr warning instead), so every record would carry
the same value. Records are identical for `--dry-run` and for the commit it
previews, and `--porcelain` replaces `git commit`'s own summary rather than
adding to it — exactly as `git commit --porcelain` does.

## Rules both forms obey

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

Per-target `+N/-M` counts sum to git's own insertion count in Go, but not
always in TypeScript or Python: an anchor absorbs a boundary separator only
where the formatter makes it mandatory — gofmt always writes one blank line
after the package clause and after the import block, while Prettier and Black
preserve whatever blank lines the author wrote. What `rgit` promises instead is
that `rgit diff` and `rgit commit --dry-run` agree row for row, in every
language.
