package synth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
)

// TestOpenFilePlan_UnsupportedLanguageReason pins the exit-9 message
// openFilePlan builds when no grammar matches. The old message was always
// "no grammar registered for " + ext, which for an extensionless path
// rendered as a dangling "... for " with no sign that shebang sniffing was
// even attempted -- these cases pin the replacement across an unmapped
// extension, an unmapped shebang, and no worktree copy to sniff at all.
func TestOpenFilePlan_UnsupportedLanguageReason(t *testing.T) {
	t.Parallel()

	t.Run("unmapped extension with no shebang names both", func(t *testing.T) {
		t.Parallel()
		dir, repo := gittest.New(t)
		gittest.Write(t, dir, "main.rs", "fn main() {}\n")

		_, err := openFilePlan(context.Background(), repo, dir, "main.rs")

		var perr *PathError
		if !errors.As(err, &perr) {
			t.Fatalf("openFilePlan error = %v; want *PathError", err)
		}
		want := `no grammar registered for main.rs (extension ".rs", shebang unmapped)`
		if perr.Reason != want {
			t.Errorf("Reason = %q; want %q", perr.Reason, want)
		}
	})

	t.Run("extensionless path with an unmapped shebang", func(t *testing.T) {
		t.Parallel()
		dir, repo := gittest.New(t)
		gittest.Write(t, dir, "bin/hook", "echo hi\n")

		_, err := openFilePlan(context.Background(), repo, dir, "bin/hook")

		var perr *PathError
		if !errors.As(err, &perr) {
			t.Fatalf("openFilePlan error = %v; want *PathError", err)
		}
		want := `no grammar registered for bin/hook (extension "", shebang unmapped)`
		if perr.Reason != want {
			t.Errorf("Reason = %q; want %q", perr.Reason, want)
		}
	})

	t.Run("extensionless path deleted from the worktree has no shebang to sniff", func(t *testing.T) {
		t.Parallel()
		dir, repo := gittest.New(t)
		gittest.Write(t, dir, "bin/hook", "echo hi\n")
		gittest.Commit(t, dir, "chore: add hook")
		if err := os.Remove(filepath.Join(dir, "bin", "hook")); err != nil {
			t.Fatal(err)
		}

		_, err := openFilePlan(context.Background(), repo, dir, "bin/hook")

		var perr *PathError
		if !errors.As(err, &perr) {
			t.Fatalf("openFilePlan error = %v; want *PathError", err)
		}
		want := `no grammar registered for bin/hook (extension "", no worktree file to sniff a shebang from)`
		if perr.Reason != want {
			t.Errorf("Reason = %q; want %q", perr.Reason, want)
		}
	})
}

// TestPathspecFileCounts_UnreadableUntrackedFileWarns pins the --dry-run
// counting path: an untracked file whose content cannot be read must not
// just vanish from the preview -- the numstat and ls-files failures right
// above it in pathspecFileCounts both warn instead of silently shrinking
// the total, and an unreadable untracked file is the same kind of gap.
func TestPathspecFileCounts_UnreadableUntrackedFileWarns(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t)
	ctx := context.Background()

	full := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(full, []byte("shh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(full, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(full, 0o644) })

	files, warnings := pathspecFileCounts(ctx, repo, dir, "secret.txt")

	for _, f := range files {
		if f.path == "secret.txt" {
			t.Errorf("unreadable file %q was still counted: %+v", f.path, f)
		}
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "secret.txt") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v; want one naming secret.txt", warnings)
	}
}
