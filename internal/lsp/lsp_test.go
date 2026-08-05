package lsp

import (
	"testing"
	"time"
)

// TestDurationEnv pins docs/INSTALL.md's contract for RGIT_LSP_DIAL_TIMEOUT
// and RGIT_LSP_QUERY_TIMEOUT: a valid override wins, and anything else --
// unset, malformed, zero, or negative -- fails closed to the default rather
// than disabling the timeout it exists to enforce.
func TestDurationEnv(t *testing.T) {
	// cannot Parallel because t.Setenv below

	const name = "RGIT_LSP_TEST_TIMEOUT"
	const def = 7 * time.Second

	cases := []struct {
		name string
		env  string // "" means unset
		want time.Duration
	}{
		{"unset falls back to default", "", def},
		{"valid override wins", "500ms", 500 * time.Millisecond},
		{"malformed value falls back to default", "not-a-duration", def},
		{"zero falls back to default", "0s", def},
		{"negative falls back to default", "-1s", def},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.env != "" {
				t.Setenv(name, c.env)
			}
			if got := durationEnv(name, def); got != c.want {
				t.Errorf("durationEnv(%q, %v) = %v; want %v", c.env, def, got, c.want)
			}
		})
	}
}

// TestDialBudgetAndQueryDeadline_HonorEnvOverride pins that the two real
// accessors read their own env vars, not just durationEnv in isolation.
func TestDialBudgetAndQueryDeadline_HonorEnvOverride(t *testing.T) {
	// cannot Parallel because t.Setenv below

	t.Setenv("RGIT_LSP_DIAL_TIMEOUT", "250ms")
	t.Setenv("RGIT_LSP_QUERY_TIMEOUT", "3s")
	if got := dialBudget(); got != 250*time.Millisecond {
		t.Errorf("dialBudget() = %v; want 250ms", got)
	}
	if got := queryDeadline(); got != 3*time.Second {
		t.Errorf("queryDeadline() = %v; want 3s", got)
	}
}
