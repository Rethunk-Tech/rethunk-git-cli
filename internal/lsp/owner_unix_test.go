//go:build !windows

package lsp

import (
	"io/fs"
	"os"
	"syscall"
	"testing"
	"time"
)

// fakeDirInfo is a minimal fs.FileInfo whose Sys() returns a *syscall.Stat_t
// carrying a caller-chosen Uid, the only field sameOwner reads. Real
// ownership can only be produced with chown, which needs root and would not
// work in a test sandbox -- this is the cheapest way to drive the
// foreign-UID branch at all.
type fakeDirInfo struct{ uid uint32 }

func (fakeDirInfo) Name() string       { return "fake" }
func (fakeDirInfo) Size() int64        { return 0 }
func (fakeDirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o700 }
func (fakeDirInfo) ModTime() time.Time { return time.Time{} }
func (fakeDirInfo) IsDir() bool        { return true }
func (f fakeDirInfo) Sys() any         { return &syscall.Stat_t{Uid: f.uid} }

// TestSameOwner_ForeignUIDIsRejected drives the one branch that actually
// implements the planted-socket defense (dial.go's own doc comment on
// privateSocketDir: permission bits alone cannot rule out another user on a
// multi-user host, ownership is the check that does). dial_test.go's own
// privateSocketDir cases cover loose mode, a symlink, a non-directory, and a
// missing base -- but every one of them runs as this process's own UID, so
// none ever drives sameOwner's foreign-UID branch, the one this whole check
// exists for.
func TestSameOwner_ForeignUIDIsRejected(t *testing.T) {
	t.Parallel()

	self := uint32(os.Getuid())

	if !sameOwner(fakeDirInfo{uid: self}) {
		t.Error("sameOwner() = false for this process's own UID; want true")
	}
	if sameOwner(fakeDirInfo{uid: self + 1}) {
		t.Error("sameOwner() = true for a foreign UID; want false")
	}
}
