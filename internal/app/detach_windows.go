//go:build windows

package app

import "syscall"

// A new process group keeps the watcher from receiving the console's Ctrl+C.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
