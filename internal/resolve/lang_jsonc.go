package resolve

// jsoncLanguage is JSON with comments. tree-sitter-json already parses `//`
// and `/* */` as extras (a "comment" node anywhere whitespace may sit), so
// .jsonc reuses jsonLanguage's declarations unchanged and differs only in
// claiming the extension and treating those nodes as comments.
//
// Trailing commas, which JSONC permits, are not part of the JSON grammar:
// they parse as an ERROR node inside the object. Declarations before the
// error still resolve (TestJSONC_TrailingComma pins this), so such files
// degrade to the keys the parser could place rather than to nothing.
type jsoncLanguage struct {
	jsonLanguage
}

func newJSONCLanguage() *jsoncLanguage {
	return &jsoncLanguage{jsonLanguage: *newJSONLanguage()}
}

func (j *jsoncLanguage) Name() string { return "jsonc" }

func (j *jsoncLanguage) Extensions() []string { return []string{".jsonc"} }

func (j *jsoncLanguage) IsComment(kind string) bool { return kind == "comment" }

// HeaderKinds is inherited as nil: every key sits inside the one top-level
// object, so toplevelExtent widens to that object's own extent (comments
// included) and @header would have no room before it to claim.
