package resolve

// SQLBuildTag is lang_sql.go's build tag, spelled once so gatedExtensionTags
// and this package's tests cannot drift from each other. lang_sql.go's
// `//go:build rgit_sql` line must stay a literal: the toolchain parses build
// constraints textually, before any Go code compiles. gated_test.go's
// TestGatedTag_AgreesWithForExtension stands in for the reference it cannot
// make -- it runs under both configurations, which is what demonstrates this
// names the real tag rather than a string that happens to match.
const SQLBuildTag = "rgit_sql"

// gatedExtensionTags maps a file extension to the build tag that would
// compile in its grammar. Never behind a build tag itself: its purpose is
// answering for a build where the adapter it names is absent, so it must
// exist in every build.
//
// Not the same information as LanguageInfo.Gated (lang.go), which describes
// an adapter that IS registered. A build without -tags rgit_sql never
// registers "sql", so Languages() has no entry for Gated to answer through
// -- this map is the only place that can still say "sql exists, you just
// didn't build it in".
var gatedExtensionTags = map[string]string{
	".sql": SQLBuildTag,
}

// GatedTag reports the build tag that would register ext's grammar, when ext
// is a known gated extension not already registered in this build. ok is
// false both when ext has no known gate (a genuinely unsupported language)
// and when ext is already registered -- checked against ForExtension so a
// caller is never told "install a tag" for a grammar already working.
//
// This is what internal/app's exit-9 rebuild hint needs; the user-facing
// sentence stays there.
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
