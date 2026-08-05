// Package diff implements `rgit diff`'s execution: resolving which of the
// four scopes docs/USAGE.md § Diff scope describes applies, enumerating the
// changed files within it, attributing each file's hunks to the symbol
// anchors docs/ANCHORS.md defines, and rendering the result in the default
// aligned layout or --porcelain's stable tab-separated form.
//
// The governing promise (AGENTS.md, docs/USAGE.md) is that every FILE:NAME
// label this package prints is exactly the string internal/resolve accepts
// back — rgit diff and rgit commit must agree on what a symbol is called.
package diff

import "github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"

// Status is one row's classification. The six porcelain spellings it maps to
// are specified in docs/CODES.md § Output records; StatusNoSymbols is
// internal to this package, an unsupported-language file reported as MOD
// since it is an ordinary modification that merely could not be split by
// symbol.
type Status int

const (
	StatusMod Status = iota
	StatusDeleted
	StatusUnanchorable
	StatusUntracked
	StatusMode
	StatusBinary
	StatusNoSymbols
)

// Porcelain returns the exact status token docs/CODES.md § Output records
// specifies for --porcelain output.
func (s Status) Porcelain() string {
	switch s {
	case StatusDeleted:
		return "DELETED"
	case StatusUnanchorable:
		return "UNANCHORABLE"
	case StatusUntracked:
		return "UNTRACKED"
	case StatusMode:
		return "MODE"
	case StatusBinary:
		return "BINARY"
	default: // StatusMod, StatusNoSymbols
		return "MOD"
	}
}

// Row is one line of rgit diff output. Symbol is empty for every
// file-level row (unanchorable, untracked, mode, binary, no-symbols); a
// non-empty Symbol is always the anchor rgit commit accepts back.
type Row struct {
	Symbol  string
	Status  Status
	Added   string // numstat-shaped: a decimal count, or "-" for binary
	Deleted string

	// ModeNote is "644->755"-shaped, set only for StatusMode rows.
	ModeNote string

	// HintSymbol is the resolvable first symbol of a StatusUntracked row's
	// file, for the "-> use --sym FILE:NAME or --file FILE" hint. Empty
	// when the file has none.
	HintSymbol string

	// pos is the row's byte offset in the file, used only to order rows
	// within that file. Output has to be sorted by path then by position so
	// it is greppable and identical between runs on an unchanged tree;
	// discovery order would put @imports last despite it being the first
	// thing in the file.
	pos uint
}

// FileReport is every row rgit diff has to say about one file.
type FileReport struct {
	Path string
	Rows []Row

	// lang is the resolved language's Name() (resolve.Language), set only
	// when buildFileReport actually reached attributeSymbols -- the same
	// resolution that already ran the shebang fallback for an extensionless
	// path (resolve.LanguageForWorktreePath). render.go's unanchorableHint
	// reads it instead of re-deriving the language from Path's extension
	// alone, so an extensionless file routed by shebang gets the same
	// answer here as everywhere else in the pipeline. Unexported: it is
	// this package's own bookkeeping, never part of the porcelain contract
	// docs/CODES.md specifies.
	lang string
}

// Report is the full result of one rgit diff invocation, already filtered
// and sorted for rendering.
type Report struct {
	Files []FileReport

	// Warnings are extents a live language server disagreed with. rgit diff
	// reports rather than gates: the same disagreement is exit 6 at commit
	// time, and learning about it while reading a diff is the point of
	// saying so here (docs/CODES.md § Exit codes, exit 6).
	Warnings []string

	// TSOnly reports that at least one file had symbols to cross-check and
	// no live language server was reached to check them, the same condition
	// rgit commit announces via synth.Plan.TSOnly. It is deliberately not
	// set for a cross-check that was never applicable -- a revision-to-
	// revision diff, which no server can see, or a file with no addressable
	// declaration -- since a signal that fires when nothing was wrong is one
	// readers learn to ignore.
	TSOnly bool

	// Patch is git's own raw patch body, set only when Options.Patch was
	// true -- opt-in, not a second format this package invents. It covers
	// the identical scope and pathspec filter as the symbol-attributed Files
	// above, since Run derives both from the same NumstatArgs/pathspecs.
	Patch []byte
}

// Dirty reports whether anything in the report is committable — the
// question docs/USAGE.md's --exit-code and --quiet flags answer.
func (r *Report) Dirty() bool {
	for _, f := range r.Files {
		if len(f.Rows) > 0 {
			return true
		}
	}
	return false
}

// SymRef is a FILE:NAME anchor filter: --sym's explicit form or a bare
// KindAnchor positional (docs/USAGE.md § Flags: the two are equivalent).
type SymRef struct {
	File string
	Name string
}

// Options is runDiff's fully-classified input: flags plus the buckets
// cli.ClassifyArgs's positionals sorted into (see BucketClassified in classify.go).
type Options struct {
	Staged   bool
	Unstaged bool

	// RangeFlag is --range's value. PositionalRange is a bare ".."/"..."
	// shaped positional pulled out ahead of cli.ClassifyArgs by
	// ExtractRangeToken, since git's own rev-parse --verify — which rule 3
	// relies on — rejects range syntax outright. At most one may be set;
	// ResolveScope rejects both together.
	RangeFlag       string
	PositionalRange string

	// Revisions are bare revision positionals rule 3 resolved (KindRevision),
	// in argument order. 0 with no other scope selector is the default
	// scope; 1 diffs the worktree against that revision; 2 diffs the first
	// against the second — both mirroring plain git diff's own positional
	// forms.
	Revisions []string

	// Files are pathspecs that scope the git-level query: --file values
	// plus bare KindPathspec positionals. Passed to git verbatim.
	Files []string

	// Syms filter rendered output to specific anchors: --sym values plus
	// bare KindAnchor positionals. Unlike Files, these narrow rendering
	// only — git has no notion of a symbol (docs/USAGE.md § Diff scope).
	Syms []SymRef

	// Patch opts into fetching git's own real patch body alongside the
	// symbol-attributed report, stored in Report.Patch. Purely additive:
	// false changes nothing about the rest of Run's behavior.
	Patch bool

	// RevPaths is the two-blob "A:f.go B:f.go" scope: exactly two
	// cli.RevPath values naming the identical path at two revisions
	// (BucketClassified's own pairing rule, classify.go), or empty for
	// every other scope. ResolveScope compares that one file across the
	// two named revisions directly, with no numstat walk of the rest of
	// the tree.
	RevPaths []cli.RevPath
}

// UsageError is a scope- or argument-shape problem Run detects itself,
// distinct from a *gitx.GitError or *gitx.ExecError: it maps to
// exitcode.InvalidUsage rather than exitcode.GitFailure.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }
