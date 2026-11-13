package gitx

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
}

func lockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
