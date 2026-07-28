// Package exitcode binds rgit's process exit statuses to named constants.
// No other package may spell an exit-status integer literal — every
// process-exit path routes through one of these.
//
// What each status means is specified once, in docs/CODES.md § Exit codes.
// The trailing comments below are not a second copy of it: they carry only
// the scope or precondition a name cannot express — which command can
// produce a code, what distinguishes it from its neighbour, what is already
// true by the time it is returned. Everything a caller needs beyond that
// belongs in the table, so the two cannot drift into disagreeing.
//
// Exit 1 is deliberately absent: `rgit diff --exit-code` uses it for "there
// is something committable", git's own convention, and unlike every status
// below its meaning is conditional on a flag rather than fixed. It lives at
// its point of use, in internal/app.
package exitcode

// Code is a process exit status. See docs/CODES.md § Exit codes.
type Code int

const (
	Success              Code = 0
	AnchorUnresolvable   Code = 3   // absent from worktree AND HEAD, not merely ambiguous
	AnchorAmbiguous      Code = 4   // a bare name matched several candidates; they are listed
	ContradictoryAnchors Code = 5   // one path named as both a pathspec and a symbol anchor
	ExtentMismatch       Code = 6   // commit only; rgit diff reports the same disagreement as a warning
	PathRefused          Code = 7   // gitignored and untracked specifically, not any unusable path
	PushFailed           Code = 8   // the commit already succeeded; only the trailing --push failed
	UnsupportedLanguage  Code = 9   // symbol anchor on a file with no grammar; naming the path still works
	SpecialPathRefused   Code = 10  // symlink, gitlink or binary; distinct from PathRefused
	NothingToCommit      Code = 11  // EVERY named target was unchanged; --allow-empty suppresses it
	GitFailure           Code = 128 // includes hook rejection and GPG failure, not just git's own faults
	InvalidUsage         Code = 129
)
