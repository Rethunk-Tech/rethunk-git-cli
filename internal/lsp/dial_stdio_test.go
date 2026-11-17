package lsp

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type errCloser struct {
	io.ReadWriteCloser
	closeErr error
}

func (e errCloser) Close() error {
	_ = e.ReadWriteCloser.Close()
	return e.closeErr
}

func TestPipeRWCClose(t *testing.T) {
	t.Parallel()
	werr := errors.New("write close")
	rerr := errors.New("read close")

	t.Run("both nil", func(t *testing.T) {
		t.Parallel()
		pr, pw, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := (pipeRWC{ReadCloser: pr, WriteCloser: pw}).Close(); err != nil {
			t.Errorf("Close() = %v; want nil", err)
		}
	})
	t.Run("write error wins", func(t *testing.T) {
		t.Parallel()
		pr, pw, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		got := pipeRWC{
			ReadCloser:  errCloser{pr, rerr},
			WriteCloser: errCloser{pw, werr},
		}.Close()
		if !errors.Is(got, werr) {
			t.Errorf("Close() = %v; want write error %v", got, werr)
		}
	})
	t.Run("read error", func(t *testing.T) {
		t.Parallel()
		pr, pw, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		got := pipeRWC{
			ReadCloser:  errCloser{pr, rerr},
			WriteCloser: errCloser{pw, nil},
		}.Close()
		if !errors.Is(got, rerr) {
			t.Errorf("Close() = %v; want read error %v", got, rerr)
		}
	})
}

func TestAcquireSpawnLock_MissingParent(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "missing", "rgit-test.sock")
	lock, ok := acquireSpawnLock(sockPath)
	if lock != nil || ok {
		t.Errorf("acquireSpawnLock() = %v, %v; want nil, false when parent is missing", lock, ok)
	}
}

func TestAcquireSpawnLock_DirectoryLockIsLeftAlone(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	if err := os.Mkdir(sockPath+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	lock, ok := acquireSpawnLock(sockPath)
	if lock != nil || ok {
		t.Errorf("acquireSpawnLock() = %v, %v; want nil, false for a directory lock", lock, ok)
	}
	if info, err := os.Lstat(sockPath + ".lock"); err != nil || !info.IsDir() {
		t.Errorf("directory lock = %v, %v; want left in place", info, err)
	}
}

func TestAcquireSpawnLock_StaleLockUnlinkFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "rgit-test.sock")
	writeStaleLock(t, sockPath)
	if err := os.Chmod(dir, 0o555); err != nil { //nolint:gosec // directory must not be writable so Remove of the stale lock fails
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // restore so TempDir cleanup can remove it
			t.Errorf("restore dir mode: %v", err)
		}
	})
	lock, ok := acquireSpawnLock(sockPath)
	if lock != nil || ok {
		t.Errorf("acquireSpawnLock() = %v, %v; want nil, false when the stale lock cannot be removed", lock, ok)
	}
}

func TestAcquireSpawnLock_Acquires(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(t.TempDir(), "rgit-test.sock")
	lock, ok := acquireSpawnLock(sockPath)
	if !ok || lock == nil {
		t.Fatalf("acquireSpawnLock() = %v, %v; want lock, true", lock, ok)
	}
	t.Cleanup(func() {
		if err := lock.Close(); err != nil {
			t.Errorf("close lock: %v", err)
		}
	})
	if _, err := os.Stat(sockPath + ".lock"); err != nil {
		t.Errorf("lock file missing after acquire: %v", err)
	}
}

func TestDialStdio_BinaryNotOnPATH(t *testing.T) {
	t.Parallel()
	spec := serverSpec{name: "test", bin: "rgit-lsp-test-binary-does-not-exist"}
	client, degrade := dialStdio(t.Context(), spec, t.TempDir())
	if client != nil || !degrade {
		t.Errorf("dialStdio() = %v, %v; want nil, true", client, degrade)
	}
}

func TestDialStdio_StartFailureDegrades(t *testing.T) {
	t.Parallel()
	bin := filepath.Join(t.TempDir(), "not-a-binary")
	if err := os.WriteFile(bin, []byte("not a binary"), 0o755); err != nil { //nolint:gosec // executable mode is the behavior under test
		t.Fatal(err)
	}
	spec := serverSpec{name: "test", bin: bin}
	client, degrade := dialStdio(t.Context(), spec, t.TempDir())
	if client != nil || !degrade {
		t.Errorf("dialStdio() = %v, %v; want nil, true", client, degrade)
	}
}

func TestDialStdio_HandshakeFailureDegrades(t *testing.T) {
	t.Parallel()
	spec := serverSpec{name: "test", bin: "true"}
	client, degrade := dialStdio(t.Context(), spec, t.TempDir())
	if client != nil || !degrade {
		t.Errorf("dialStdio() = %v, %v; want nil, true", client, degrade)
	}
}

const fakeLSPMain = `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func main() {
	in := bufio.NewReader(os.Stdin)
	for {
		var n int
		for {
			line, err := in.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "content-length") {
				n, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if n == 0 {
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(in, body); err != nil {
			return
		}
		var msg struct {
			ID     json.RawMessage ` + "`json:\"id\"`" + `
			Method string          ` + "`json:\"method\"`" + `
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			return
		}
		if len(msg.ID) == 0 || string(msg.ID) == "null" {
			if msg.Method == "exit" {
				return
			}
			continue
		}
		result := ` + "`{\"capabilities\":{}}`" + `
		if msg.Method == "shutdown" {
			result = "null"
		}
		resp := fmt.Sprintf(` + "`{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}`" + `, msg.ID, result)
		fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(resp), resp)
	}
}
`

func buildFakeLSP(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(fakeLSPMain), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fake-lsp\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "fake-lsp")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".") //nolint:gosec // fixed argv: compile the in-test fake LSP helper
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build fake-lsp: %v\n%s", err, out)
	}
	return bin
}

func TestDialStdio_SuccessfulHandshake(t *testing.T) {
	t.Parallel()
	spec := serverSpec{name: "test", bin: buildFakeLSP(t)}
	client, degrade := dialStdio(t.Context(), spec, t.TempDir())
	if degrade || client == nil {
		t.Fatalf("dialStdio() = %v, degrade=%v; want client, false", client, degrade)
	}
	// Close's own return is not asserted nil: killAndReap always SIGKILLs
	// as insurance, so Wait can report "signal: killed".
	first := client.Close()
	if second := client.Close(); second != first { //nolint:errorlint // closeOnce must return the identical value
		t.Errorf("second Close() = %v; want same as first %v (closeOnce)", second, first)
	}
	if _, err := client.DocumentSymbols(t.Context(), "x.go", []byte("package x\n")); err == nil {
		t.Error("DocumentSymbols after Close = nil error; want one -- the connection should already be closed")
	}
}
