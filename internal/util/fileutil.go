// Package util holds shared helper functions used across internal packages.
package util

import (
	"bytes"
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

// BinarySampleLimit matches core.bigFileThreshold scale used elsewhere in git's
// own binary-detection sampling.
const BinarySampleLimit = 8000

// LooksBinary applies git's heuristic: a NUL byte anywhere in a leading
// sample means binary.
func LooksBinary(content []byte) bool {
	n := len(content)
	if n > BinarySampleLimit {
		n = BinarySampleLimit
	}
	return bytes.IndexByte(content[:n], 0) != -1
}
