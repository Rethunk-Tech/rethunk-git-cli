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
//
// What catches this double drifting from a real os.Stat result (n17 in the
// 2026-07-29 audit): dial_test.go's own privateSocketDir cases call sameOwner
// through the real os.Lstat/os.Stat path on every directory they build (loose
// mode, a symlink, a non-directory, a missing base), so a fs.FileInfo whose
// Sys() shape or Mode() semantics diverged from what this repo's target
// platforms actually return would fail there, not silently pass here. The
// fs.FileInfo interface itself is the other half of that net: it is a
// compile-time seam sameOwner already treats as its only real dependency
// (a Sys() any it type-asserts), so nothing about the interface itself can
// drift out from under this fake without a build failure.
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

	self := uint32(os.Getuid()) //nolint:gosec // syscall.Stat_t.Uid is uint32 on supported Unix targets

	if !sameOwner(fakeDirInfo{uid: self}) {
		t.Error("sameOwner() = false for this process's own UID; want true")
	}
	if sameOwner(fakeDirInfo{uid: self + 1}) {
		t.Error("sameOwner() = true for a foreign UID; want false")
	}
}
