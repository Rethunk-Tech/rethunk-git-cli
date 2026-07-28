// Package exitcode binds rgit's process exit statuses to named constants.
// No other package may spell an exit-status integer literal — every
// process-exit path routes through one of these.
//
// What each status means is specified once, in docs/CODES.md § Exit codes.
// A second prose copy here is the duplication CONTRIBUTING.md § Documentation
// forbids, and is how a code's meaning drifts from the table that defines it.
// The names carry the meaning; the table carries the detail.
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
	AnchorUnresolvable   Code = 3
	AnchorAmbiguous      Code = 4
	ContradictoryAnchors Code = 5
	ExtentMismatch       Code = 6
	PathRefused          Code = 7
	PushFailed           Code = 8
	UnsupportedLanguage  Code = 9
	SpecialPathRefused   Code = 10
	NothingToCommit      Code = 11
	GitFailure           Code = 128
	InvalidUsage         Code = 129
)
