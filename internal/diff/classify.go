package diff

import (
	"fmt"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/cli"
)

// BucketClassified sorts cli.ClassifyArgs's per-positional results into the
// buckets Options needs. A bare KindAnchor positional is equivalent to
// --sym and a bare KindPathspec positional is equivalent to --file
// (docs/USAGE.md § Flags); the caller merges these with the explicit flag
// values.
//
// KindRevPath (a bare "rev:path" positional) is refused unless exactly two
// appear and name the identical path at two revisions -- "rgit diff
// A:f.go B:f.go", the one shape with a single file for rgit diff to group
// symbol rows under. Comparing two arbitrary, differently-named blobs is
// valid git diff syntax but has no such file to report
// against, and a lone rev:path (no partner to pair with) is exactly as
// unaddressable as it always was.
func BucketClassified(classified []cli.Classification) (revisions, files []string, syms []SymRef, revPaths []cli.RevPath, err error) {
	for _, c := range classified {
		switch c.Kind {
		case cli.KindPathspec:
			files = append(files, c.Pathspec)
		case cli.KindRevision:
			revisions = append(revisions, c.Revision)
		case cli.KindAnchor:
			syms = append(syms, SymRef{File: c.Anchor.File, Name: c.Anchor.Name})
		case cli.KindRevPath:
			revPaths = append(revPaths, c.RevPath)
		}
	}

	switch {
	case len(revPaths) == 0:
		return revisions, files, syms, nil, nil
	case len(revPaths) == 2 && revPaths[0].Path == revPaths[1].Path:
		return revisions, files, syms, revPaths, nil
	case len(revPaths) == 1:
		return nil, nil, nil, nil, &UsageError{Msg: fmt.Sprintf(
			"rev:path blob reference (%s:%s) has no paired blob to compare against; name two, "+
				"\"A:%s B:%s\", or name the file directly", revPaths[0].Rev, revPaths[0].Path, revPaths[0].Path, revPaths[0].Path,
		)}
	default:
		names := make([]string, len(revPaths))
		for i, rp := range revPaths {
			names[i] = rp.Rev + ":" + rp.Path
		}
		return nil, nil, nil, nil, &UsageError{Msg: fmt.Sprintf(
			"rev:path blob references (%s) are not a supported diff scope; "+
				"exactly two naming the identical path, \"A:f.go B:f.go\", compare that file across revisions",
			strings.Join(names, ", "),
		)}
	}
}
