# Security

## Supported versions

The latest tagged release. `rgit` has no long-lived release branches, so a
fix ships in the next tag rather than being backported.

## Reporting a vulnerability

Use GitHub's **private vulnerability reporting** on this repository
(Security → Report a vulnerability). It stays private until a fix ships.

Please do not open a public issue for a suspected vulnerability first.

## What is in scope

`rgit` stages and commits on a caller's behalf, so the interesting failures
are ones where it writes something the caller did not name:

- A symbol anchor that stages bytes outside its own extent, or a path
  outside the repository.
- A crafted source file that makes the resolver stage content from an
  unrelated declaration.
- Anything that causes `rgit` to run a command the caller did not ask for,
  beyond the `git` invocations it documents.

## What is not

- **Hooks running arbitrary code.** `rgit` runs git's hooks exactly as
  `git commit` does, and deliberately does not police them
  ([`docs/USAGE.md`](docs/USAGE.md#behaviour-inherited-from-git)). A
  malicious hook in a repository you have already checked out is outside
  this boundary — as it is for `git` itself.
- **Language servers and the tree-sitter CLI.** Both are optional external
  programs you install; `rgit` shells out to them and trusts them the way
  any editor does. That trust does not extend to the path `rgit` reaches
  `gopls`'s daemon socket at: a predictable name inside a shared,
  world-writable temp directory could be pre-created by another user on a
  multi-user host, so before every dial or spawn `rgit` creates (or
  re-verifies) a private, UID-scoped, `0700` subdirectory, `Lstat`s it to
  reject a planted symlink outright rather than follow it, and confirms it
  is still owned by the current user; any check failing degrades to
  `[ts-only]` instead of trusting the path
  (`internal/lsp/dial.go`'s `privateSocketDir`). On Windows there is no UID
  to scope by (`os.Getuid` returns `-1`, so the directory is literally named
  `rgit--1`) and no ownership check to make: the per-user temp directory is
  already ACL-restricted to its owner by the platform, so the shared,
  world-writable temp directory this defends against on Unix has no
  equivalent there to defend against. A caller-supplied `$RGIT_LSP_SOCKET`
  is exempt from all of this — it is the caller's own path to manage, not
  one `rgit` need vouch for.
- **A wrong extent that stages too little.** That is a correctness bug —
  please file it as an ordinary issue.
