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
