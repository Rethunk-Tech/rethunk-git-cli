# Validation spikes (reference only — delete after porting)

Throwaway Python prototypes that validated DESIGN.md's mechanisms before any Go
was written. They are **not** part of the build and have no CI. Their value is as
the executable source for the Go test suite; delete this directory once
`index_test.go` and `resolver_test.go` cover the same cases.

| File | Validates |
| --- | --- |
| `synth.py` | Blob-synthesis library: extent extraction, doc attribution, splice, insertion |
| `test_basic.py` | Happy paths — 18 assertions |
| `adversarial.py` | Cases designed to break it — 20 assertions |
| `pseudo.py` | `@imports` / `@header` / `@toplevel` addressability |
| `lsp.py` | Drives a live `gopls`, dumps `documentSymbol` ranges |
| `compare.py` | Declaration-only normalization vs live gopls ranges |

Run: `uv run --with tree-sitter --with tree-sitter-go python <file>.py`
