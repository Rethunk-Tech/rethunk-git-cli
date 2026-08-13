package gitx

import (
	"bytes"
	"context"
)

// IndexWorktreeBits reports the stage-0 worktree bits for path.
func (r *Repo) IndexWorktreeBits(ctx context.Context, path string) (skip, assume, found bool, err error) {
	out, err := r.checked(ctx, "ls-files", "-v", "--", path)
	if err != nil {
		return false, false, false, err
	}
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case 'S':
			return true, false, true, nil
		case 's':
			return true, true, true, nil
		case 'h':
			return false, true, true, nil
		case 'H':
			return false, false, true, nil
		}
	}
	return false, false, false, nil
}
