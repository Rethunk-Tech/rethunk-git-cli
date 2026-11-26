package gitx

import "context"

// SetBeforeOnlyCommit runs f between Commit's HEAD check and git commit
// starting, the window a writer outside rgit's commit lock can land in.
func SetBeforeOnlyCommit(f func()) (restore func()) {
	prev := beforeOnlyCommit
	beforeOnlyCommit = f
	return func() { beforeOnlyCommit = prev }
}

// SetAfterOnlyGitCommit runs f once git commit has returned for a --only
// commit, the window before its index sync that a sibling commit can land in.
func SetAfterOnlyGitCommit(f func()) (restore func()) {
	prev := afterOnlyGitCommit
	afterOnlyGitCommit = f
	return func() { afterOnlyGitCommit = prev }
}

// FinishOnlyCommit exposes finishOnlyCommit so a test can drive its
// after-the-fact HEAD check without a hook arming the guard marker.
func (r *Repo) FinishOnlyCommit(ctx context.Context, opts CommitOptions) error {
	return r.finishOnlyCommit(ctx, r, opts)
}

// ResetCommand exposes resetCommand to the external test package.
var ResetCommand = resetCommand
