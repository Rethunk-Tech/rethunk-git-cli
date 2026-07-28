package resolve

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

// ResolveError is a typed resolution failure. Callers map Code directly onto
// a process exit status (docs/USAGE.md § Exit codes) rather than pattern
// matching on Error's text.
type ResolveError struct {
	Code       exitcode.Code
	Anchor     string
	Candidates []string

	// TreeSitterRange and LSPRange are set only for Code ==
	// exitcode.ExtentMismatch: both sides of a cross-check disagreement, as
	// 1-based "Lstart..Lend" strings, so the caller can print both without
	// its own knowledge of resolve's byte-offset internals (docs/USAGE.md
	// § Exit codes, exit 6).
	TreeSitterRange string
	LSPRange        string
}

func (e *ResolveError) Error() string {
	if e.Code == exitcode.ExtentMismatch {
		return fmt.Sprintf("resolve: %q: language-server extent mismatch (tree-sitter %s, language-server %s)",
			e.Anchor, e.TreeSitterRange, e.LSPRange)
	}
	label := "unresolved"
	if e.Code == exitcode.AnchorAmbiguous {
		label = "ambiguous"
	}
	if len(e.Candidates) == 0 {
		return fmt.Sprintf("resolve: %q: %s", e.Anchor, label)
	}
	return fmt.Sprintf("resolve: %q: %s (did you mean: %s?)",
		e.Anchor, label, strings.Join(e.Candidates, ", "))
}

// Symbol is one resolved top-level declaration: its identity, both extents,
// and the anchor form rgit emits for it.
type Symbol struct {
	Decl     Declaration
	Full     Extent
	DeclOnly Extent

	// Qualified is the anchor rgit emits for this symbol: Container.Bare
	// when there is a container, Bare#Ordinal when the bare name collides
	// with a sibling and there is no container to disambiguate with,
	// otherwise the bare name unchanged.
	Qualified string
}

// index resolves anchors against one parsed source: bare and qualified name
// lookup, ambiguity detection, and ordinal assignment. Built fresh per
// Resolve call — rgit is a short-lived CLI process, so there is no cache to
// invalidate.
type index struct {
	order       []*Symbol
	byQualified map[string]*Symbol
	// byBare indexes every symbol under its bare name too, not just symbols
	// that lack a container: keying
	// only on qualified names makes a bare "Get" resolve as absent (exit 3)
	// when the correct answer is ambiguous (exit 4) — the remediations
	// differ, so both indexes must exist.
	byBare map[string][]*Symbol
}

func buildIndex(lang Language, src []byte, root *ts.Node) *index {
	decls := lang.Declarations(src, root)

	syms := make([]*Symbol, len(decls))
	for i := range decls {
		d := decls[i]
		syms[i] = &Symbol{
			Decl:     d,
			Full:     fullExtent(lang, src, d.Node),
			DeclOnly: declOnlyExtent(d.Node),
		}
	}

	assignQualifiedNames(syms)

	idx := &index{
		order:       syms,
		byQualified: make(map[string]*Symbol, len(syms)),
		byBare:      make(map[string][]*Symbol, len(syms)),
	}
	for _, s := range syms {
		idx.byQualified[s.Qualified] = s
		idx.byBare[s.Decl.Bare] = append(idx.byBare[s.Decl.Bare], s)
	}
	return idx
}

// assignQualifiedNames sets Symbol.Qualified for every symbol. Ordinals are
// 1-based in source order and apply only within a group of symbols sharing
// a bare name with no container to disambiguate them — Go's repeated
// func init() is the case docs/ANCHORS.md names explicitly.
func assignQualifiedNames(syms []*Symbol) {
	uncontained := map[string]int{}
	for _, s := range syms {
		if s.Decl.Container == "" {
			uncontained[s.Decl.Bare]++
		}
	}

	seen := map[string]int{}
	for _, s := range syms {
		switch {
		case s.Decl.Container != "":
			s.Qualified = s.Decl.Container + "." + s.Decl.Bare
		case uncontained[s.Decl.Bare] == 1:
			s.Qualified = s.Decl.Bare
		default:
			seen[s.Decl.Bare]++
			s.Qualified = fmt.Sprintf("%s#%d", s.Decl.Bare, seen[s.Decl.Bare])
		}
	}
}

// normalizeAnchorInput rewrites gopls's receiver spelling, "(*A).Get" or
// "(A).Get", to the form rgit always emits, "A.Get" (docs/ANCHORS.md), so an
// anchor copied from an IDE outline resolves without the caller reformatting
// it first.
func normalizeAnchorInput(anchor string) string {
	if !strings.HasPrefix(anchor, "(") {
		return anchor
	}
	closeParen := strings.Index(anchor, ")")
	if closeParen < 0 {
		return anchor
	}
	recv := strings.TrimSpace(anchor[1:closeParen])
	recv = strings.TrimPrefix(recv, "*")
	return recv + anchor[closeParen+1:]
}

// resolve maps anchor to its Symbol, or a typed error carrying the exit code
// the caller should use: AnchorAmbiguous when a bare name matches more than
// one symbol, AnchorUnresolvable otherwise.
func (idx *index) resolve(anchor string) (*Symbol, error) {
	anchor = normalizeAnchorInput(anchor)

	if s, ok := idx.byQualified[anchor]; ok {
		return s, nil
	}

	if group, ok := idx.byBare[anchor]; ok {
		if len(group) == 1 {
			return group[0], nil
		}
		candidates := make([]string, len(group))
		for i, s := range group {
			candidates[i] = s.Qualified
		}
		slices.Sort(candidates)
		return nil, &ResolveError{
			Code:       exitcode.AnchorAmbiguous,
			Anchor:     anchor,
			Candidates: candidates,
		}
	}

	return nil, &ResolveError{
		Code:       exitcode.AnchorUnresolvable,
		Anchor:     anchor,
		Candidates: idx.suggest(anchor),
	}
}

// suggest returns up to three known anchors closest to anchor by edit
// distance, for the exit-3 "did you mean" listing (docs/USAGE.md).
func (idx *index) suggest(anchor string) []string {
	const maxCandidates = 3
	const maxDistance = 3

	type scored struct {
		name string
		dist int
	}
	all := make([]scored, 0, len(idx.byQualified))
	for name := range idx.byQualified {
		all = append(all, scored{name, levenshtein(anchor, name)})
	}
	slices.SortFunc(all, func(x, y scored) int {
		return cmp.Or(cmp.Compare(x.dist, y.dist), cmp.Compare(x.name, y.name))
	})

	out := make([]string, 0, maxCandidates)
	for _, s := range all {
		if s.dist > maxDistance || len(out) >= maxCandidates {
			break
		}
		out = append(out, s.name)
	}
	return out
}

func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}
