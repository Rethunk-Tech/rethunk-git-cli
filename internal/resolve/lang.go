// Package resolve maps a symbol anchor to a byte extent in a source file.
//
// Tree-sitter is the primary resolver and always produces the extent that gets
// staged; a language server, when reachable, only verifies it. Extents are byte
// offsets rather than line numbers because line-based ranges are exactly the
// fragility symbol anchors exist to avoid.
package resolve

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// Extent is a half-open byte range [Start, End) in a source file.
type Extent struct {
	Start uint
	End   uint
}

// Declaration is one addressable top-level entity, as reported by a grammar
// adapter. It carries the outermost node — for an exported TypeScript function
// that is the export_statement, and for a decorated Python function the
// decorated_definition — because a caller naming that symbol means the whole
// statement, not the declaration buried inside it.
//
// Doc-comment attribution, ordinals, and the bare/qualified name indexes are
// computed by the core resolver, not by adapters, so the blank-line rule cannot
// drift between languages.
type Declaration struct {
	// Node is the outermost node whose extent is staged.
	Node *ts.Node

	// Bare is the unqualified name, e.g. "Get".
	Bare string

	// Container qualifies Bare when the language nests the symbol, e.g. the
	// receiver type "A" for Go's (a *A) Get. Empty when there is none.
	Container string

	// Sep overrides containerQualified's (index.go) default "." join
	// between Container and Bare, for the rare adapter whose own
	// qualified-name convention is not a dot. CSS Nesting is the one case:
	// nesting flattens via the descendant combinator, a literal space
	// (`.parent { .child {} }` means what `.parent .child { }` means), so a
	// dot-joined qualifier ("parent.child") would not correspond to
	// anything a CSS author could paste back into a stylesheet, unlike
	// every other adapter's own dot-joined convention (Go/TypeScript/Python
	// member access, TOML's and SQL's own dotted-path syntax). Empty (the
	// zero value every other adapter already leaves it at) keeps today's
	// "." unchanged.
	Sep string
}

// Language adapts one tree-sitter grammar. An implementation reports which
// nodes are addressable and how they are named; it does not compute extents.
type Language interface {
	// Name is the identifier used in diagnostics and language-server routing,
	// e.g. "go", "typescript", "python".
	Name() string

	// Extensions lists the file suffixes this grammar claims, including the
	// leading dot.
	Extensions() []string

	// TSLanguage returns the compiled grammar.
	TSLanguage() *ts.Language

	// IsComment reports whether a node kind is a comment, for doc attribution.
	IsComment(kind string) bool

	// Declarations returns every addressable top-level declaration in source
	// order. Nodes that own no symbol are omitted; the core resolver reports
	// their hunks as unanchorable.
	Declarations(src []byte, root *ts.Node) []Declaration

	// ImportKinds lists the node kinds @imports spans. It is a list, not a
	// single kind, because Go emits one import_declaration while TypeScript
	// and Python emit one node per import and Python distinguishes
	// import_statement from import_from_statement.
	//
	// This is a fallback, not the only mechanism: a Language whose import
	// statement is not identifiable by node kind alone (shell's `source
	// f.sh` is an ordinary "command" node, the same kind as every other
	// command) should instead implement ImportMatcher, which importsExtent
	// consults first.
	ImportKinds() []string

	// HeaderKinds lists the node kinds belonging to @header — shebang,
	// build tags, copyright, package clause.
	HeaderKinds() []string

	// OwnsTrailingSeparator reports whether lang's own formatting convention
	// deterministically inserts exactly one blank line after @header or
	// @imports, so that blank line is as much part of the region as its own
	// trailing newline (ExtendThroughOwnedSeparator, pseudo.go). gofmt is
	// the only formatter among this resolver's languages that makes this
	// guarantee unconditionally; every other adapter must answer false
	// explicitly, with a comment giving the reason, rather than falling
	// through to it by default.
	OwnsTrailingSeparator() bool

	// MembersSitFlush reports whether lang's own convention keeps sibling
	// container members (struct fields, interface methods, class methods)
	// adjacent with no blank line between them, so a newly spliced-in member
	// (classify.go's escalateToContainer) is inserted flush rather than with
	// blank-line padding. Every adapter must give this a considered answer
	// rather than defaulting to false.
	MembersSitFlush() bool

	// AllowsRawHeadingFallback reports whether an anchor that fails to
	// resolve as a slug should be retried as raw heading text
	// (rawHeadingFallback, index.go) -- true only for markdown, where a
	// caller may paste a heading's own title rather than rgit's emitted
	// slug.
	AllowsRawHeadingFallback() bool
}

// ImportMatcher is an optional refinement of Language for a grammar whose
// import statement cannot be identified by node kind alone. Shell's `source
// f.sh` (or `. f.sh`) parses as an ordinary "command" node — the same kind
// every other command in the script uses — distinguished only by its own
// command name, so ImportKinds cannot describe it: returning "command" would
// make @imports span nearly every line of a script, silently.
//
// importsExtent (pseudo.go) type-asserts for this and consults it first when
// present; a Language that does not implement it falls back to ImportKinds,
// so Go, TypeScript and Python — none of which need per-node text
// inspection to recognize an import — never take this path.
type ImportMatcher interface {
	// IsImport reports whether n, one of root's own named children, is an
	// import. src is the whole file, passed for the same reason
	// Declarations already takes it: a tree-sitter Node carries no source
	// text of its own to read without it.
	IsImport(src []byte, n *ts.Node) bool
}

