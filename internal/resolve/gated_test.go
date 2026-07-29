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
//     must name "rgit_sql" as the tag that would register it (gated.go's
//     "genuinely unsupported" case does not apply -- .sql has a known gate).
//   - tag present: ForExtension(".sql") is registered (lang_sql.go's own
//     init), and GatedTag(".sql") must report ok=false -- gated.go's own
//     doc comment: a caller must never be told "install a tag" for a
//     grammar already working.
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
		t.Fatal("GatedTag(\".sql\") ok = false while ForExtension(\".sql\") is unregistered; want (\"rgit_sql\", true)")
	}
	if tag != "rgit_sql" {
		t.Errorf("GatedTag(\".sql\") tag = %q; want %q", tag, "rgit_sql")
	}
}
