package synth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/util"
)

// PathError is a target refused before any resolution was attempted on
// it: a symlink, gitlink, binary, or unmerged path a symbol anchor cannot
// address (exit 10), a gitignored-and-untracked path (exit 7), or a symbol
// anchor naming a language with no grammar in this build (exit 9).
// docs/ANCHORS.md and
// docs/USAGE.md's exit-code table are authoritative; Code is set to
// match directly rather than requiring callers to pattern-match text.
type PathError struct {
	Code   exitcode.Code
	Path   string
	Reason string
}

func (e *PathError) Error() string {
	return fmt.Sprintf("synth: %s: %s", e.Path, e.Reason)
}

// pathKind classifies what a path IS, independent of whether the caller
// named it with a symbol anchor or a plain pathspec.
type pathKind int

const (
	pathRegular pathKind = iota
	pathSymlink
	pathGitlink
	pathBinary
	pathUnmerged
)

// classifyPath determines a path's kind by preferring the worktree entry
// (Lstat, so a symlink is detected as itself rather than followed) and
// falling back to the HEAD tree entry when the worktree has nothing --
// the case docs/ANCHORS.md documents for "staging a symbol deletion from
// a deleted file". A path present in neither is reported pathRegular:
// classifyPath's job is refusing addressable-but-wrong-kind paths, not
// diagnosing "does not exist", which resolve.Resolve already does with
// the right exit code.
func classifyPath(ctx context.Context, repo *gitx.Repo, root, path string) (pathKind, error) {
	unmerged, err := repo.IsUnmerged(ctx, path)
	if err != nil {
		return pathRegular, err
	}
	if unmerged {
		return pathUnmerged, nil
	}

	full := filepath.Join(root, path)
	info, statErr := os.Lstat(full)
	switch {
	case statErr == nil:
		kind, isDir, err := classifyWorktreeEntry(full, info)
		if err != nil || kind != pathRegular || !isDir {
			return kind, err
		}
		// A worktree directory with no ".git" inside it is not yet proof
		// there is no submodule here: an uninitialized (or since-deinited)
		// submodule is exactly this shape -- HEAD still records its gitlink,
		// but "git submodule update --init" was never run, so nothing
		// distinguishes it from an ordinary directory by Lstat alone.
		// Falling through to the same HEAD-tree-mode check the "absent from
		// the worktree entirely" branch below already does is what lets an
		// uninitialized submodule refuse a symbol anchor the same way an
		// initialized one does (exit 10), rather than falling all the way
		// through to a misleading "no grammar registered" (exit 9) once the
		// directory's own contents turn out unparseable.
		return classifyTreeEntry(ctx, repo, path)
	case !os.IsNotExist(statErr):
		return pathRegular, statErr
	}

	return classifyTreeEntry(ctx, repo, path)
}

// classifyTreeEntry is classifyPath's HEAD-tree fallback, shared by the
// "absent from the worktree entirely" path and the "worktree directory
// exists but isn't recognizably a submodule" path above -- both ultimately
// ask the identical question, "what does HEAD's own tree say this path is",
// and must answer it identically.
func classifyTreeEntry(ctx context.Context, repo *gitx.Repo, path string) (pathKind, error) {
	entry, found, err := repo.LsTreeTolerant(ctx, "HEAD", path)
	if err != nil {
		return pathRegular, err
	}
	if !found {
		return pathRegular, nil
	}
	switch entry.Mode {
	case "120000":
		return pathSymlink, nil
	case "160000":
		return pathGitlink, nil
	}
	sample, exists, err := repo.CatFileSample(ctx, "HEAD", path, util.BinarySampleLimit)
	if err != nil {
		return pathRegular, err
	}
	if exists && util.LooksBinary(sample) {
		return pathBinary, nil
	}
	return pathRegular, nil
}

func classifyWorktreeEntry(full string, info os.FileInfo) (kind pathKind, isDir bool, err error) {
	if info.Mode()&os.ModeSymlink != 0 {
		return pathSymlink, false, nil
	}
	if info.IsDir() {
		if _, err := os.Stat(filepath.Join(full, ".git")); err == nil {
			return pathGitlink, true, nil
		}
		// Not recognizably a submodule by local shape alone -- classifyPath
		// still cross-checks HEAD's own tree mode before settling on
		// pathRegular (an uninitialized submodule is exactly this shape).
		return pathRegular, true, nil
	}
	binary, berr := util.LooksBinaryFile(full)
	if berr != nil {
		return pathRegular, false, berr
	}
	if binary {
		return pathBinary, false, nil
	}
	return pathRegular, false, nil
}

// refusalFor turns a non-regular pathKind into the PathError docs/ANCHORS.md
// specifies for a symbol anchor targeting it. Called only when a target
// is an anchor, not a bare pathspec -- those kinds stage just fine via
// plain `git add` (docs/ANCHORS.md § Paths that anchors cannot address).
func refusalFor(path string, kind pathKind) error {
	switch kind {
	case pathSymlink:
		return &PathError{Code: exitcode.SpecialPathRefused, Path: path, Reason: "symlink; name the path instead of a symbol"}
	case pathGitlink:
		return &PathError{Code: exitcode.SpecialPathRefused, Path: path, Reason: "submodule; name the path instead of a symbol"}
	case pathBinary:
		return &PathError{Code: exitcode.SpecialPathRefused, Path: path, Reason: "binary; name the path instead of a symbol"}
	case pathUnmerged:
		return &PathError{Code: exitcode.SpecialPathRefused, Path: path, Reason: "unmerged; name the path instead of a symbol"}
	default:
		return nil
	}
}

// checkGitignoreRefusal implements "gitignored and untracked" exit 7,
// matching plain `git add`'s own refusal (docs/ANCHORS.md). Already-
// tracked-but-now-ignored paths (a common .gitignore edit after the fact)
// are not refused, exactly as git add itself does not refuse them.
func checkGitignoreRefusal(ctx context.Context, repo *gitx.Repo, path string) error {
	ignored, err := repo.CheckIgnore(ctx, path)
	if err != nil {
		return err
	}
	if !ignored {
		return nil
	}
	_, tracked, err := repo.LsTreeTolerant(ctx, "HEAD", path)
	if err != nil {
		return err
	}
	if tracked {
		return nil
	}
	return &PathError{Code: exitcode.PathRefused, Path: path, Reason: "gitignored and untracked"}
}
