package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestDial_NewServers exercises each server added to the servers map
// alongside YAML/JSON/CSS/Markdown for real, the same "dial the actual
// dependency" discipline CONTRIBUTING.md holds the live-gopls check to:
// skip cleanly when the binary is not on PATH or -short is set, dial and
// query for real otherwise. A double here would only prove this package
// calls a mock the way its author expected, exactly the class of defect
// specs/design.md's cross-check survey warns a stand-in produces.
func TestDial_NewServers(t *testing.T) {
	if testing.Short() {
		t.Skip("live language-server dial skipped under -short")
	}

	tests := []struct {
		lang string
		bin  string
		ext  string
		src  string
	}{
		{"yaml", "yaml-language-server", ".yaml", "a:\n  b: 1\n"},
		{"json", "vscode-json-language-server", ".json", "{\n  \"a\": 1\n}\n"},
		{"css", "vscode-css-language-server", ".css", ".a {\n  color: red;\n}\n"},
		{"markdown", "marksman", ".md", "# A\n\nbody\n"},
	}

	for _, tc := range tests {
		t.Run(tc.lang, func(t *testing.T) {
			if _, err := exec.LookPath(tc.bin); err != nil {
				t.Skipf("%s not on PATH", tc.bin)
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "fixture"+tc.ext)
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			client, degraded := Dial(ctx, tc.lang, dir)
			if degraded {
				t.Fatalf("Dial(%q) degraded with %s on PATH", tc.lang, tc.bin)
			}
			defer func() { _ = client.Close() }()

			syms, err := client.DocumentSymbols(ctx, path, []byte(tc.src))
			if err != nil {
				t.Fatalf("DocumentSymbols: %v", err)
			}
			if len(syms) == 0 {
				t.Errorf("DocumentSymbols returned no symbols for %s fixture", tc.lang)
			}
		})
	}
}

// TestDial_NewServers_Degraded covers the absent-binary side for a
// language with no chance of a real server on the test machine: Dial must
// report degraded rather than block or error, the same contract every
// other unsupported/absent-server case already has.
func TestDial_NewServers_Degraded(t *testing.T) {
	_, degraded := Dial(context.Background(), "yaml", t.TempDir())
	if _, err := exec.LookPath("yaml-language-server"); err == nil {
		t.Skip("yaml-language-server is on PATH; this case wants it absent")
	}
	if !degraded {
		t.Error("Dial(\"yaml\") with no yaml-language-server on PATH = not degraded; want degraded")
	}
}
