//go:build !windows

package lsp

import (
	"io/fs"
	"os"
	"syscall"
)

// sameOwner reports whether info's directory entry is owned by this
// process's own UID. Permission bits alone cannot rule out another user on
// a multi-user host: an attacker can create their own directory with the
// exact 0700 mode rgit expects at the exact predictable path rgit expects
// it -- ownership is the check that actually excludes them.
func sameOwner(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(stat.Uid) == os.Getuid()
}

// pidDead reports whether pid is provably gone: kill(pid, 0) answering ESRCH.
// EPERM means a live process of another user, so it is not dead.
func pidDead(pid int) bool {
	return syscall.Kill(pid, 0) == syscall.ESRCH
}
