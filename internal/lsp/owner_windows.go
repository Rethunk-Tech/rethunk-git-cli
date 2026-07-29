//go:build windows

package lsp

import "io/fs"

// sameOwner always reports true on Windows. os.Getuid has no meaning there
// (it always returns -1), and the per-user temp directory rgit's
// runtimeDir falls back to is already ACL-restricted to the owning user by
// the platform itself -- the POSIX shared, world-writable /tmp finding 1 is
// about has no equivalent here for rgit to defend against the same way.
func sameOwner(fs.FileInfo) bool {
	return true
}
