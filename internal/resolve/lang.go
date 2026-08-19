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
	//
	// nil is a legitimate answer for a grammar with no import concept at
	// all, not merely one absent from a particular file -- defaultLanguage
	// (lang_default.go) supplies exactly that for every adapter that has
	// nothing more specific to say.
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
	// slug. defaultLanguage (lang_default.go) answers false for every
	// adapter that does not override it.
	AllowsRawHeadingFallback() bool

	// FlatContainer reports true when Declaration.Container is not an
	// ancestor's name and must never be resolved as one. defaultLanguage
	// answers false; only an adapter whose Container is a formatting
	// device rather than real containment overrides it.
	FlatContainer() bool
}

// StructuredDataLanguage is an optional refinement of Language for an
// adapter whose format `rgit commit`'s own symbol-splice guard must refuse
// a FILE:SYMBOL anchor against: JSON, YAML, and TOML's grammars do not
// always agree with where a spliced extent actually belongs, and unlike a
// source-code grammar there is no compiler downstream to catch the
// resulting malformed blob -- it lands in HEAD looking like a normal
// commit (internal/synth/stage.go's openFilePlan is where the refusal
// itself lives). Naming the path instead is unaffected: nothing here
// changes how `diff`, `blame`, or `log` read these files.
//
// A Language that does not implement this interface is not one of these
// guarded formats -- IsStructuredData below answers false for it, the same
// "absence means no" default ImportMatcher already uses.
type StructuredDataLanguage interface {
	// StructuredData reports whether this adapter's format is guarded.
	StructuredData() bool
}

