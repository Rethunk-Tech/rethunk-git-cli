package lsp

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/util"
)

// stopGrace is how long a stranded daemon gets to honour SIGTERM before
// SIGKILL. It sits on a path that has already spent dialBudget+queryDeadline
// on a failed handshake, so it stays short.
const stopGrace = 500 * time.Millisecond

func pidPath(sockPath string) string { return sockPath + ".pid" }

// writePIDFile records the daemon rgit just spawned at sockPath. The
// temp-then-rename keeps a concurrent reader from ever seeing a partial PID.
func writePIDFile(sockPath string, pid int) {
	_ = util.WriteFileAtomic(pidPath(sockPath), []byte(strconv.Itoa(pid)))
}

// stopStrandedDaemon terminates the daemon rgit spawned at sockPath, whose
// socket answered but failed the handshake. Unlinking the socket alone would
// leave that process listening on an inode nothing can reach until its idle
// timeout. The PID is only signalled while its argv is exactly what
// trySpawnDaemon launched for this socket, so a recycled PID or a daemon
// someone else started is never touched.
func stopStrandedDaemon(spec serverSpec, sockPath string) {
	defer func() { _ = os.Remove(pidPath(sockPath)) }()
	data, err := os.ReadFile(pidPath(sockPath))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}
	ours := func() bool {
		argv, ok := processArgv(pid)
		return ok && len(argv) > 0 && filepath.Base(argv[0]) == spec.bin &&
			slices.Equal(argv[1:], spec.daemonArgs(sockPath))
	}
	if !ours() {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if proc.Signal(syscall.SIGTERM) != nil {
		return
	}
	for deadline := time.Now().Add(stopGrace); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !ours() {
			return
		}
	}
	if ours() {
		_ = proc.Kill()
	}
}
