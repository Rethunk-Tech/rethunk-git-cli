package lsp

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// standInSpec launches sh as a stand-in daemon whose argv names sockPath,
// the way gopls's -listen flag does. ignoreTERM makes it survive SIGTERM so
// only the SIGKILL escalation can stop it.
func standInSpec(ignoreTERM bool) serverSpec {
	script := "while :; do sleep 0.05; done"
	if ignoreTERM {
		script = "trap '' TERM; " + script
	}
	return serverSpec{name: "test", bin: "sh", daemonArgs: func(sockPath string) []string {
		return []string{"-c", script, sockPath}
	}}
}

func startStandIn(t *testing.T, spec serverSpec, sockPath string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(spec.bin, spec.daemonArgs(sockPath)...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

// waitTERMIgnored blocks until the shell has installed its trap, so SIGTERM
// cannot land before it and end the process on the first signal.
func waitTERMIgnored(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		status, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
		for line := range strings.SplitSeq(string(status), "\n") {
			if mask, ok := strings.CutPrefix(line, "SigIgn:"); ok {
				bits, _ := strconv.ParseUint(strings.TrimSpace(mask), 16, 64)
				if bits&(1<<(syscall.SIGTERM-1)) != 0 {
					return
				}
			}
		}
	}
	t.Fatal("stand-in never ignored SIGTERM")
}

func TestStopStrandedDaemon_EscalatesToKill(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	spec := standInSpec(true)
	cmd := startStandIn(t, spec, sockPath)
	waitTERMIgnored(t, cmd.Process.Pid)
	writePIDFile(sockPath, cmd.Process.Pid)

	stopStrandedDaemon(spec, sockPath)

	var exitErr *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exitErr) {
		t.Fatalf("Wait() = %v; want the stand-in killed", err)
	}
	if ws := exitErr.Sys().(syscall.WaitStatus); !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
		t.Errorf("stand-in exit = %v; want SIGKILL after ignoring SIGTERM", ws)
	}
	if _, err := os.Stat(pidPath(sockPath)); !os.IsNotExist(err) {
		t.Errorf("pidfile = %v; want removed", err)
	}
}

// A PID recorded for this socket but now held by a process with a different
// argv is a recycled PID, and must survive.
func TestStopStrandedDaemon_RecycledPIDIsLeftAlone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "rgit-test.sock")
	other := startStandIn(t, standInSpec(false), filepath.Join(dir, "other.sock"))
	writePIDFile(sockPath, other.Process.Pid)

	stopStrandedDaemon(standInSpec(false), sockPath)

	if err := other.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("unrelated process signalled: %v", err)
	}
	if _, err := os.Stat(pidPath(sockPath)); !os.IsNotExist(err) {
		t.Errorf("pidfile = %v; want removed", err)
	}
}