// IsStructuredData reports whether lang implements StructuredDataLanguage
// and answers true -- the one test internal/synth's openFilePlan applies to
// every FILE:SYMBOL anchor before staging it.
func IsStructuredData(lang Language) bool {
	sd, ok := lang.(StructuredDataLanguage)
	return ok && sd.StructuredData()
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

// stripQuotes strips text's own surrounding quote byte -- a best-effort
// unwrap of a quoted key's source text, not full string-escape decoding,
// shared by lang_yaml.go's yamlKeyName (double_quote_scalar/
// single_quote_scalar) and lang_toml.go's tomlKeyName (quoted_key): both
// read a quoted key's full node text, including its delimiters, and only
// need the same one-byte-off-each-end trim. ok=false means text is too
// short to have both an opening and a closing quote to strip -- callers
// treat that as unaddressable rather than fabricating a name.
func stripQuotes(text string) (string, bool) {
	if len(text) < 2 {
		return "", false
	}
	return text[1 : len(text)-1], true
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

// ForExtensionFolding returns the adapter claiming ext. It preserves exact
// lookup first and only folds case when the caller explicitly requests it.
func ForExtensionFolding(ext string, fold bool) (Language, bool) {
	if l, ok := ForExtension(ext); ok {
		return l, true
	}
	if fold {
		l, ok := ForExtension(strings.ToLower(ext))
		return l, ok
	}
	return nil, false
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

	// The whole Node/TypeScript ecosystem routes to the TypeScript adapter
	// (.ts), never a new grammar: tree-sitter-typescript's own dialect
	// already parses plain JS as a subset, and none of these interpreters
	// is a JSX runner specifically -- a real ".tsx"/".jsx" file already
	// carries its own extension and never reaches shebang sniffing at all.
	"node":    ".ts",
	"nodejs":  ".ts",
	"tsx":     ".ts",
	"ts-node": ".ts",
	"bun":     ".ts",
	"deno":    ".ts",
}

// ForPathFolding returns the adapter for path. Extension lookup is tried
// first and is byte-for-byte ForExtensionFolding's own result -- a ".go"
// file, or any other recognized extension, never reaches the code below.
// Only when that yields nothing does it fall back to sniffing content's
// first line for a "#!" interpreter line, via shebangExtension. fold applies
// to the extension lookup alone; path itself stays byte-for-byte unchanged
// and shebang lookup is never folded.
//
// content is whatever the caller already has; ForPathFolding itself never
// reads a file. It looks at content's first line alone and nothing past it
// -- a caller that only peeked a bounded prefix of a large or binary file
// (rather than reading the whole thing just to decide it has no shebang)
// gets exactly the same answer a full read would have given, since a real
// shebang line is always the first thing in the file. content may be nil,
// meaning no bytes were available to peek (e.g. the path exists only in
// HEAD, not the worktree); it then behaves exactly like ForExtensionFolding.
func ForPathFolding(path string, content []byte, fold bool) (Language, bool) {
	if l, ok := ForExtensionFolding(filepath.Ext(path), fold); ok {
		return l, true
	}
	interp, ok := shebangInterpreter(content)
	if !ok {
		return nil, false
	}
	ext, ok := shebangExtensionLookup(interp)
	if !ok {
		return nil, false
	}
	return ForExtension(ext)
}

// shebangExtensionLookup keeps the interpreter table exact by default, then
// recognizes versions only for runtimes whose mapped family is unambiguous.
// Python 2 is intentionally excluded: accepting every numeric Python suffix
// would route its syntax to the Python 3 grammar.
func shebangExtensionLookup(interp string) (string, bool) {
	if ext, ok := shebangExtension[interp]; ok {
		return ext, true
	}

	if strings.HasPrefix(interp, "python3") && versionSuffix(interp[len("python3"):], true) {
		return shebangExtension["python3"], true
	}
	for _, family := range []string{"node", "bun"} {
		if strings.HasPrefix(interp, family) && versionSuffix(interp[len(family):], false) {
			return shebangExtension[family], true
		}
	}
	return "", false
}

func versionSuffix(suffix string, dottedOnly bool) bool {
	if suffix == "" {
		return false
	}
	if dottedOnly && suffix[0] != '.' {
		return false
	}
	if dottedOnly {
		suffix = suffix[1:]
	}
	if !dottedOnly && suffix[0] == '-' {
		return false
	}
	for part := range strings.SplitSeq(suffix, ".") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// shebangInterpreter reads the interpreter name off content's own first
// line, or reports ok=false when that line is not a shebang at all -- an
// ordinary leading "# comment" is the most common false start, and only a
// line starting with the literal two bytes "#!" is considered.
//
// "#!/usr/bin/env bash" and "#!/bin/bash" both resolve to "bash": the
// "/usr/bin/env NAME" indirection is unwrapped to NAME, the same interpreter
// a direct "#!/bin/NAME" spells directly. "#!/usr/bin/env -S NAME ..." is
// unwrapped the same way, skipping the "-S" itself -- env's own "split the
// rest of the line into multiple arguments" flag, needed for a NAME that
// takes flags of its own ("#!/usr/bin/env -S node --import tsx"). Only that
// one flag is recognized; "#!/usr/bin/env -S bash -x" unwraps to "bash",
// but any other env flag before NAME is left unmapped rather than guessed
// at, the same honest "no grammar registered" refusal every unrecognized
// interpreter already falls through to.
//
// "npx NAME" and "bunx NAME" are unwrapped once more, to NAME itself: both
// are package runners, not interpreters, and NAME is what actually decides
// the language ("#!/usr/bin/env npx tsx" is TypeScript, not "npx"). A bare
// "npx"/"bunx" with nothing after it stays unmapped -- there is no honest
// guess for what it would have run. "bun" needs no such unwrap: unlike
// npx/bunx it is a real JS/TS runtime in its own right, mapped directly in
// shebangExtension, and a trailing subcommand ("bun run") is simply never
// looked at.
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

	i := 0
	interp := filepath.Base(fields[i])
	if interp == "env" && len(fields) > i+1 {
		i++
		if fields[i] == "-S" && len(fields) > i+1 {
			i++
		}
		interp = filepath.Base(fields[i])
	}
	if (interp == "npx" || interp == "bunx") && len(fields) > i+1 {
		interp = filepath.Base(fields[i+1])
	}
	if strings.HasSuffix(strings.ToLower(interp), ".exe") {
		interp = interp[:len(interp)-len(".exe")]
	}
	return interp, true
}

// shebangPeekBytes bounds how much of a candidate file peekShebangLine will
// ever read: a real interpreter line is always short, so this is generous
// headroom for one, not an attempt to capture more.
const shebangPeekBytes = 256

// ShebangPeekBytes is the bound callers use when sampling a HEAD blob for an
// extensionless path whose worktree copy is absent.
const ShebangPeekBytes = shebangPeekBytes

// peekShebangLine reads at most shebangPeekBytes from the worktree file at
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
// internal/synth and internal/diff both reach it through
// LanguageForWorktreePath below rather than each reading their own prefix.
func peekShebangLine(fullPath string) ([]byte, bool) {
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

// HeadShebangSample supplies a bounded sample of a path's HEAD blob. It is a
// callback rather than a gitx dependency so resolve stays independent of the
// git execution layer.
type HeadShebangSample func() (sample []byte, exists bool, err error)

// LanguageForPathFolding resolves relPath's language from its extension,
// then a bounded shebang peek. An existing worktree copy always wins the
// shebang lookup; only when that copy is absent does headSample get called
// for a bounded HEAD-blob peek. Extension lookup never calls either source,
// and fold applies to it alone -- relPath stays unchanged and shebang lookup
// is never folded.
//
// peeked reports whether a source was actually found and inspected,
// independent of ok: a source with no shebang or an unmapped interpreter
// still has peeked=true. headSample may be nil when HEAD fallback is not
// available -- LanguageForWorktreePathFolding is that case named.
func LanguageForPathFolding(root, relPath string, fold bool, headSample HeadShebangSample) (lang Language, ok bool, peeked bool, err error) {
	if lang, ok := ForExtensionFolding(filepath.Ext(relPath), fold); ok {
		return lang, true, false, nil
	}
	fullPath := filepath.Join(root, relPath)
	line, peeked := peekShebangLine(fullPath)
	if peeked {
		lang, ok = ForPathFolding(relPath, line, fold)
		return lang, ok, true, nil
	}
	if _, statErr := os.Stat(fullPath); statErr == nil || !errors.Is(statErr, os.ErrNotExist) {
		return nil, false, false, nil
	}
	if headSample == nil {
		return nil, false, false, nil
	}
	line, exists, err := headSample()
	if err != nil {
		return nil, false, false, err
	}
	if !exists {
		return nil, false, false, nil
	}
	lang, ok = ForPathFolding(relPath, line, fold)
	return lang, ok, true, nil
}

// LanguageForWorktreePathFolding resolves relPath from its extension or a
// bounded shebang peek of the worktree copy, optionally folding its
// extension. Callers that can fall back to a HEAD blob use
// LanguageForPathFolding directly.
func LanguageForWorktreePathFolding(root, relPath string, fold bool) (lang Language, ok bool, peeked bool) {
	lang, ok, peeked, _ = LanguageForPathFolding(root, relPath, fold, nil)
	return lang, ok, peeked
}
