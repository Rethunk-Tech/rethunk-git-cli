package resolve

// defaultLanguage supplies two Language methods where nearly every adapter
// in this package gives the same answer for the same reason -- no
// measured, grammar-specific fact backs a different one. Embed it and
// override only what a specific grammar's own behaviour actually requires.
//
// OwnsTrailingSeparator and MembersSitFlush are deliberately NOT part of
// this default, even though most adapters also answer false for both:
// Language's own doc comment on each requires an explicit, considered
// answer from every adapter, specifically so a new grammar that forgets one
// fails to compile rather than silently inheriting a value that happens to
// match every existing adapter but was never actually decided for the new
// one. Folding those into a shared default would trade that compile-time
// guarantee for the exact silent-drift risk a default exists to avoid in
// the first place -- and several of their per-language comments (why Go
// alone owns its trailing separator; why Python's MembersSitFlush answers
// false for a different reason than YAML's) record a measured fact worth
// keeping distinct, not a copy of the same one.
//
// HeaderKinds is left alone too, for a smaller reason: only jsonLanguage
// answers nil today, so there is close to nothing to deduplicate, and a
// forgotten override there would silently claim a real grammar has no
// header at all rather than degrade the way a forgotten ImportKinds does.
type defaultLanguage struct{}

// AllowsRawHeadingFallback is false: only markdown has a heading concept
// for rawHeadingFallback (index.go) to retry an unresolved anchor against.
// Every other adapter returns this same value for this same reason — only
// markdown opts in via lang_markdown.go.
func (defaultLanguage) AllowsRawHeadingFallback() bool { return false }

// ImportKinds is nil: no node kind in the grammar plays the role of an
// import/include statement at all -- not merely absent from one file, but
// not a concept the grammar has. sql, toml, markdown, yaml, and json all
// return nil here identically, each pointing at
// the others as the same degraded-but-not-an-error precedent
// (docs/ANCHORS.md). go, python, css, and typescript/tsx all have a real
// import-shaped node and override this explicitly with it; shell overrides
// explicitly too despite also returning nil -- its nil records a different
// fact (ImportMatcher takes precedence, lang_shell.go), not "no import
// concept," so it is not this default's to speak for.
func (defaultLanguage) ImportKinds() []string { return nil }
