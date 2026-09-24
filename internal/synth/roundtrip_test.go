package synth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// TestApplyEdits_IdentitySpliceIsByteIdentical is the write-side counterpart
// to the LSP cross-check: for every declaration a grammar finds in a real
// file, replace that extent with its own bytes and require the result to
// equal the input exactly.
//
// A single such replacement is near-tautological; applying all of a file's
// declarations at once is not. Nested declarations produce genuinely
// overlapping extents -- a CSS rule contains its nested rule, a Go struct its
// fields -- so this drives coalesceOverlaps, mergeInsertTies, and the
// reverse-byte-offset ordering the whole package rests on, against real
// source rather than a fixture built to suit them.
func TestApplyEdits_IdentitySpliceIsByteIdentical(t *testing.T) {
	t.Parallel()

	for _, path := range identityCorpus(t) {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src, err := os.ReadFile(path) //nolint:gosec // identity corpus paths are explicit test inputs
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			lang, ok := resolve.ForExtension(filepath.Ext(name))
			if !ok {
				t.Skipf("no grammar for %s", name)
			}
			decls, err := resolve.DeclExtents(lang, src)
			if err != nil {
				t.Fatalf("DeclExtents(%s): %v", name, err)
			}
			if len(decls) == 0 {
				t.Skipf("%s declares nothing", name)
			}

			ops := make([]editOp, 0, len(decls))
			for _, d := range decls {
				ops = append(ops, editOp{
					kind:  editReplace,
					start: d.Extent.Start,
					end:   d.Extent.End,
					text:  src[d.Extent.Start:d.Extent.End],
				})
			}

			got := applyEdits(src, ops)
			if string(got) != string(src) {
				t.Errorf("%s: replacing every declaration with its own bytes changed the file\n"+
					"declarations: %d\nwant %d bytes, got %d", name, len(decls), len(src), len(got))
			}
		})
	}
}

// identityCorpus is the committed fixture set by default -- the same one
// `make xcheck` measures the read side against, so one corpus serves both
// directions. SYNTH_CORPUS names a file of newline-separated paths instead,
// which is how this property gets pointed at real source: run over 211 fleet
// files across all ten grammars, every one came back byte-identical.
func identityCorpus(t *testing.T) []string {
	t.Helper()
	if list := os.Getenv("SYNTH_CORPUS"); list != "" {
		data, err := os.ReadFile(list) //nolint:gosec // SYNTH_CORPUS is an explicit test-only fixture list
		if err != nil {
			t.Fatalf("read SYNTH_CORPUS %s: %v", list, err)
		}
		var paths []string
		for line := range strings.SplitSeq(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				paths = append(paths, line)
			}
		}
		if len(paths) == 0 {
			t.Fatalf("SYNTH_CORPUS %s named no files", list)
		}
		return paths
	}

	dir := filepath.Join("..", "resolve", "testdata", "xcheck")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	if len(paths) == 0 {
		t.Fatal("corpus is empty")
	}
	return paths
}
