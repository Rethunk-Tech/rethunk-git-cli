# TODO

Future work, and the decisions that closed the door on work not being done.
Behaviour that ships is documented in [`docs/`](docs/); how the code works is
the code. What lives here is only what neither of those can hold: a measurement
that already settled a question. It also holds a design that is accepted but
not built.

Limitations that ship — unsupported languages, excluded cross-build targets,
constructs no anchor reaches — are in
[`docs/LIMITATIONS.md`](docs/LIMITATIONS.md), not here.

## `rgit show --json` needs a byte-safe encoding, not a TSV wrap

Decided: no `show --json` until a named consumer needs the extent
as JSON; `--porcelain` stays the only machine form. Reopen with that consumer.

`show --porcelain` length-frames extents (`FILE:ANCHOR<TAB>NBYTES` plus
exactly `NBYTES` bytes, `internal/app/show.go:33–36`) because a symbol
body can contain any byte, including a header-shaped line. `--json` as
used on `log`/`symbols` is UTF-8 text fields. Putting the extent in a
JSON string would require an encoding choice (UTF-8 with replacement,
base64, or `\u` escapes) that `--porcelain` does not need.

Do not add `--json` until that encoding is decided in `docs/CODES.md`
and a consumer exists. `--porcelain` / `--with-header` stay the
machine-readable path. Default one-anchor output stays raw bytes.

Rejected: JSON that assumes the extent is valid UTF-8. Rejected: wrapping
only the header and leaving the body on stdout after the object.

### Read

1. `internal/app/show.go:21` help, `:33` `--porcelain`, `:111` `framed`.
2. `docs/CODES.md` `rgit show --porcelain` — length-framed dialect, not
   TSV+newline.
3. `docs/CODES.md:137` — `--json` is defined as one object for
   languages/doctor/context/symbols/log, none of which carry arbitrary
   file bytes.

### Traps

- **A Go `json.Encoder` will `\u`escape `<` if `SetEscapeHTML` stays
  default-true.** Even with it off, invalid UTF-8 becomes U+FFFD.
  Base64 avoids that and is a new dialect, not "the porcelain fields."
- **Single-anchor default is unframed bytes for piping.** JSON would
  always frame, like `--porcelain`, and must not change the no-flag
  path.

### Acceptance

No `--json` on `show` until `CODES.md` names the byte encoding and a
test pins that a fixture with non-UTF-8 bytes round-trips. `--porcelain`
remains the scripting form.

## `rgit context --porcelain` would alias the default stream

`docs/CODES.md:144` states `context` has no `--porcelain`; `--json`
encodes the same record stream. The default output is already TSV with a
leading type tag (`internal/app/context.go:42`). `--porcelain` as an
unknown flag is exit 129 (`context.go:125–130`,
`internal/app/context_test.go` `--json --porcelain`). Adding
`--porcelain` as a no-op alias of the default, or as a second encoding
beside `--json`, either changes that 129 or invents a dialect the
default already is.

Leave the refusal. If a caller wants TSV, they omit `--json`. If a
caller wants an explicit flag name, that is a contract change to document
in `CODES.md` (the three `--porcelain` dialects table) and in completion
(`rgitContextFlags`), not a silent alias.

### Read

1. `docs/CODES.md:144` — "`context` has no `--porcelain`".
2. `internal/app/context.go:27` help (`[--json]` only), `:125` any other
   argument is extra.
3. `internal/app/context_test.go` the `--json --porcelain` 129 case.
4. `internal/app/completion.go` `rgitContextFlags = "--json -h --help"`.

### Traps

- **Making `--porcelain` succeed as a synonym of the default changes
  exit 129 tests and the "records are fixed; any other argument is
  refused" rule in `docs/USAGE.md` § Context.**
- **Do not emit a headerless copy that drops the type tags.** `F` rows
  would then collide with `rgit diff --porcelain`, which is a different
  dialect on purpose.

### Acceptance

`rgit context --porcelain` remains exit 129. `--json` and the text
stream stay as documented. No new flag.
