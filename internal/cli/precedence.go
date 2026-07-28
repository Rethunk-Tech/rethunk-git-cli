// Package cli implements argument classification shared by rgit's two
// subcommands: the six-rule precedence table from docs/USAGE.md §
// Argument shape that decides whether a positional is a pathspec, a
// revision, or a symbol anchor.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

// Kind is the classification a positional argument resolved to.
type Kind int

const (
	// KindPathspec is handed to git verbatim: a plain existing path, a
	// leading-colon pathspec magic form, or anything after "--".
	KindPathspec Kind = iota
	// KindRevision is a bare revision (branch, tag, SHA, range) rule 3
	// verified via git rev-parse --verify. diff-only.
	KindRevision
	// KindRevPath is a "rev:path" blob reference, rule 3's other outcome.
	// diff-only.
	KindRevPath
	// KindAnchor is a FILE:NAME symbol anchor, rule 5.
	KindAnchor
)

// RevPath is a "rev:path" blob reference, e.g. "HEAD~1:auth.go".
type RevPath struct {
	Rev  string
	Path string
}

// Anchor is a FILE:NAME symbol anchor.
type Anchor struct {
	File string
	Name string
}

// Classification is the result of applying the six-rule table to one
// positional argument. Rule records which of the six rules matched
// (1-indexed, matching docs/USAGE.md's table), which is diagnostic only —
// downstream code should switch on Kind, not Rule.
type Classification struct {
	Kind     Kind
	Rule     int
	Raw      string
	Pathspec string
	Revision string
	RevPath  RevPath
	Anchor   Anchor
}

// PathChecker answers rule 4 and rule 5's "exists in the worktree or at
// HEAD" test.
type PathChecker interface {
	ExistsInWorktreeOrHEAD(path string) (bool, error)
}

// RevisionResolver answers rule 3's "resolves via git rev-parse --verify"
// test. A single call also covers rev:path blob references and revision
// ranges (A..B, A...B), since git's own rev-parse syntax accepts all
// three forms.
type RevisionResolver interface {
	ResolvesAsRevision(token string) (bool, error)
}

// UnresolvedArgError is rule 6: none of the applicable rules matched.
// Tried lists every interpretation that was attempted, in the order
// tried, so the caller sees why rather than a bare "invalid argument".
type UnresolvedArgError struct {
	Arg   string
	Tried []string
}

func (e *UnresolvedArgError) Error() string {
	return fmt.Sprintf("cannot classify %q: tried %s", e.Arg, strings.Join(e.Tried, "; "))
}

// ClassifyArgs applies docs/USAGE.md's six-rule precedence table to args,
// in order, exactly as documented:
//
//  1. Everything after the first "--" is a pathspec, always.
//  2. A token starting with ":" is git pathspec magic, passed through
//     verbatim.
//  3. (diff only, gated by allowRevisions) A token git's own rev-parse
//     resolves is a revision, a rev:path blob reference, or a range.
//  4. A token naming a path that exists in the worktree or at HEAD is a
//     pathspec.
//  5. A token that splits at its last ":" into an existing path and a
//     name is a symbol anchor.
//  6. Otherwise, an *UnresolvedArgError listing every rule tried.
func ClassifyArgs(args []string, allowRevisions bool, paths PathChecker, revs RevisionResolver) ([]Classification, error) {
	out := make([]Classification, 0, len(args))
	seenDoubleDash := false
	for _, a := range args {
		if !seenDoubleDash && a == "--" {
			seenDoubleDash = true
			continue
		}
		if seenDoubleDash {
			out = append(out, Classification{Kind: KindPathspec, Rule: 1, Raw: a, Pathspec: a})
			continue
		}
		c, err := classifyOne(a, allowRevisions, paths, revs)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func classifyOne(a string, allowRevisions bool, paths PathChecker, revs RevisionResolver) (Classification, error) {
	var tried []string

	// Rule 2. All git pathspec magic is leading-colon, so this alone
	// disambiguates it from an interior-colon symbol anchor; no escaping
	// is ever needed.
	if strings.HasPrefix(a, ":") {
		return Classification{Kind: KindPathspec, Rule: 2, Raw: a, Pathspec: a}, nil
	}
	tried = append(tried, "pathspec magic (leading ':')")

	// Rule 3, diff only. Checked before the existing-path test so that
	// "HEAD~1:f.go" resolves as a blob reference rather than failing the
	// path-existence check and falling through to rule 5.
	if allowRevisions {
		ok, err := revs.ResolvesAsRevision(a)
		if err != nil {
			return Classification{}, err
		}
		if ok {
			if idx := strings.IndexByte(a, ':'); idx >= 0 {
				return Classification{Kind: KindRevPath, Rule: 3, Raw: a, RevPath: RevPath{Rev: a[:idx], Path: a[idx+1:]}}, nil
			}
			return Classification{Kind: KindRevision, Rule: 3, Raw: a, Revision: a}, nil
		}
		tried = append(tried, "revision, rev:path, or range (git rev-parse --verify)")
	}

	// Rule 4. An existing-path check beats a colon-split so that a
	// tracked file like "src/notes:draft.md" is claimed here rather than
	// being split into a bogus anchor by rule 5.
	exists, err := paths.ExistsInWorktreeOrHEAD(a)
	if err != nil {
		return Classification{}, err
	}
	if exists {
		return Classification{Kind: KindPathspec, Rule: 4, Raw: a, Pathspec: a}, nil
	}
	tried = append(tried, "existing path (worktree or HEAD)")

	// Rule 5. Split at the LAST colon, not the first, so a path that
	// itself contains one (having already failed rule 4 whole) still
	// yields the right file/name split.
	if idx := strings.LastIndexByte(a, ':'); idx > 0 && idx < len(a)-1 {
		file, name := a[:idx], a[idx+1:]
		fileExists, ferr := paths.ExistsInWorktreeOrHEAD(file)
		if ferr != nil {
			return Classification{}, ferr
		}
		if fileExists {
			return Classification{Kind: KindAnchor, Rule: 5, Raw: a, Anchor: Anchor{File: file, Name: name}}, nil
		}
	}
	tried = append(tried, "symbol anchor (existing path + name after last ':')")

	// Rule 6.
	return Classification{}, &UnresolvedArgError{Arg: a, Tried: tried}
}

// GitPathChecker is the real PathChecker: worktree existence via the
// filesystem, HEAD existence via gitx.
type GitPathChecker struct {
	Root string
	Repo *gitx.Repo
	Ctx  context.Context
}

func (c GitPathChecker) ExistsInWorktreeOrHEAD(path string) (bool, error) {
	if _, err := os.Stat(filepath.Join(c.Root, path)); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}

	_, found, err := c.Repo.LsTree(c.Ctx, "HEAD", path)
	if err != nil {
		var execErr *gitx.ExecError
		if errors.As(err, &execErr) {
			return false, err
		}
		// A *GitError here is almost always "HEAD does not exist yet" (an
		// unborn branch), not a real failure — a tree that does not exist
		// trivially contains no path.
		return false, nil
	}
	return found, nil
}

// GitRevisionResolver is the real RevisionResolver, backed by gitx.
type GitRevisionResolver struct {
	Repo *gitx.Repo
	Ctx  context.Context
}

func (r GitRevisionResolver) ResolvesAsRevision(token string) (bool, error) {
	_, ok, err := r.Repo.RevParseVerify(r.Ctx, token)
	return ok, err
}
