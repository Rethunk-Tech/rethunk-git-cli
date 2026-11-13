# Security

## Supported versions

The latest tagged release. `rgit` has no long-lived release branches, so a
fix ships in the next tag rather than being backported.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting on this repository: <https://github.com/Rethunk-Tech/rethunk-git-cli/security/advisories/new>, or the **Security** tab, then **Report a vulnerability**. The report stays private between you and us until a fix ships.

If private reporting is unavailable, email [oss@rethunk.tech](mailto:oss@rethunk.tech) with **[SECURITY]** in the subject.

Please do not open a public issue, pull request or discussion for a suspected vulnerability.

A useful report has the affected repository and version (or commit), steps to reproduce, what an attacker gets and under which conditions, and whether anyone else knows.

## What counts as a vulnerability

A vulnerability is a flaw that lets someone cross a boundary they should not: run code, read or change data they have no right to, escape a sandbox or path restriction, or leak a credential. A bug is anything else that misbehaves, such as a crash on your own input, wrong output or a slow path; open an ordinary issue for those. If you are unsure which one you have, report it privately and we will say.

## Coordinated disclosure

We handle the report inside the private advisory, so you see each status change and can comment on the fix. The advisory goes public when the fix ships, or 90 days after your report, whichever comes first. If a fix is close, either of us can ask for more time in the thread. You are credited in the advisory unless you would rather not be.

## Response timelines

- We acknowledge your report within 3 business days.
- We give you an assessment (accepted, needs more information or declined, with reasons) within 10 business days.
- We aim to ship a fix within 90 days of the report.

## What is in scope

`rgit` stages and commits on a caller's behalf, so the interesting failures
are ones where it writes something the caller did not name:

- A symbol anchor that stages bytes outside its own extent, or a path
  outside the repository.
- A crafted source file that makes the resolver stage content from an
  unrelated declaration.
- Anything that causes `rgit` to run a command the caller did not ask for,
  beyond the `git` invocations it documents.

## What is out of scope

- **Hooks running arbitrary code.** `rgit` runs git's hooks exactly as
  `git commit` does, and deliberately does not police them
  ([`docs/USAGE.md`](docs/USAGE.md#behaviour-inherited-from-git)). A
  malicious hook in a repository you have already checked out is outside
  this boundary, as it is for `git` itself.
- **Language servers and the tree-sitter CLI.** Both are optional external
  programs you install; `rgit` shells out to them and trusts them the way
  any editor does. That trust does not extend to the path `rgit` reaches
  `gopls`'s daemon socket at: a predictable name in a world-writable temp
  directory could be pre-created by another user on a multi-user host. So
  before every dial or spawn `rgit` creates or re-verifies a private,
  UID-scoped, `0700` subdirectory, `Lstat`s it to reject a planted symlink
  rather than follow it, and confirms the current user still owns it; any
  check failing degrades to `[ts-only]` (`internal/lsp/dial.go`'s
  `privateSocketDir`). Windows has no UID to scope by (`os.Getuid` returns
  `-1`, naming the directory `rgit--1`) and needs no ownership check: its
  per-user temp directory is already ACL-restricted by the platform. A
  caller-supplied `$RGIT_LSP_SOCKET` is exempt: it is the caller's path to
  manage, not one `rgit` vouches for.
- **A wrong extent that stages too little.** That is a correctness bug;
  please file it as an ordinary issue.

## Safe harbour

We will not pursue legal action against good-faith research that avoids privacy violations, data destruction and disruption of services, stops at a proof of concept, and keeps the details private until the advisory is published. Rethunk.Tech, LLC maintains this repository.

## Release signing

Releases from the first one after this section landed carry `SHA256SUMS.asc`, a detached signature over `SHA256SUMS` made with the Rethunk.Tech release signing key. Earlier releases are unsigned.

- Fingerprint: `827D 0553 6BF3 3C8F 634E  DBE3 E60A 9B0F 6E3F 276E`
- Verify: `gpg --import rethunk-release-signing.pub.asc && gpg --verify SHA256SUMS.asc SHA256SUMS`

```
-----BEGIN PGP PUBLIC KEY BLOCK-----

mDMEavZr2xYJKwYBBAHaRw8BAQdAuTAn1oKhKC13SOyJMlYmaoGGQtVRyZ5yEpxT
yZBxH/q0NFJldGh1bmsuVGVjaCByZWxlYXNlIHNpZ25pbmcgPHJlbGVhc2VzQHJl
dGh1bmsudGVjaD6ImQQTFgoAQRYhBIJ9BVNr8zyPY07b4+YKmw9uPyduBQJq9mvb
AhsDBQkDwmcABQsJCAcCAiICBhUKCQgLAgQWAgMBAh4HAheAAAoJEOYKmw9uPydu
7WIA+wYeW352nuKph1uJ+XJPPpgmkPyYujZWiRBZIUUA5hfwAQDY6ru4vNL8TfQ5
5rddKHoSKh1nMIdiLWn9ukkyWW+MCg==
=meeO
-----END PGP PUBLIC KEY BLOCK-----
```