// nodeText is n's own source text.
func nodeText(src []byte, n *ts.Node) string {
	return string(src[n.StartByte():n.EndByte()])
}

// namedDecl builds a Declaration staged as extent but named from nameHost's
// "name" field. The two nodes differ whenever a grammar wraps the thing that
// carries the name -- a TypeScript export_statement around a function, a
// Python decorated_definition around a def -- and every adapter needs that
// same rule, so it lives here rather than once per grammar. Pass the same
// node twice when nothing wraps it.
func namedDecl(src []byte, extent, nameHost *ts.Node) (Declaration, bool) {
	name := nameHost.ChildByFieldName("name")
	if name == nil {
		return Declaration{}, false
	}
	return Declaration{Node: extent, Bare: nodeText(src, name)}, true
}

// registered holds every adapter, keyed by file extension. Adapters add
// themselves from an init function in their own file so that adding a grammar
// touches exactly one file and no shared registry.
var registered = map[string]Language{}

// register claims each of l's extensions. It panics on a duplicate claim,
// which can only be a programming error: two grammars fighting over one
// extension would make resolution depend on package initialisation order.
func register(l Language) {
	for _, ext := range l.Extensions() {
		if prior, dup := registered[ext]; dup {
			panic("resolve: " + ext + " claimed by both " + prior.Name() + " and " + l.Name())
		}
		registered[ext] = l
	}
}

// ForExtension returns the adapter claiming ext (including the leading dot).
// A false result means the language is unsupported, which callers report as
// exit 9 — not an error, since naming the path still works.
func ForExtension(ext string) (Language, bool) {
	l, ok := registered[ext]
	return l, ok
}

// buildTagGated is an optional refinement of Language, the same seam shape
// as ImportMatcher above and trailingCommentTrimmer (extent.go): a Language
// implements it only when it needs to answer something most adapters never
// have occasion to. Here, that is "was I compiled in only because a build
// tag selected me" -- only sqlLanguage does (lang_sql.go's rgit_sql tag).
//
// This is deliberately an optional interface rather than a required
// Language method the way OwnsTrailingSeparator/MembersSitFlush/
// AllowsRawHeadingFallback were promoted to (lang.go): those three failed
// silently for a language that never got a considered answer, corrupting
// what that adapter actually resolved. Forgetting to implement this one for
// some future gated adapter cannot do that -- the worst it does is describe
// that adapter as unconditionally present in a `rgit languages` listing,
// cosmetic rather than a correctness gap. A gated adapter's own author is
// also, by construction, already writing the bespoke tag-scoped
// registration file this seam lives beside, the same position lang_sql.go's
// own author was already in.
type buildTagGated interface {
	buildTagGated() bool
}

// LanguageInfo describes one registered adapter for a caller that needs to
// list what this build supports without reaching into resolve's own
// registry or depending on the Language interface itself -- a `rgit
// languages` subcommand is the motivating case. It is deliberately the
// minimal read-only projection that satisfies that: no *Language, no
// grammar handle, nothing that would let a caller start resolving anchors
// through this seam instead of the real one (internal/synth,
// internal/diff).
type LanguageInfo struct {
	// Name is the same string Language.Name reports for this adapter --
	// "go", "css", "typescript", etc.
	Name string

	// Extensions is every file suffix this adapter claims, including the
	// leading dot, in the same order Language.Extensions reports it.
	Extensions []string

	// Gated is true when this adapter was compiled in only because a build
	// tag selected it (buildTagGated above) -- currently true for "sql"
	// alone. A build with the tag omitted has no registry entry for a gated
	// language to begin with, so there is nothing for Languages to report
	// about it at all: Gated is never a way to discover an *absent*
	// language, only to explain why a *present* one might not be in every
	// build.
	Gated bool
}

