package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"
)

// TestDial_NewServers exercises each server added to the servers map
// alongside YAML/JSON/CSS/Markdown for real, the same "dial the actual
// dependency" discipline CONTRIBUTING.md holds the live-gopls check to:
// skip cleanly when the binary is not on PATH or -short is set, dial and
// query for real otherwise. A double here would only prove this package
// calls a mock the way its author expected, exactly the class of defect
// specs/design.md's cross-check coverage warns a stand-in produces.
func TestDial_NewServers(t *testing.T) {
	t.Parallel()
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

			// 10s, not 5s: comfortably covers one degraded attempt below
			// (bounded internally by dialBudget+queryDeadline, ~2.15s) plus
			// a second, successful one plus the DocumentSymbols query after
			// it -- generous because this bounds only how long this test
			// is willing to wait, never Dial's own dialBudget, which stays
			// exactly what a real caller gets.
			//
			// Every non-gopls server here is a one-shot stdio subprocess
			// (dialStdio's exec.CommandContext ties the subprocess itself
			// to this ctx, not just the handshake), so this same ctx has to
			// stay live through Dial and DocumentSymbols both -- a shorter-
			// lived context created just to bound the retry below would
			// kill a successfully dialled server out from under the query
			// that follows it.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Retry once on a degraded Dial before failing. Degrading
			// within dialBudget is Dial's own designed behaviour under
			// load (AGENTS.md: "Never block on a cold server"), not a
			// wiring defect -- every one of these servers is spawned fresh
			// per query (dialStdio), so losing a single race against a
			// loaded scheduler is exactly the contention dialBudget exists
			// to protect a real caller from, not the regression this test
			// exists to catch. A genuine regression (a broken invocation,
			// a rejected handshake, wrong initialize params) fails to
			// connect on every attempt, not just one, so retrying once is
			// the cheapest way to tell the two apart -- raising dialBudget
			// itself would only turn a fast flake into a slow one while
			// leaving the assertion just as environment-dependent.
			//
			// Measured, not assumed: this subtest ("markdown") lost the
			// race while three agents were saturating this same checkout,
			// marksman taking ~2.2s to fail against a 150ms dialBudget --
			// contention real enough that a second attempt, moments later,
			// is a materially different roll, not a rubber stamp.
			client, degraded := Dial(ctx, tc.lang, dir)
			if degraded {
				client, degraded = Dial(ctx, tc.lang, dir)
			}
			if degraded {
				t.Fatalf("Dial(%q) degraded twice in a row with %s on PATH", tc.lang, tc.bin)
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
// other unsupported/absent-server case already has. PATH is stripped down
// to a fresh, empty temp dir rather than skipped when yaml-language-server
// happens to already be on it: skipping there means any dev box or CI
// runner with servers installed never exercises this path at all -- and
// worse, the skip used to come after the Dial call below, so on an
// equipped machine this actually dialled a real server and discarded the
// result before ever checking whether to skip. cmd/rgit/rgit_e2e_test.go's
// TestDocumentedPathsWithoutOtherCoverage strips PATH the same way for its
// own [ts-only] case.
func TestDial_NewServers_Degraded(t *testing.T) {
	// cannot Parallel because t.Setenv("PATH", ...) below
	t.Setenv("PATH", t.TempDir())

	_, degraded := Dial(context.Background(), "yaml", t.TempDir())
	if !degraded {
		t.Error("Dial(\"yaml\") with no yaml-language-server on PATH = not degraded; want degraded")
	}
}

// TestServers pins Servers()'s contract against the package-level servers
// map directly, so a caller like doctor.go can trust it rather than a
// second hand list: every wired binary appears exactly once, sorted, with
// every language that dials it grouped under it.
func TestServers(t *testing.T) {
	t.Parallel()
	got := Servers()

	byBin := map[string][]string{}
	var order []string
	for _, s := range got {
		if _, ok := byBin[s.Bin]; ok {
			t.Errorf("Servers() lists %q more than once", s.Bin)
		}
		byBin[s.Bin] = s.Languages
		order = append(order, s.Bin)
	}
	if !sort.StringsAreSorted(order) {
		t.Errorf("Servers() bins are not sorted: %v", order)
	}

	for lang, spec := range servers {
		langs := byBin[spec.bin]
		if !slices.Contains(langs, lang) {
			t.Errorf("Servers()[%q].Languages = %v; want it to contain %q (servers[%q].bin)", spec.bin, langs, lang, lang)
		}
	}

	wantBins := map[string]bool{}
	for _, spec := range servers {
		wantBins[spec.bin] = true
	}
	for bin := range byBin {
		if !wantBins[bin] {
			t.Errorf("Servers() has %q, which no servers map entry's bin matches", bin)
		}
	}
}
