package resolve

// defaultLanguage supplies the Language methods where nearly every adapter
// gives the same answer for the same reason. Embed it and override only what
// a grammar's own behaviour requires.
//
// OwnsTrailingSeparator and MembersSitFlush stay out of it even though most
// adapters also answer false: a new grammar that forgets one must fail to
// compile rather than silently inherit a value nobody decided for it. Their
// per-language answers also record distinct measured facts (why Go alone
// owns its trailing separator; why Python's MembersSitFlush is false for a
// different reason than YAML's), not copies of one. HeaderKinds stays out
// for a smaller reason: only jsonLanguage answers nil, and a forgotten
// override would claim a real grammar has no header rather than degrade the
// way a forgotten ImportKinds does.
type defaultLanguage struct{}

// AllowsRawHeadingFallback is false: only markdown has a heading concept for
// rawHeadingFallback (index.go) to retry an unresolved anchor against, and
// it opts in via lang_markdown.go.
func (defaultLanguage) AllowsRawHeadingFallback() bool { return false }

// ImportKinds is nil: the grammar has no import/include concept at all, not
// merely no import in one file -- the degraded-but-not-an-error case
// docs/ANCHORS.md describes. sql, toml, markdown, yaml and json inherit it.
// go, python, css and typescript/tsx override with a real import node.
// Shell overrides too despite also returning nil: its nil records that
// ImportMatcher takes precedence (lang_shell.go), a different fact.
func (defaultLanguage) ImportKinds() []string { return nil }

// FlatContainer is false: Declaration.Container names a real, resolvable
// ancestor for every adapter but HTML, whose tag name is carried only so
// containerQualified (index.go) can build "tag#id" anchor text. Resolving
// that as containment -- internal/synth's escalateToContainer widening a new
// member to its enclosing container -- would chase whatever unrelated
// element elsewhere happens to share the tag as its own id.
func (defaultLanguage) FlatContainer() bool { return false }
