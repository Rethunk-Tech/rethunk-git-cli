// Package exitcode binds rgit's process exit statuses to named constants.
// No other package may spell an exit-status integer literal — every
// process-exit path routes through one of these.
//
// What each status means is specified once, in docs/CODES.md § Exit codes.
// The comments below are not a second copy of it: each carries only the
// scope or precondition a name cannot express — which command can produce
// a code, what distinguishes it from its neighbour, what is already true
// by the time it is returned. A constant whose name needs no such warning
// carries no comment. Everything beyond that belongs in the table, so the
// two cannot drift into disagreeing.
//
// Exit 8 is retired with `rgit commit --push` and not reused.
//
// Exit 1 is deliberately absent: `rgit diff --exit-code` uses it for "there
// is something committable", git's own convention, and unlike every status
// below its meaning is conditional on a flag rather than fixed. It lives at
// its point of use, in internal/app.
package exitcode

// Code is a process exit status. See docs/CODES.md § Exit codes.
type Code int

const (
	Success Code = 0

	// LandConflict means `land --branch` could not rebase the branch.
	LandConflict Code = 2

	// AnchorUnresolvable means absent from worktree AND HEAD, not ambiguous.
	AnchorUnresolvable Code = 3

	// AnchorAmbiguous means a bare name matched several listed candidates.
	AnchorAmbiguous Code = 4

	// ContradictoryAnchors means one path named as pathspec and as anchor.
	ContradictoryAnchors Code = 5

	// ExtentMismatch is commit's alone; rgit diff warns and continues.
	ExtentMismatch Code = 6

	// PathRefused means gitignored and untracked, not any unusable path.
	PathRefused Code = 7

	// UnsupportedLanguage means no grammar; naming the path still works.
	UnsupportedLanguage Code = 9

	// SpecialPathRefused means a commit symbol anchor hit a special path; not
	// PathRefused. See docs/CODES.md.
	SpecialPathRefused Code = 10

	// NothingToCommit means EVERY named target was unchanged, not some.
	NothingToCommit Code = 11

	// StructuredDataAnchorRefused means commit's alone: a FILE:SYMBOL
	// anchor targeted JSON/YAML/TOML. Name the path instead.
	StructuredDataAnchorRefused Code = 12

	// LandGateRed means `land --branch`'s gate stayed red after a --failed re-run.
	LandGateRed Code = 3

	// LandNotEnqueued means `land --branch`'s merge-queue PR was not enqueued.
	LandNotEnqueued Code = 4

	// LandInstallFailed means a `land` install, including a catch-up install, failed.
	LandInstallFailed Code = 5

	// DirtyOverlap means a local (non---push) `land --branch` could not fast-forward because the
	// checkout's own uncommitted edits touch the same files: retry later.
	DirtyOverlap Code = 13

	// GitFailure includes hook rejection and GPG failure, not only git's.
	GitFailure Code = 128

	InvalidUsage Code = 129
)
