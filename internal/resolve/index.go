package resolve

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

// ResolveError is a typed resolution failure. Callers map Code directly onto
// a process exit status (docs/CODES.md § Exit codes) rather than pattern
// matching on Error's text.
type ResolveError struct {
	Code       exitcode.Code
	Anchor     string
	Candidates []string

	// Path is the file the anchor was resolved against, for a caller that
	// has no other way to recover it once this error has propagated past
	// whoever had it in scope. It is optional and populated by exactly one
	// caller today -- internal/diff/run.go's validateSym, which sets it
	// both on the *ResolveError it constructs directly (no grammar for the
	// file) and, by backfilling it after the fact, on the one
	// resolve.Resolve itself returns (Resolve takes no path argument, so it
	// cannot set this field on construction). validateSym does this because
	// its own caller (internal/app/diff.go's runDiff) has no other way to
	// learn which file a failed --sym/bare-anchor filter named --
	// ResolveError.Anchor carries only the bare symbol name, and
	// diffpkg.SymRef pairs are not otherwise recoverable from the error
	// alone.
	//
	// Every other construction site in this package, and in internal/synth
	// and internal/app, leaves Path empty deliberately: those callers either
	// have no path to attach (resolve.Resolve's own internal failures, with
	// no caller yet needing one back) or already hold the path in a local
	// variable and have no need to fish it back out of the error. Callers
	// must not assume Path is populated just because the field exists.
	Path string

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
	// label defaults to AnchorUnresolvable's own wording; every other code
	// handled here overrides it explicitly rather than falling through --
	// exitcode.UnsupportedLanguage used to fall through to "unresolved",
	// contradicting docs/CODES.md's exit-9 row ("Unsupported / deferred
	// language for a symbol anchor") while the exit code itself was
	// already right. internal/diff/run.go's validateSym is the one caller
	// that constructs this code, and it never sets Candidates -- there is
	// nothing to suggest for a language with no grammar at all -- so the
	// "did you mean" suffix below stays unreachable for it the same way it
	// already is for ExtentMismatch.
	label := "unresolved"
	switch e.Code {
	case exitcode.AnchorAmbiguous:
		label = "ambiguous"
	case exitcode.UnsupportedLanguage:
		label = "unsupported language"
	}
	if len(e.Candidates) == 0 {
		return fmt.Sprintf("resolve: %q: %s", e.Anchor, label)
	}
	return fmt.Sprintf("resolve: %q: %s (did you mean: %s?)",
		e.Anchor, label, strings.Join(e.Candidates, ", "))
}

// AsResolveError reports whether err is, or wraps, a *ResolveError, via
// errors.As rather than a bare type assertion -- a caller one layer removed
// from where an error is constructed (internal/synth's classify.go is the
// motivating case) cannot assume it never travels wrapped, and a bare
// assertion silently treats a wrapped ResolveError as "some other kind of
// hard failure" instead of the typed resolution outcome it actually is.
// Exported so every package needing this reads it from here once, rather
// than each carrying its own copy the way internal/synth's classify.go used
// to (isResolveError/asResolveError, now deleted in its favor).
func AsResolveError(err error) (*ResolveError, bool) {
	var rerr *ResolveError
	if errors.As(err, &rerr) {
		return rerr, true
	}
	return nil, false
}

// AsAmbiguous is AsResolveError narrowed to exitcode.AnchorAmbiguous -- the
// one Code a caller like classify's switch must re-propagate immediately
// rather than falling through to "neither side resolved", since ambiguous
// and absent take different exit codes and different remediations.
func AsAmbiguous(err error) (*ResolveError, bool) {
	if rerr, ok := AsResolveError(err); ok && rerr.Code == exitcode.AnchorAmbiguous {
		return rerr, true
	}
	return nil, false
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

	// allowRawHeading gates rawHeadingFallback, set from the language's own
	// Language.AllowsRawHeadingFallback answer (buildIndex) rather than
	// inferred from the anchor text itself: the fallback's raw-text-to-slug
	// rewrite is a markdown-only accommodation (docs/ANCHORS.md), and gating
	// on the language that was already selected by the file's extension is
	// the only test that can never produce a false positive for Go/TS/Python
	// — unlike gating on whether the anchor text merely contains a space,
	// which excludes every single-word heading for no reason tied to
	// cross-language safety.
	allowRawHeading bool
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
		order:           syms,
		byQualified:     make(map[string]*Symbol, len(syms)),
		byBare:          make(map[string][]*Symbol, len(syms)),
		allowRawHeading: lang.AllowsRawHeadingFallback(),
	}
	for _, s := range syms {
		idx.byQualified[s.Qualified] = s
		idx.byBare[s.Decl.Bare] = append(idx.byBare[s.Decl.Bare], s)
		// The same reasoning one level in: when an ordinal had to be appended,
		// "Box.size" is no longer a key of its own, and without this it reads
		// as absent (exit 3, "did you mean") when the honest answer is
		// ambiguous (exit 4, "here are the two").
		if cq := containerQualified(s); cq != s.Decl.Bare {
			idx.byBare[cq] = append(idx.byBare[cq], s)
		}
	}
	return idx
}

