# Cutover from the rethunk-git MCP

`rgit` replaces the `rethunk-git` MCP server's `commit` and `diff` surfaces.
The remaining 22 tools are retired in favour of plain `git`, which measured
cheaper on every one. Rationale: [`design.md`](design.md).

**Phased gate.** Complete every item below *before* deleting the MCP server.
Committing must remain possible throughout — that means `rgit` on `PATH` and
rewritten agent instructions land first. Plain `git` stays allowlisted for
everything `rgit` does not cover.

## Standing instruction

Replaces ~431 tokens of MCP routing prose with 46 (cl100k, measured):

> Commit: `rgit diff`, then `rgit commit -m "type(scope): subject" TARGET...`.
> TARGET = path or `FILE:SYMBOL`. All other git: plain `git`. One repo per call.

## Binary & PATH

- [ ] Build and install `rgit` to `PATH` — see [`../docs/INSTALL.md`](../docs/INSTALL.md)
- [ ] Provision language servers used in your repos (`gopls`, `vtsls`, `pyright`)
- [ ] Allowlist `Bash(rgit:*)` in agent settings wherever shell git rules apply

## Agent instructions & skills

All of these must land **before** the MCP is deleted.

- [ ] `~/.claude/CLAUDE.md` — replace MCP commit/diff routing with `rgit`; remove the "MCP commits as Bastion Agent" claim (**verified false** — commits used ambient git config); remove multi-root MCP guidance; point reads at `git` / `git -C`
- [ ] Local workspace rules (`.cursor/rules/`) and prompts — document `rgit` usage
- [ ] `~/.claude/skills/good-version-control/SKILL.md` + `conventions.md` — `rgit` for commit/diff, plain git for status/log, drop the `batch_commit` requirement
- [ ] `~/.claude/skills/good-version-control/scripts/commit-checkpoint.py` — track `rgit commit` instead of `batch_commit` tool ids
- [ ] `~/.claude/skills/orchestrate/commits.md` + `parallel-protocol.md` — worker commits via `rgit`; drop MCP `batch_commit` fences
- [ ] `~/.claude/skills/orchestrate/SKILL.md` — same
- [ ] `~/.claude/skills/git-push-order/SKILL.md` — `rgit commit --push` vs plain `git push`
- [ ] `~/.claude/skills/repo-ops/AGENTS.md` — headless commit path, if applicable
- [ ] `~/.claude/settings.json` — update MCP allowlists, remove the `batch_commit` dependency
- [ ] Cursor `rethunk-git` plugin README / agent rules — document the `rgit` replacement

## Already done

- [x] `~/.claude/hooks/rethunk-mcp-nudge.sh` deleted — it was wired nowhere
- [x] `cursor-harness` skill — no stale `rethunk-mcp-nudge` references remain

## Decommission

Only after every item above.

- [ ] Delete the legacy MCP server package (`Rethunk-AI/mcp-multi-root-git`)
- [ ] Remove `rethunk-git` MCP plugin registration from Cursor and Claude configs
- [ ] Re-measure actual token savings and record them in [`design.md`](design.md)
