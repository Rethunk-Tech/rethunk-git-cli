package synth

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gitx"
)

// PathError is a target refused before any resolution was attempted on
// it: a symlink, gitlink, or binary path a symbol anchor cannot address
// (exit 10), a gitignored-and-untracked path (exit 7), or a symbol anchor
// naming a language with no v1 grammar (exit 9). docs/ANCHORS.md and
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
	full := filepath.Join(root, path)
	info, statErr := os.Lstat(full)
	switch {
	case statErr == nil:
		return classifyWorktreeEntry(full, info)
	case !os.IsNotExist(statErr):
		return pathRegular, statErr
	}

	entry, found, err := repo.LsTree(ctx, "HEAD", path)
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
	content, exists, err := repo.CatFile(ctx, "HEAD", path)
	if err != nil {
		return pathRegular, err
	}
	if exists && looksBinary(content) {
		return pathBinary, nil
	}
	return pathRegular, nil
}

func classifyWorktreeEntry(full string, info os.FileInfo) (pathKind, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		return pathSymlink, nil
	}
	if info.IsDir() {
		if _, err := os.Stat(filepath.Join(full, ".git")); err == nil {
			return pathGitlink, nil
		}
		// A directory that isn't a submodule is never a symbol-anchor
		// target on its own; report it regular and let the caller's own
		// pathspec/anchor split handle it.
		return pathRegular, nil
	}
	content, err := os.ReadFile(full)
	if err != nil {
		return pathRegular, err
	}
	if looksBinary(content) {
		return pathBinary, nil
	}
	return pathRegular, nil
}

// looksBinary applies git's own heuristic: a NUL byte anywhere in a
// leading sample means binary. 8000 bytes matches core.bigFileThreshold
// scale used elsewhere in git's own binary-detection sampling.
func looksBinary(content []byte) bool {
	n := len(content)
	const sample = 8000
	if n > sample {
		n = sample
	}
	return bytes.IndexByte(content[:n], 0) != -1
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
	_, tracked, err := repo.LsTree(ctx, "HEAD", path)
	if err != nil {
		return err
	}
	if tracked {
		return nil
	}
	return &PathError{Code: exitcode.PathRefused, Path: path, Reason: "gitignored and untracked"}
}