// assignQualifiedNames sets Symbol.Qualified for every symbol. Ordinals are
// 1-based in source order and apply only within a group of symbols sharing
// a bare name with no container to disambiguate them — Go's repeated
// func init() is the case docs/ANCHORS.md names explicitly.
func assignQualifiedNames(syms []*Symbol) {
	total := map[string]int{}
	for _, s := range syms {
		total[containerQualified(s)]++
	}

	seen := map[string]int{}
	for _, s := range syms {
		name := containerQualified(s)
		if total[name] == 1 {
			s.Qualified = name
			continue
		}
		seen[name]++
		s.Qualified = fmt.Sprintf("%s#%d", name, seen[name])
	}
}

// containerQualified is a symbol's name before ordinals are applied:
// Container<Sep>Bare where there is a container, the bare name otherwise.
// Sep is "." unless the declaration overrides it (Declaration.Sep's own doc
// comment -- CSS Nesting is the one adapter that does).
//
// The ordinal rule counts these, not bare names, so it reaches inside a
// container as well as beside one. Two members of one class can share a name
// -- a TypeScript get/set pair is the ordinary case, not a corner -- and
// counting only bare names would let two same-named container members (or
// two same-named classes in one file) collide on one qualified name,
// silently keeping whichever the index visited last with no ambiguity
// reported.
func containerQualified(s *Symbol) string {
	if s.Decl.Container == "" {
		return s.Decl.Bare
	}
	return joinQualified(s.Decl.Container, s.Decl.Bare, s.Decl.Sep)
}

// joinQualified joins container and name with sep, defaulting an empty sep
// to "." -- the one rule containerQualified above and crosscheck.go's
// qualifyLSPSymbol both need identically. It is deliberately only that
// much: the two callers otherwise disagree on what an *empty* container
// means (containerQualified's own bare name vs. qualifyLSPSymbol's
// gopls-receiver normalization), so neither is folded in here.
func joinQualified(container, name, sep string) string {
	if sep == "" {
		sep = "."
	}
	return container + sep + name
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

// rawHeadingFallback reports slugify(anchor) when anchor looks like a
// markdown heading's raw text copied verbatim rather than rgit's emitted
// slug — whether or not that text happens to contain a space. A single-word
// heading's raw text ("Install") is exactly as valid a copy-paste source as
// a multi-word one ("Diff Scope"); gating on the presence of a space made
// the single-word case unreachable by its own text for no reason a caller
// could act on. Scoping this to markdown is the caller's job
// (idx.allowRawHeading), not this function's -- see resolve for why that
// gate is safe to relax.
func rawHeadingFallback(anchor string) (string, bool) {
	slug := slugify(anchor)
	if slug == "" || slug == anchor {
		return "", false
	}
	return slug, true
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

	// Markdown accepts a heading's own raw text on input (docs/ANCHORS.md)
	// the same way Go accepts gopls's "(*A).Get" spelling on input while
	// always emitting "A.Get": the canonical, emitted spelling is a slug,
	// but a person typing an anchor by hand may copy the heading text
	// itself, single word or several. Gated on idx.allowRawHeading, set only
	// for the markdown adapter (buildIndex), rather than on whether anchor
	// contains a space: the fallback is reachable only for a markdown file
	// to begin with -- the file's extension already selected this adapter --
	// so there is no cross-language ambiguity a text-shape gate needs to
	// protect against; only a bare heading's raw text is accepted this way,
	// not a raw "Container.Raw Text" combination.
	if idx.allowRawHeading {
		if slug, ok := rawHeadingFallback(anchor); ok {
			if s, err := idx.resolve(slug); err == nil {
				return s, nil
			} else if rerr, ok := AsAmbiguous(err); ok {
				return nil, rerr
			}
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
