package diff

import (
	"fmt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
)

// BucketClassified sorts cli.ClassifyArgs's per-positional results into the
// three buckets Options needs. A bare KindAnchor positional is equivalent
// to --sym and a bare KindPathspec positional is equivalent to --file
// (docs/USAGE.md § Flags); the caller merges these with the explicit flag
// values.
//
// KindRevPath (a bare "rev:path" positional) is refused rather than
// silently mishandled: comparing two arbitrary blobs by revision is valid
// git diff syntax (specs/design.md), but it names two independent blobs
// with no single "changed file" to enumerate, and nothing in this phase's
// required scope exercises it.
func BucketClassified(classified []cli.Classification) (revisions, files []string, syms []SymRef, err error) {
	for _, c := range classified {
		switch c.Kind {
		case cli.KindPathspec:
			files = append(files, c.Pathspec)
		case cli.KindRevision:
			revisions = append(revisions, c.Revision)
		case cli.KindAnchor:
			syms = append(syms, SymRef{File: c.Anchor.File, Name: c.Anchor.Name})
		case cli.KindRevPath:
			return nil, nil, nil, &UsageError{Msg: fmt.Sprintf(
				"rev:path blob references (%s:%s) are not supported as a diff scope; name the file directly",
				c.RevPath.Rev, c.RevPath.Path)}
		}
	}
	return revisions, files, syms, nil
}
