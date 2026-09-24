// Package util holds shared helper functions used across internal packages.
package util

import (
	"bytes"
	"errors"
	"io"
	"os"
)

// GitFileMode is the tree mode git records for a regular worktree file:
// executable-by-anyone is 100755, everything else 100644.
func GitFileMode(info os.FileInfo) string {
	if info.Mode()&0o111 != 0 {
		return "100755"
	}
	return "100644"
}

// ReadFileIfExists reads path, reporting a missing file as exists=false
// rather than as an error. Both the diff scope's worktree side and blob
// synthesis need exactly that distinction -- a path absent from the worktree
// is an ordinary outcome (new in HEAD, or staged-deleted), not a failure --
// and it matches gitx.CatFile's convention for the same question asked of a
// tree, so the two sides of a comparison read alike.
func ReadFileIfExists(path string) (content []byte, exists bool, err error) {
	content, err = os.ReadFile(path) //nolint:gosec // callers supply paths resolved within the selected worktree
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return content, true, nil
}

// BinarySampleLimit matches core.bigFileThreshold scale used elsewhere in git's
// own binary-detection sampling.
const BinarySampleLimit = 8000

// LooksBinary applies git's heuristic: a NUL byte anywhere in a leading
// sample means binary.
func LooksBinary(content []byte) bool {
	n := min(len(content), BinarySampleLimit)
	return bytes.IndexByte(content[:n], 0) != -1
}

// LooksBinaryFile applies LooksBinary to path without reading the whole
// file: only the leading BinarySampleLimit bytes are read, since that is
// all LooksBinary ever inspects. Classifying a large worktree file (e.g. a
// symbol anchor's target) has no other reason to touch bytes past that
// sample.
func LooksBinaryFile(path string) (bool, error) {
	f, err := os.Open(path) //nolint:gosec // callers supply paths resolved within the selected worktree
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, BinarySampleLimit)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	return LooksBinary(buf[:n]), nil
}
