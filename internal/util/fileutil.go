// Package util holds shared helper functions used across internal packages.
package util

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path through a temp file in the same
// directory and a rename, so a concurrent reader never sees a partial file.
// The temp file is removed on any failure.
func WriteFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}

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
func ReadFileIfExists(rootDir, path string) (content []byte, exists bool, err error) {
	content, err = ReadFileInRoot(rootDir, path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return content, true, nil
}

// ReadFileInRoot reads rel beneath rootDir through os.Root, so rel cannot
// escape the directory, and reports a Close failure when the read succeeded.
func ReadFileInRoot(rootDir, rel string) (data []byte, err error) {
	err = InRoot(rootDir, func(root *os.Root) (rerr error) {
		data, rerr = root.ReadFile(rel)
		return rerr
	})
	return data, err
}

// InRoot opens dir as an os.Root, runs fn, closes the root, and reports a
// Close failure when fn succeeded.
func InRoot(dir string, fn func(root *os.Root) error) (err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	return fn(root)
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
func LooksBinaryFile(rootDir, path string) (binary bool, err error) {
	err = InRoot(rootDir, func(root *os.Root) (rerr error) {
		f, rerr := root.Open(path)
		if rerr != nil {
			return rerr
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil && rerr == nil {
				rerr = closeErr
			}
		}()

		buf := make([]byte, BinarySampleLimit)
		n, readErr := io.ReadFull(f, buf)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return readErr
		}
		binary = LooksBinary(buf[:n])
		return nil
	})
	return binary, err
}
