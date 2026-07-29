package resolve

// SQLBuildTag is lang_sql.go's own build tag, spelled once so
// gatedExtensionTags below and this package's own tests read the identical
// string rather than each hand-writing "rgit_sql" and risking one drifting
// from the other. lang_sql.go's `//go:build rgit_sql` line itself MUST
// stay a literal -- go's toolchain parses build constraints textually,
// before any Go code (including this constant) is compiled, so the
// directive can never reference it directly. gated_test.go's own
// TestGatedTag_AgreesWithForExtension is the drift guard that stands in
// for a direct reference: it runs once under each configuration
// -tags rgit_sql selects, and passing in both is what actually
// demonstrates this constant names the real gating tag, not merely a
// string that happens to match today.
const SQLBuildTag = "rgit_sql"

// gatedExtensionTags maps a file extension to the build tag that would
// compile in its grammar -- currently SQL alone, via SQLBuildTag above.
// Deliberately always compiled, never behind a build tag itself: its whole
// purpose is answering for a build where the adapter it names is ABSENT,
// so it must exist in every build, including the ones with nothing gated
// in at all.
//
// This is deliberately different information from LanguageInfo.Gated
// (lang.go): Gated describes an adapter that IS registered in this build --
// by that struct's own doc comment, it "is never a way to discover an
// absent language". A build without -tags rgit_sql never registers "sql" at
// all, so Languages() has no entry for Gated to answer through. Both facts
// are correct at once and are not redundant with each other -- do not
// collapse this map into Gated, or into a buildTagGated-style interface
// method, or a build without the tag loses the only place that can still
// say "sql exists, you just didn't build it in".
var gatedExtensionTags = map[string]string{
	".sql": SQLBuildTag,
}

// GatedTag reports the build tag that would register ext's grammar, when
// ext is a known gated extension not already registered in this build. ok
// is false both when ext has no known gate at all (a genuinely unsupported
// language) and when ext IS already registered -- checked here against
// ForExtension so a caller can never be told "install a tag" for a grammar
// already working, even if gatedExtensionTags and a *_sql.go build gate
// ever drifted apart.
//
// This is the fact `internal/app`'s exit-9 rebuild hint needs; the
// user-facing sentence stays there; internal/resolve owns only the extension
// -> tag mapping.
func GatedTag(ext string) (tag string, ok bool) {
	tag, known := gatedExtensionTags[ext]
	if !known {
		return "", false
	}
	if _, registered := ForExtension(ext); registered {
		return "", false
	}
	return tag, true
}
