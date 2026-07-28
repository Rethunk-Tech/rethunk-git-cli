// Package exitcode holds the complete rgit exit-code table as named
// constants. No other package may spell an exit-status integer literal —
// every process-exit path routes through one of these.
package exitcode

// Code is a process exit status, per docs/USAGE.md § Exit codes.
type Code int

const (
	// Success indicates the command completed. Unchanged-target warnings may
	// still have been printed to stderr; a warning alone does not fail the run.
	Success Code = 0

	// AnchorUnresolvable means a symbol anchor named a symbol missing from
	// both the worktree and HEAD. Candidate names, if any were found by a
	// fuzzy match, are listed on stderr.
	AnchorUnresolvable Code = 3

	// AnchorAmbiguous means a bare symbol name matched more than one
	// candidate (e.g. two receivers sharing a method name). Candidates are
	// listed so the caller can qualify the name.
	AnchorAmbiguous Code = 4

	// ContradictoryAnchors means --sym and --file were both given for the
	// same path in one invocation.
	ContradictoryAnchors Code = 5

	// ExtentMismatch means the language server's range disagreed with
	// tree-sitter's normalized (declaration-only) extent for an anchor.
	// Nothing is staged when this happens.
	ExtentMismatch Code = 6

	// PathRefused means a named path is gitignored and not already tracked,
	// matching plain git add's refusal.
	PathRefused Code = 7

	// PushFailed means the commit itself succeeded but the trailing --push
	// failed. The commit is not rolled back.
	PushFailed Code = 8

	// UnsupportedLanguage means a symbol anchor targeted a file whose
	// language has no v1 grammar. Name the path instead of a symbol.
	UnsupportedLanguage Code = 9

	// SpecialPathRefused means a symbol anchor targeted a symlink, gitlink,
	// binary, or otherwise non-parseable path. Name the path instead.
	SpecialPathRefused Code = 10

	// NothingToCommit means every named target resolved cleanly but none had
	// uncommitted changes. --allow-empty suppresses this.
	NothingToCommit Code = 11

	// GitFailure covers fatal git or system failures: hook rejection, GPG
	// failure, and anything else git itself would exit non-zero for. Mirrors
	// git's own exit 128 convention.
	GitFailure Code = 128

	// InvalidUsage covers bad flags, a missing commit message, no targets,
	// an invalid flag combination, or a path-escape attempt. Mirrors git's
	// own exit 129 convention for usage errors.
	InvalidUsage Code = 129
)
