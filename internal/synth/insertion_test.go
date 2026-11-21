package synth

import (
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gittest"
)

// TestStage_FirstMemberOfAnEmptyContainerSplicesInsideIt pins the insertion
// point for the one case the nearest-sibling walk cannot answer on its own:
// HEAD already has the container but none of its members, so the only
// sibling to splice against is the container itself. Splicing at the
// container's own end puts the member after its closing brace, and the blob
// does not parse.
func TestStage_FirstMemberOfAnEmptyContainerSplicesInsideIt(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	gittest.Write(t, dir, "a.go", "package a\n\ntype Box struct {\n}\n")
	gittest.Commit(ctx, t, dir, "chore: empty container")
	gittest.Write(t, dir, "a.go", "package a\n\ntype Box struct {\n\tsizeX int\n}\n")

	if err := stageTargets(ctx, repo, dir, []Target{AnchorTarget("a.go", "Box.sizeX")}); err != nil {
		t.Fatalf("stageTargets: %v", err)
	}

	blob := gittest.Git(ctx, t, dir, "show", ":a.go")
	if !strings.Contains(blob, "type Box struct {\n\tsizeX int\n}") {
		t.Errorf("staged a.go = %q; want sizeX inside the struct's braces", blob)
	}
	if strings.Contains(blob, "}\n\n\tsizeX") {
		t.Errorf("staged a.go = %q; want the member inside the container, not appended after it", blob)
	}
}

// TestStage_CRLFWorktreeFileKeepsItsLineEndings pins byte-identity for a
// worktree file that uses CRLF: the separator a splice writes is part of the
// blob, so an LF pair spliced into a CRLF file commits mixed endings and
// leaves the file looking dirty after a commit that reported success.
func TestStage_CRLFWorktreeFileKeepsItsLineEndings(t *testing.T) {
	t.Parallel()

	t.Run("appended function", func(t *testing.T) {
		t.Parallel()
		dir, repo := gittest.RepoWithFile(t.Context(), t, "a.go", "package a\r\n\r\nfunc A() int {\r\n\treturn 1\r\n}\r\n", "chore: crlf a.go")
		work := "package a\r\n\r\nfunc A() int {\r\n\treturn 1\r\n}\r\n\r\nfunc B() int {\r\n\treturn 2\r\n}\r\n"
		gittest.Write(t, dir, "a.go", work)

		if err := stageTargets(t.Context(), repo, dir, []Target{AnchorTarget("a.go", "B")}); err != nil {
			t.Fatalf("stageTargets: %v", err)
		}

		if got := gittest.Git(t.Context(), t, dir, "show", ":a.go"); got != work {
			t.Errorf("staged a.go = %q; want the worktree bytes %q", got, work)
		}
	})

	t.Run("deleted function", func(t *testing.T) {
		t.Parallel()
		dir, repo := gittest.RepoWithFile(t.Context(), t, "a.go", "package a\r\n\r\nfunc A() int {\r\n\treturn 1\r\n}\r\n\r\nfunc B() int {\r\n\treturn 2\r\n}\r\n", "chore: crlf a.go")
		work := "package a\r\n\r\nfunc A() int {\r\n\treturn 1\r\n}\r\n"
		gittest.Write(t, dir, "a.go", work)

		if err := stageTargets(t.Context(), repo, dir, []Target{AnchorTarget("a.go", "B")}); err != nil {
			t.Fatalf("stageTargets: %v", err)
		}

		if got := gittest.Git(t.Context(), t, dir, "show", ":a.go"); got != work {
			t.Errorf("staged a.go = %q; want the worktree bytes %q", got, work)
		}
	})
}

// TestStage_MemberKeepsItsPositionBesideAKnownSibling pins the other half of
// that walk: when HEAD has some members but not the new one, and the new one
// lands ahead of the first member HEAD knows, the splice goes in at that
// sibling's line start rather than at the container's end -- otherwise the
// member's order in the file changes between the worktree and the blob.
func TestStage_MemberKeepsItsPositionBesideAKnownSibling(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	ctx := t.Context()

	gittest.Write(t, dir, "a.go", "package a\n\ntype Box struct {\n\tsizeX int\n\tsizeY int\n}\n")
	gittest.Commit(ctx, t, dir, "chore: two fields")
	gittest.Write(t, dir, "a.go", "package a\n\ntype Box struct {\n\tfirst int\n\tsizeX int\n\tsizeY int\n}\n")

	if err := stageTargets(ctx, repo, dir, []Target{AnchorTarget("a.go", "Box.first")}); err != nil {
		t.Fatalf("stageTargets: %v", err)
	}

	blob := gittest.Git(ctx, t, dir, "show", ":a.go")
	if !strings.Contains(blob, "type Box struct {\n\tfirst int\n\tsizeX int\n\tsizeY int\n}") {
		t.Errorf("staged a.go = %q; want first ahead of sizeX, its worktree position", blob)
	}
}
