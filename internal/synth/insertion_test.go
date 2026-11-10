package synth

import (
	"strings"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gittest"
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
