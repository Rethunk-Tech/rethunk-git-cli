package prereq

import (
	"bytes"
	"testing"

	qt "github.com/go-quicktest/qt"
)

func TestLookPath(t *testing.T) {
	t.Parallel()

	t.Run("found reports the resolved path", func(t *testing.T) {
		t.Parallel()
		// git is this repo's own hard requirement (CONTRIBUTING.md,
		// internal/gittest shells out to it already), so it is always on
		// PATH in a test environment -- the real dependency, not a double.
		c := LookPath("git", "git", "")
		qt.Assert(t, qt.Equals(c.Name, "git"))
		qt.Assert(t, qt.IsTrue(c.OK))
		qt.Assert(t, qt.Not(qt.Equals(c.Detail, "")))
	})

	t.Run("missing carries the caller's own note", func(t *testing.T) {
		t.Parallel()
		c := LookPath("nonexistent-tool", "rgit-prereq-test-does-not-exist", "optional -- see docs")
		qt.Assert(t, qt.IsFalse(c.OK))
		qt.Assert(t, qt.Equals(c.Detail, "optional -- see docs"))
	})

	// The regression this guards: doctor's and cmd/rgit-install's git checks
	// report an empty detail on failure today (no note at all) -- a
	// LookPath that silently substituted something else here would change
	// both commands' output, which is exactly what this extraction must not
	// do.
	t.Run("missing with no note reports an empty detail", func(t *testing.T) {
		t.Parallel()
		c := LookPath("nonexistent-tool", "rgit-prereq-test-does-not-exist", "")
		qt.Assert(t, qt.IsFalse(c.OK))
		qt.Assert(t, qt.Equals(c.Detail, ""))
	})
}

func TestPrint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		c    Check
		want string
	}{
		{
			name: "ok",
			c:    Check{Name: "git", OK: true, Detail: "/usr/bin/git"},
			want: "  [ok] git                      /usr/bin/git\n",
		},
		{
			name: "missing",
			c:    Check{Name: "tree-sitter CLI", OK: false, Detail: "optional"},
			want: "  [MISSING] tree-sitter CLI          optional\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			Print(&buf, tt.c)
			qt.Assert(t, qt.Equals(buf.String(), tt.want))
		})
	}
}
