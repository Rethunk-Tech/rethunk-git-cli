package gitx

import "context"

// SetBeforeOnlyCommit runs f between Commit's HEAD check and git commit
// starting, the window a writer outside rgit's commit lock can land in.
func SetBeforeOnlyCommit(f func()) (restore func()) {
	prev := beforeOnlyCommit
	beforeOnlyCommit = f
	return func() { beforeOnlyCommit = prev }
}

// FinishOnlyCommit exposes finishOnlyCommit so a test can drive its
// after-the-fact HEAD check without a hook arming the guard marker.
func (r *Repo) FinishOnlyCommit(ctx context.Context, opts CommitOptions) error {
	return r.finishOnlyCommit(ctx, r, opts)
}