// Languages lists every adapter registered in this build, one entry per
// distinct language rather than per extension -- .ts/.mts/.cts all report
// as the single "typescript" entry, with Extensions holding all three, the
// same de-duplication ForExtension's own multi-extension registration
// already implies. Sorted by Name so a listing is stable across runs,
// matching every other sorted listing this resolver produces (sortResults,
// internal/synth's own stage.go).
func Languages() []LanguageInfo {
	seen := map[string]bool{}
	var out []LanguageInfo
	for _, l := range registered {
		name := l.Name()
		if seen[name] {
			continue
		}
		seen[name] = true
		g, _ := l.(buildTagGated)
		out = append(out, LanguageInfo{
			Name:       name,
			Extensions: append([]string(nil), l.Extensions()...),
			Gated:      g != nil && g.buildTagGated(),
		})
	}
	slices.SortFunc(out, func(a, b LanguageInfo) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// shebangExtension maps a shebang's own interpreter name to the file
// extension whose adapter should resolve it. Deliberately conservative and
// short: bash and (POSIX) sh both go to the shell grammar; python3 and
// python go to Python, which needs no special-casing of its own to accept
// a shebang arriving via a path with no ".py" suffix -- HeaderKinds already
// treats a shebang as an ordinary "comment" node regardless of how the
// adapter was looked up (lang_python.go).
//
// zsh is deliberately absent, not merely unmapped: tree-sitter-bash
// mis-parses zsh-only syntax, so silently routing it to the shell adapter
// would produce wrong extents rather than an honest refusal (lang_shell.go).
// Every other interpreter -- perl, ruby, node, a project's own wrapper
// script -- is left unmapped for the same reason: guessing wrong is worse
// than the plain "no grammar registered" a caller already handles.
var shebangExtension = map[string]string{
	"bash": ".sh",
	"sh":   ".sh",

	"python3": ".py",
	"python":  ".py",
}

// ForPath returns the adapter for path. Extension lookup is tried first and
// is byte-for-byte ForExtension's own result -- a ".go" file, or any other
// recognized extension, never reaches the code below. Only when that yields
// nothing does ForPath fall back to sniffing content's first line for a "#!"
// interpreter line, via shebangExtension.
//
// content is whatever the caller already has; ForPath itself never reads a
// file. It looks at content's first line alone and nothing past it -- a
// caller that only peeked a bounded prefix of a large or binary file (rather
// than reading the whole thing just to decide it has no shebang) gets
// exactly the same answer a full read would have given, since a real
// shebang line is always the first thing in the file. content may be nil,
// meaning no bytes were available to peek (e.g. the path exists only in
// HEAD, not the worktree); ForPath then behaves exactly like ForExtension.
func ForPath(path string, content []byte) (Language, bool) {
	if l, ok := ForExtension(filepath.Ext(path)); ok {
		return l, true
	}
	interp, ok := shebangInterpreter(content)
	if !ok {
		return nil, false
	}
	ext, ok := shebangExtension[interp]
	if !ok {
		return nil, false
	}
	return ForExtension(ext)
}

// shebangInterpreter reads the interpreter name off content's own first
// line, or reports ok=false when that line is not a shebang at all -- an
// ordinary leading "# comment" is the most common false start, and only a
// line starting with the literal two bytes "#!" is considered.
//
// "#!/usr/bin/env bash" and "#!/bin/bash" both resolve to "bash": the
// "/usr/bin/env NAME" indirection is unwrapped to NAME, the same interpreter
// a direct "#!/bin/NAME" spells directly. An env invocation carrying flags
// of its own ("#!/usr/bin/env -S bash -x") is not unwrapped -- the first
// field after "env" would be "-S", not the interpreter -- and is left
// unmapped rather than guessed at; this is a known gap, not a silent
// misparse, since an unrecognized interpreter falls through to the same
// honest "no grammar registered" refusal every other unmapped shebang does.
func shebangInterpreter(content []byte) (string, bool) {
	line := content
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = bytes.TrimRight(line, "\r")
	if !bytes.HasPrefix(line, []byte("#!")) {
		return "", false
	}
	fields := strings.Fields(string(line[2:]))
	if len(fields) == 0 {
		return "", false
	}
	interp := filepath.Base(fields[0])
	if interp == "env" && len(fields) > 1 {
		interp = filepath.Base(fields[1])
	}
	return interp, true
}

// shebangPeekBytes bounds how much of a candidate file PeekShebangLine will
// ever read: a real interpreter line is always short, so this is generous
// headroom for one, not an attempt to capture more.
const shebangPeekBytes = 256

// PeekShebangLine reads at most shebangPeekBytes from the worktree file at
// fullPath and returns its first line, for ForPath's shebang fallback. ok is
// false when fullPath cannot be opened at all -- most commonly, no worktree
// copy exists there: a path resolved only against HEAD, the index, or an
// arbitrary revision (most often a file deleted from the worktree). Callers
// degrade to whatever they did before shebang sniffing existed -- extension
// lookup is unaffected either way, since it never calls this at all.
//
// The bounded read is deliberate and shared by every caller: a binary file,
// or one with no newline in its opening bytes, must never be read in full
// just to learn it has no shebang. This is the one place that logic lives;
// internal/synth and internal/diff both call it rather than each reading
// their own prefix.
func PeekShebangLine(fullPath string) ([]byte, bool) {
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()

	raw, err := bufio.NewReader(io.LimitReader(f, shebangPeekBytes)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		// Running out of bytes within the peek window (or the whole file,
		// for one shorter than shebangPeekBytes) is expected and not an
		// error worth reporting -- io.EOF is exactly what ReadString
		// returns for both. Anything else (fullPath naming a directory,
		// whose Open succeeds but whose Read does not, and other genuine
		// I/O failures) means fullPath cannot be trusted the same way a
		// failed os.Open above cannot.
		return nil, false
	}
	if len(raw) == 0 {
		// Nothing was actually read -- an empty file, most commonly.
		// Reporting ok=true here would let a zero-byte read look
		// identical to a genuine (if shebang-less) first line to every
		// caller that only checks the bool.
		return nil, false
	}
	return []byte(raw), true
}
