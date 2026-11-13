package gitx

// SetBeforeOnlyCommit runs f between Commit's HEAD check and git commit
// starting, the window a writer outside rgit's commit lock can land in.
func SetBeforeOnlyCommit(f func()) (restore func()) {
	prev := beforeOnlyCommit
	beforeOnlyCommit = f
	return func() { beforeOnlyCommit = prev }
}
