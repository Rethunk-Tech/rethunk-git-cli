//go:build !linux

package lsp

// processArgv reports ok=false where there is no /proc to read argv from:
// without proof the PID is still rgit's daemon, it is never signalled, and a
// stranded daemon falls back to its own idle timeout.
func processArgv(int) ([]string, bool) { return nil, false }
