# Cutover from the rethunk-git MCP

`rgit` replaced the `rethunk-git` MCP server's `commit` and `diff` surfaces on
2026-07-28. The remaining 22 tools were retired in favour of plain `git`, which
measured cheaper on every one. Rationale and the measured saving:
[`design.md`](design.md).

## Standing instruction

This is what replaced the routing prose — **50 tokens against 659**, a 92%
reduction on a cost paid every session:

> Commit: `rgit diff`, then `rgit commit -m "type(scope): subject" TARGET...`.
> TARGET = path or `FILE:SYMBOL`. All other git: plain `git`. One repo per call.

## What changed

`rgit` is built and installed on `PATH`, with `Bash(rgit:*)` allowlisted and
`gopls`, `vtsls`, and `pyright` provisioned.

Agent instructions were rewritten across `~/.claude`: the routing table in
`CLAUDE.md`, `good-version-control` (including the checkpoint script, which now
recognises `rgit commit`), `orchestrate`'s canonical commit paste block and its
protocol files, `git-push-order`, and `repo-ops`. The `batch_commit`
requirement and the "MCP-only, no shell fallback" rule are gone — there is no
MCP left for local git to route to. `CLAUDE.md`'s claim that MCP commits landed
as "Bastion Agent" went with them; it was verified false across 20 commits,
which used ambient git config.

Cursor needed no separate work: it reads Claude Code's files, and
`~/.cursor/rules/` is empty.

The `rethunk-github` MCP is **unaffected** and stays.

## What was deliberately not done

The plugin is **disabled, not uninstalled** — `rethunk-git@rethunk-plugins` is
`false` in `settings.json` while the install record and cache remain. That is
the accurate state, since it is still on disk, and it is reversible where
hand-editing the harness's own install manifest is not.

`Rethunk-AI/mcp-multi-root-git` is **still on GitHub**, untouched. Deleting a
public repository is irreversible and nothing requires it; the server is simply
no longer registered.
