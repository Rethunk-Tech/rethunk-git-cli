//go:build linux

package lsp

import (
	"os"
	"strconv"
	"strings"
)

// processArgv returns pid's argv. An exited or zombie process reads as an
// empty cmdline, which reports ok=false like a missing one.
func processArgv(pid int) ([]string, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"), true
}
