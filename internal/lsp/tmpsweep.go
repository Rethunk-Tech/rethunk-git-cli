package lsp

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const stdioTmpPrefix = "rgit-lsp-"

// newStdioTmp makes the private TMPDIR for one stdio server. The owner's pid
// is in the name so sweepStdioTmp can tell an abandoned dir (its rgit was
// SIGKILLed or OOM-killed, so no cleanup ran) from a live invocation's.
func newStdioTmp() (string, error) {
	return os.MkdirTemp("", stdioTmpPrefix+strconv.Itoa(os.Getpid())+"-")
}

// sweepStdioTmp removes scratch dirs left by rgit processes that died before
// killAndReap ran. Only dirs owned by this uid whose embedded pid is dead are
// touched.
func sweepStdioTmp() {
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), stdioTmpPrefix+"*-*"))
	for _, m := range matches {
		pidStr, _, _ := strings.Cut(strings.TrimPrefix(filepath.Base(m), stdioTmpPrefix), "-")
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid <= 0 || !pidDead(pid) {
			continue
		}
		info, err := os.Lstat(m)
		if err != nil || !info.IsDir() || !sameOwner(info) {
			continue
		}
		_ = os.RemoveAll(m)
	}
}
