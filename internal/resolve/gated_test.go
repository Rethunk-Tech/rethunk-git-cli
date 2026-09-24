package resolve

import "testing"

// TestGatedTag_AgreesWithForExtension pins gated.go's own contract: GatedTag
// and ForExtension must never both say "usable" -- or both say "absent, no
// hint" -- for the same extension, in whichever build this test happens to
// run under. It reads ForExtension first and asserts GatedTag's complementary
// answer, rather than hand-writing "tag present" and "tag absent" as two
// separate cases, so the same assertion is correct -- and this file needs no
// build tag of its own -- whether or not -tags rgit_sql registered the SQL
// adapter:
//
//   - tag absent: ForExtension(".sql") is unregistered, and GatedTag(".sql")
//     must name SQLBuildTag as the tag that would register it (gated.go's
//     "genuinely unsupported" case does not apply -- .sql has a known gate).
//   - tag present: ForExtension(".sql") is registered (lang_sql.go's tagged
//     registration), and GatedTag(".sql") must report ok=false -- gated.go's own
//     doc comment: a caller must never be told "install a tag" for a
//     grammar already working.
//
// Asserting against SQLBuildTag rather than a second "rgit_sql" literal is
// also this suite's drift guard for the constant itself: lang_sql.go's own
// //go:build line must stay a literal string (go's toolchain requirement,
// SQLBuildTag's own doc comment), so it cannot reference the constant
// directly. Running this test once under each configuration -tags rgit_sql
// selects is what actually demonstrates SQLBuildTag names the real gating
// tag, not merely a string that happens to match today.
func TestGatedTag_AgreesWithForExtension(t *testing.T) {
	t.Parallel()

	_, registered := ForExtension(".sql")
	tag, ok := GatedTag(".sql")

	if registered {
		if ok {
			t.Errorf("GatedTag(\".sql\") = (%q, true) while ForExtension(\".sql\") is already registered; want ok=false", tag)
		}
		return
	}

	if !ok {
		t.Fatalf("GatedTag(\".sql\") ok = false while ForExtension(\".sql\") is unregistered; want (%q, true)", SQLBuildTag)
	}
	if tag != SQLBuildTag {
		t.Errorf("GatedTag(\".sql\") tag = %q; want %q", tag, SQLBuildTag)
	}
}
