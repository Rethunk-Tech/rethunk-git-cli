//go:build !windows

package gitx

import (
	"errors"
	"os"
	"syscall"
)

func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func lockBusy(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}
