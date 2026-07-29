package diff

import (
	"errors"
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
)

// TestBucketClassified_SortsEveryKind covers the three branches only a
// KindPathspec classification exercised before (measured at 57.1% under
// -short -coverpkg=./...): a bare revision positional, a bare symbol
// anchor positional, and a rev:path blob reference -- the one kind
// BucketClassified refuses outright, since it names two independent blobs
// with no single changed file to group rows under (the func's own doc
// comment).
func TestBucketClassified_SortsEveryKind(t *testing.T) {
	t.Parallel()

	t.Run("revision, pathspec, and anchor sort into their own buckets", func(t *testing.T) {
		t.Parallel()
		revisions, files, syms, err := BucketClassified([]cli.Classification{
			{Kind: cli.KindPathspec, Pathspec: "a.go"},
			{Kind: cli.KindRevision, Revision: "HEAD~1"},
			{Kind: cli.KindAnchor, Anchor: cli.Anchor{File: "b.go", Name: "B"}},
		})
		if err != nil {
			t.Fatalf("BucketClassified: %v", err)
		}
		if len(revisions) != 1 || revisions[0] != "HEAD~1" {
			t.Errorf("revisions = %v; want [\"HEAD~1\"]", revisions)
		}
		if len(files) != 1 || files[0] != "a.go" {
			t.Errorf("files = %v; want [\"a.go\"]", files)
		}
		if len(syms) != 1 || syms[0] != (SymRef{File: "b.go", Name: "B"}) {
			t.Errorf("syms = %v; want [{b.go B}]", syms)
		}
	})

	t.Run("a rev:path positional is refused", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := BucketClassified([]cli.Classification{
			{Kind: cli.KindRevPath, RevPath: cli.RevPath{Rev: "HEAD", Path: "a.go"}},
		})
		var uerr *UsageError
		if !errors.As(err, &uerr) {
			t.Fatalf("BucketClassified error = %v (%T); want *UsageError", err, err)
		}
		if uerr.Error() != `rev:path blob references (HEAD:a.go) are not supported as a diff scope; name the file directly` {
			t.Errorf("BucketClassified error = %q", uerr.Error())
		}
	})
}
