//go:build !windows

package app

import "syscall"

// A new session keeps the watcher alive after the terminal that started it closes.
func detachedAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
