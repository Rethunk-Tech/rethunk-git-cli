package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gittest"
)

func TestRunCommit_PorcelainEmitsCommitSHA(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.RepoWithFile(t.Context(), t, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n", "feat: add A")
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 2\n}\n")

	var stdout, stderr strings.Builder
	code := runCommit(t.Context(), dir, []string{"--dry-run", "--porcelain", "-m", "fix: update A", "a.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("dry-run porcelain = %v; stdout: %s; stderr: %s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "H\t") {
		t.Fatalf("dry-run porcelain = %q; must not emit a commit SHA", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runCommit(t.Context(), dir, []string{"--porcelain", "-m", "fix: update A", "a.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("porcelain commit = %v; stdout: %s; stderr: %s", code, stdout.String(), stderr.String())
	}

	sha := strings.TrimSpace(gittest.Git(t.Context(), t, dir, "rev-parse", "HEAD"))
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("porcelain output = %q; want SHA plus one target row", stdout.String())
	}
	if lines[0] != "H\t"+sha {
		t.Errorf("porcelain header = %q; want %q", lines[0], "H\t"+sha)
	}
	if !strings.HasPrefix(lines[1], "a.go\t\t") {
		t.Errorf("porcelain target row = %q; want a.go path record", lines[1])
	}

	stdout.Reset()
	stderr.Reset()
	code = runCommit(t.Context(), dir, []string{"--allow-empty", "--porcelain", "-m", "chore: empty", "a.go:A"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("allow-empty porcelain = %v; stdout: %s; stderr: %s", code, stdout.String(), stderr.String())
	}
	emptyLines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(emptyLines) != 1 || !strings.HasPrefix(emptyLines[0], "H\t") {
		t.Errorf("allow-empty porcelain = %q; want one H record", stdout.String())
	}
}

// TestRunCommit_DeletedMemberCountMatchesDiff pins docs/CODES.md's "every
// per-symbol row agrees with `rgit commit --dry-run`" for a deleted
// container member. A member is separated from its siblings by a line break,
// not a blank line, so its removal takes no extra line with it -- counting
// one anyway made the preview report a phantom deletion the commit never
// made.
func TestRunCommit_DeletedMemberCountMatchesDiff(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, path, head, work, anchor, symbol string
	}{
		{
			name:   "go struct field",
			path:   "t.go",
			head:   "package a\n\ntype B struct {\n\tA int\n\tC int\n}\n",
			work:   "package a\n\ntype B struct {\n\tC int\n}\n",
			anchor: "t.go:B.A",
			symbol: "B.A",
		},
		{
			name:   "python class method",
			path:   "t.py",
			head:   "class C:\n    def a(self):\n        return 1\n\n    def b(self):\n        return 2\n",
			work:   "class C:\n    def b(self):\n        return 2\n",
			anchor: "t.py:C.a",
			symbol: "C.a",
		},
		{
			name:   "typescript class method",
			path:   "t.ts",
			head:   "export class C {\n  a(): number {\n    return 1;\n  }\n\n  b(): number {\n    return 2;\n  }\n}\n",
			work:   "export class C {\n  b(): number {\n    return 2;\n  }\n}\n",
			anchor: "t.ts:C.a",
			symbol: "C.a",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, _ := gittest.RepoWithFile(t.Context(), t, tc.path, tc.head, "chore: fixture")
			gittest.Write(t, dir, tc.path, tc.work)

			diffOut, _, code := runApp(t, "-C", dir, "diff", "--porcelain")
			if code != exitcode.Success {
				t.Fatalf("diff = %v", code)
			}
			preview, _, code := runApp(t, "-C", dir, "commit", "--dry-run", "--porcelain", "-m", "feat: delete", tc.anchor)
			if code != exitcode.Success {
				t.Fatalf("commit --dry-run = %v", code)
			}

			want := rowFor(t, diffOut, tc.symbol)
			got := rowFor(t, preview, tc.symbol)
			if got != want {
				t.Errorf("dry-run row = %q; diff row = %q; want the preview to agree with the diff", got, want)
			}
		})
	}
}

// rowFor returns the ADDED and DELETED a porcelain stream reports for
// symbol, so a test compares counts across the two dialects without pinning
// the columns they do not share.
func rowFor(t *testing.T, porcelain, symbol string) string {
	t.Helper()
	var found []string
	for line := range strings.SplitSeq(strings.TrimRight(porcelain, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 2 && fields[1] == symbol {
			found = append(found, strings.Join(fields[len(fields)-2:], "\t"))
		}
	}
	if len(found) != 1 {
		t.Fatalf("porcelain %q: %d records name %q; want exactly one", porcelain, len(found), symbol)
	}
	return found[0]
}

// TestRunCommit_DuplicateAnchorListsOneRow pins the one-edit answer for an
// anchor named twice: `git add p p` stages p once, so two rows would
// double-count a single change for any caller summing them.
func TestRunCommit_DuplicateAnchorListsOneRow(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.RepoWithFile(t.Context(), t, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n", "feat: add A")
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 2\n}\n")

	stdout, stderr, code := runApp(t, "-C", dir, "commit", "--dry-run", "--porcelain", "-m", "feat: A", "a.go:A", "a.go:A")
	if code != exitcode.Success {
		t.Fatalf("dry-run = %v; stderr: %s", code, stderr)
	}
	if lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n"); len(lines) != 1 {
		t.Errorf("dry-run porcelain = %q; want one record for one edit", stdout)
	}

	stdout, stderr, code = runApp(t, "-C", dir, "commit", "--porcelain", "-m", "feat: A", "a.go:A", "a.go:A")
	if code != exitcode.Success {
		t.Fatalf("commit = %v; stderr: %s", code, stderr)
	}
	if lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n"); len(lines) != 2 {
		t.Errorf("commit porcelain = %q; want one H record plus one target record", stdout)
	}
	if got := gittest.Git(t.Context(), t, dir, "show", "HEAD:a.go"); !strings.Contains(got, "return 2") {
		t.Errorf("HEAD:a.go = %q; want the single edit committed", got)
	}
}

func TestRunCommit_QuietSuccessEmptyStdout(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.RepoWithFile(t.Context(), t, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n", "feat: add A")
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 2\n}\n")

	var stdout, stderr strings.Builder
	code := runCommit(t.Context(), dir, []string{"--quiet", "-m", "fix: update A", "a.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("quiet commit = %v; stdout: %s; stderr: %s", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("quiet stdout = %q; want empty", stdout.String())
	}
}

// An edit that keeps size and mtime is invisible to a stat check; git
// catches it only because an entry no older than the index file itself is
// "racy" and gets its content compared. The fixture pins both mtimes to one
// past instant (ctime is not settable, hence trustctime off), which is what
// a same-tick edit looks like under load.
func TestRunCommit_SameTickEditIsCommitted(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.New(t.Context(), t)
	gittest.Git(t.Context(), t, dir, "config", "core.trustctime", "false")
	path := filepath.Join(dir, "a.go")
	tick := time.Now().Add(-time.Hour)
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n")
	if err := os.Chtimes(path, tick, tick); err != nil {
		t.Fatal(err)
	}
	gittest.Commit(t.Context(), t, dir, "feat: add A")
	gittest.Write(t, dir, "a.go", "package a\n\nfunc A() int {\n\treturn 2\n}\n")
	for _, p := range []string{path, filepath.Join(dir, ".git", "index")} {
		if err := os.Chtimes(p, tick, tick); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr strings.Builder
	if code := runCommit(t.Context(), dir, []string{"--quiet", "-m", "fix: update A", "a.go"}, &stdout, &stderr); code != exitcode.Success {
		t.Fatalf("commit = %v; stderr: %s", code, stderr.String())
	}
	if got := gittest.Git(t.Context(), t, dir, "show", "HEAD:a.go"); !strings.Contains(got, "return 2") {
		t.Fatalf("HEAD:a.go = %q; want the same-tick edit committed", got)
	}
}
