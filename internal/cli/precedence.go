// Package cli implements argument classification shared by rgit's two
// subcommands: the six-rule precedence table from docs/USAGE.md §
// Argument shape that decides whether a positional is a pathspec, a
// revision, or a symbol anchor.
package cli

import (
	"context"
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
// positional argument.
type Classification struct {
	Kind     Kind
	Pathspec string
	Revision string
	RevPath  RevPath
	Anchor   Anchor
}

// PathChecker answers rule 4 and rule 5's "exists in the worktree or at
// HEAD" test.
type PathChecker interface {
	ExistsInWorktreeOrHEAD(ctx context.Context, path string) (bool, error)
}

// RevisionResolver answers rule 3's "resolves via git rev-parse --verify"
// test. A single call also covers rev:path blob references and revision
// ranges (A..B, A...B), since git's own rev-parse syntax accepts all
// three forms.
type RevisionResolver interface {
	ResolvesAsRevision(ctx context.Context, token string) (bool, error)
}

// UnresolvedArgError is rule 6: none of the applicable rules matched.
// Tried lists every rule considered, in the order checked, so the caller
// sees why rather than a bare "invalid argument". "Considered" rather than
// "attempted" on purpose: rule 2, for instance, is ruled out by a leading-
// colon prefix check on a, not by actually resolving it as a pathspec and
// finding it wanting.
type UnresolvedArgError struct {
	Arg   string
	Tried []string
}

func (e *UnresolvedArgError) Error() string {
	return fmt.Sprintf("cannot classify %q: rules considered: %s", e.Arg, strings.Join(e.Tried, "; "))
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
//  6. Otherwise, an *UnresolvedArgError listing every rule considered.
func ClassifyArgs(ctx context.Context, args []string, allowRevisions bool, paths PathChecker, revs RevisionResolver) ([]Classification, error) {
	out := make([]Classification, 0, len(args))
	seenDoubleDash := false
	for _, a := range args {
		if !seenDoubleDash && a == "--" {
			seenDoubleDash = true
			continue
		}
		if seenDoubleDash {
			out = append(out, Classification{Kind: KindPathspec, Pathspec: a})
			continue
		}
		c, err := classifyOne(ctx, a, allowRevisions, paths, revs)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func classifyOne(ctx context.Context, a string, allowRevisions bool, paths PathChecker, revs RevisionResolver) (Classification, error) {
	var tried []string

	// Rule 2. All git pathspec magic is leading-colon, so this alone
	// disambiguates it from an interior-colon symbol anchor; no escaping
	// is ever needed.
	if strings.HasPrefix(a, ":") {
		return Classification{Kind: KindPathspec, Pathspec: a}, nil
	}
	tried = append(tried, "pathspec magic (leading ':')")

	// Rule 3, diff only. Checked before the existing-path test so that
	// "HEAD~1:f.go" resolves as a blob reference rather than failing the
	// path-existence check and falling through to rule 5.
	if allowRevisions {
		ok, err := revs.ResolvesAsRevision(ctx, a)
		if err != nil {
			return Classification{}, err
		}
		if ok {
			if rev, path, hasColon := strings.Cut(a, ":"); hasColon {
				return Classification{Kind: KindRevPath, RevPath: RevPath{Rev: rev, Path: path}}, nil
			}
			return Classification{Kind: KindRevision, Revision: a}, nil
		}
		tried = append(tried, "revision, rev:path, or range (git rev-parse --verify)")
	}

	// Rule 4. An existing-path check beats a colon-split so that a
	// tracked file like "src/notes:draft.md" is claimed here rather than
	// being split into a bogus anchor by rule 5.
	exists, err := paths.ExistsInWorktreeOrHEAD(ctx, a)
	if err != nil {
		return Classification{}, err
	}
	if exists {
		return Classification{Kind: KindPathspec, Pathspec: a}, nil
	}
	tried = append(tried, "existing path (worktree or HEAD)")

	// Rule 5. Split at the LAST colon, not the first, so a path that
	// itself contains one (having already failed rule 4 whole) still
	// yields the right file/name split.
	if idx := strings.LastIndexByte(a, ':'); idx > 0 && idx < len(a)-1 {
		file, name := a[:idx], a[idx+1:]
		fileExists, ferr := paths.ExistsInWorktreeOrHEAD(ctx, file)
		if ferr != nil {
			return Classification{}, ferr
		}
		if fileExists {
			return Classification{Kind: KindAnchor, Anchor: Anchor{File: file, Name: name}}, nil
		}
	}
	tried = append(tried, "symbol anchor (existing path + name after last ':')")

	// Rule 6.
	return Classification{}, &UnresolvedArgError{Arg: a, Tried: tried}
}

// PrefixPath rebases a caller-supplied path onto the repository root.
// git resolves a pathspec relative to the current directory, so `a.go`
// typed in pkg/deep means pkg/deep/a.go; rgit works in root-relative
// paths internally, and every path a caller names goes through here.
//
// Leading-colon pathspec magic is exempt: git already defines those as
// root-relative (`:/`, `:(top)`), and their interior is not a plain path
// to join onto anything.
func PrefixPath(prefix, path string) string {
	if prefix == "" || strings.HasPrefix(path, ":") {
		return path
	}
	return filepath.Join(prefix, path)
}

// GitPathChecker is the real PathChecker: worktree existence via the
// filesystem, HEAD existence via gitx. Prefix is the current directory
// relative to Root (gitx.Repo.ShowPrefix), so rules 4 and 5 test the
// same path git would.
type GitPathChecker struct {
	Root   string
	Prefix string
	Repo   *gitx.Repo
}

func (c GitPathChecker) ExistsInWorktreeOrHEAD(ctx context.Context, path string) (bool, error) {
	rel := PrefixPath(c.Prefix, path)
	if _, err := os.Stat(filepath.Join(c.Root, rel)); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}

	_, found, err := c.Repo.LsTreeTolerant(ctx, "HEAD", rel)
	if err != nil {
		return false, err
	}
	return found, nil
}

// GitRevisionResolver is the real RevisionResolver, backed by gitx.
type GitRevisionResolver struct {
	Repo *gitx.Repo
}

func (r GitRevisionResolver) ResolvesAsRevision(ctx context.Context, token string) (bool, error) {
	_, ok, err := r.Repo.RevParseVerify(ctx, token)
	return ok, err
}
