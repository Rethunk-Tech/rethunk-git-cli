package synth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
