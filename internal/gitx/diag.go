package gitx

import (
	"context"
	"fmt"
	"strings"
)

// HasStash reports whether the repository has a stash ref.
func (r *Repo) HasStash(ctx context.Context) (bool, error) {
	_, ok, err := r.RevParseVerify(ctx, "refs/stash")
	return ok, err
}

// SparseCheckout reports whether git's sparse-checkout configuration is true.
func (r *Repo) SparseCheckout(ctx context.Context) (bool, error) {
	args := []string{"config", "--bool", "--get", "core.sparseCheckout"}
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
		switch strings.TrimSpace(string(res.Stdout)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return false, fmt.Errorf("git config core.sparseCheckout returned invalid value %q", strings.TrimSpace(string(res.Stdout)))
		}
	case 1:
		return false, nil
	default:
		return false, gitError(args, res)
	}
}

// ChangedPaths lists the paths whose index differs from HEAD (staged) and
// whose worktree differs from the index (unstaged), each via `git diff
// --name-only -z` so no path-quoting rules apply. Untracked files are in
// neither list.
func (r *Repo) ChangedPaths(ctx context.Context) (staged, unstaged []string, err error) {
	names := func(extra string) ([]string, error) {
		args := []string{"diff", "--name-only", "-z", "--no-renames"}
		if extra != "" {
			args = append(args, extra)
		}
		out, cerr := r.checked(ctx, args...)
		if cerr != nil {
			return nil, cerr
		}
		trimmed := strings.Trim(string(out), "\x00")
		if trimmed == "" {
			return nil, nil
		}
		return strings.Split(trimmed, "\x00"), nil
	}
	if staged, err = names("--cached"); err != nil {
		return nil, nil, err
	}
	if unstaged, err = names(""); err != nil {
		return nil, nil, err
	}
	return staged, unstaged, nil
}
