package app

import (
	"path/filepath"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/cli"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/resolve"
)

func TestHasConventionalShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		messages []string
		want     bool
	}{
		{name: "empty is -F/--amend/--fixup, not a warning", messages: nil, want: true},
		{name: "type(scope): subject", messages: []string{"feat(cli): add flag"}, want: true},
		{name: "breaking bang", messages: []string{"feat(cli)!: drop flag"}, want: true},
		{name: "type without scope", messages: []string{"chore: tidy"}, want: true},
		{name: "plain prose", messages: []string{"just some words"}, want: false},
		{name: "missing subject after colon", messages: []string{"feat(cli):"}, want: false},
		{name: "only the first -m is judged", messages: []string{"just some words", "feat(cli): later"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hasConventionalShape(tt.messages); got != tt.want {
				t.Errorf("hasConventionalShape(%q) = %v; want %v", tt.messages, got, tt.want)
			}
		})
	}
}

func TestLineRange(t *testing.T) {
	t.Parallel()

	src := []byte("one\ntwo\nthree")
	tests := []struct {
		name      string
		ext       resolve.Extent
		wantStart int
		wantEnd   int
	}{
		{name: "first line", ext: resolve.Extent{Start: 0, End: 4}, wantStart: 1, wantEnd: 1},
		// End is exclusive and a node never includes a trailing newline; using
		// End itself would pull line 3 into a range that ends exactly at "two\n".
		{name: "middle line ending on its newline", ext: resolve.Extent{Start: 4, End: 8}, wantStart: 2, wantEnd: 2},
		{name: "last line without a trailing newline", ext: resolve.Extent{Start: 8, End: 13}, wantStart: 3, wantEnd: 3},
		{name: "empty extent stays on its start line", ext: resolve.Extent{Start: 4, End: 4}, wantStart: 2, wantEnd: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			start, end := lineRange(src, tt.ext)
			if start != tt.wantStart || end != tt.wantEnd {
				t.Errorf("lineRange(%+v) = %d,%d; want %d,%d", tt.ext, start, end, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestCheckPathEscape(t *testing.T) {
	t.Parallel()

	root := filepath.FromSlash("/repo")
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "in-repo path", path: "pkg/a.go"},
		{name: "leading-colon magic is not a path", path: ":(exclude)docs/*"},
		{name: "parent directory", path: "../outside.go", wantErr: true},
		{name: "nested climb", path: "pkg/../../outside.go", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := checkPathEscape(root, tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("checkPathEscape(%q) = nil; want an error", tt.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("checkPathEscape(%q) = %v", tt.path, err)
			}
		})
	}
}

func TestSplitAnchor(t *testing.T) {
	t.Parallel()
	dir, repo := gittest.New(t.Context(), t)
	gittest.Write(t, dir, "s.css", "a {}\n")
	gittest.Commit(t.Context(), t, dir, "chore: fixtures")

	tests := []struct {
		name    string
		in      string
		checker bool
		file    string
		sym     string
		ok      bool
	}{
		{
			name:    "name may contain a colon when the left side exists",
			in:      "s.css:a:hover",
			checker: true,
			file:    "s.css",
			sym:     "a:hover",
			ok:      true,
		},
		{
			name:    "untracked file still splits at the last colon",
			in:      "new.go:Main",
			checker: true,
			file:    "new.go",
			sym:     "Main",
			ok:      true,
		},
		{
			name: "nil checker skips the existence walk and uses the last colon",
			in:   "s.css:a:hover",
			file: "s.css:a",
			sym:  "hover",
			ok:   true,
		},
		{name: "no colon", in: "s.css", checker: true},
		{name: "trailing colon", in: "s.css:", checker: true},
		{name: "leading colon is not an anchor", in: ":Main", checker: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var paths *cli.GitPathChecker
			if tt.checker {
				paths = &cli.GitPathChecker{Root: dir, Repo: repo}
			}
			file, name, ok, err := splitAnchor(t.Context(), tt.in, paths)
			if err != nil {
				t.Fatalf("splitAnchor(%q) = %v", tt.in, err)
			}
			if ok != tt.ok || file != tt.file || name != tt.sym {
				t.Errorf("splitAnchor(%q) = %q, %q, %v; want %q, %q, %v",
					tt.in, file, name, ok, tt.file, tt.sym, tt.ok)
			}
		})
	}
}
