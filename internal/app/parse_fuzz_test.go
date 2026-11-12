package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func FuzzCheckPathEscape(f *testing.F) {
	f.Add("pkg/a.go")
	f.Add(":(exclude)docs/*")
	f.Add("../outside.go")
	f.Add("pkg/../../outside.go")
	root := filepath.FromSlash("/repo")
	f.Fuzz(func(t *testing.T, path string) {
		err := checkPathEscape(root, path)
		if err != nil {
			return
		}
		if strings.HasPrefix(path, ":") {
			return
		}
		rel, relErr := filepath.Rel(root, filepath.Join(root, path))
		if relErr != nil {
			t.Fatalf("checkPathEscape(%q) = nil, but Rel: %v", path, relErr)
		}
		if rel == "." {
			return
		}
		if !filepath.IsLocal(rel) {
			t.Fatalf("checkPathEscape(%q) = nil, but Rel %q leaves the repo", path, rel)
		}
	})
}
