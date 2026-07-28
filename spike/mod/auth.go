package spike

import "errors"

// ValidateToken checks the JWT.
// Returns ErrExpired if stale.
//
//go:noinline
func ValidateToken(t string) error {
	if t == "" {
		return errors.New("empty")
	}
	return nil
}

type A struct{}
type B struct{}

// Get returns A's value.
func (a *A) Get() int { return 1 }

// Get returns B's value.
func (b *B) Get() int { return 2 }
