package resolve

import ts "github.com/tree-sitter/go-tree-sitter"

func init() { register(newJSONLanguage()) }

// jsonLanguage adapts the tree-sitter JSON grammar. Node shapes were
// measured against a compiled parse tree, not assumed from grammar.js.
//
// Unlike CSS or YAML, JSON's grammar does declare real fields: a "pair"
// node's "key" and "value" children are addressable by ChildByFieldName,
// measured directly -- so, unusually among this resolver's adapters,
// jsonKeyName and objectDeclarations below read fields rather than scanning
// by position.
type jsonLanguage struct {
	lang *ts.Language
}

func newJSONLanguage() *jsonLanguage {
	return &jsonLanguage{lang: jsonGrammar()}
}

func (j *jsonLanguage) Name() string { return "json" }

func (j *jsonLanguage) Extensions() []string { return []string{".json"} }

func (j *jsonLanguage) TSLanguage() *ts.Language { return j.lang }

// IsComment is unconditionally false: JSON has no comment syntax at all --
// measured by confirming no "comment" node kind appears anywhere in this
// grammar's node-types.json, unlike every other adapter in this resolver.
func (j *jsonLanguage) IsComment(string) bool { return false }

// HeaderKinds returns nil: with no comment syntax, there is nothing @header
// could ever claim -- JSON has no shebang, license-comment convention, or
// package-clause equivalent either. headerExtent's own walk over
// HeaderKinds() finds nothing regardless, but returning nil here says so
// directly rather than leaving a reader to infer it from an empty result.
func (j *jsonLanguage) HeaderKinds() []string { return nil }

// ImportKinds returns nil for the same reason: JSON has no include/import
// directive of any kind, the same degraded-but-not-an-error answer Markdown,
// YAML, and TOML already give.
func (j *jsonLanguage) ImportKinds() []string { return nil }

// OwnsTrailingSeparator is false: HeaderKinds is already nil, so @header
// never matches anything for JSON and this is unreached in practice.
// Answered explicitly anyway rather than left to fall through a switch by
// omission.
func (j *jsonLanguage) OwnsTrailingSeparator() bool { return false }

// MembersSitFlush is false: JSON has no formatting convention -- deterministic
// or otherwise -- for blank lines between object members at all; authored
// JSON essentially never carries one either way, so there is no tool-backed
// convention to match the way there is for Go and TypeScript, the same "no
// convention to match" default YAML and Python (for their own, different
// reasons) also land on.
func (j *jsonLanguage) MembersSitFlush() bool { return false }

// AllowsRawHeadingFallback is false: JSON has no heading concept for the
// fallback to apply to.
func (j *jsonLanguage) AllowsRawHeadingFallback() bool { return false }

// Declarations addresses a top-level object's own key paths,
// container-qualified one level in for a nested object -- the same
// nearest-ancestor-only rule lang_yaml.go's mappingDeclarations already
// uses, chosen for the same reason: Declaration carries one Container
// field, not a path.
//
// A document whose root value is not an object -- a bare array or scalar,
// both legal top-level JSON -- has no key to address at all and returns
// nil, the same refusal lang_yaml.go gives a document with no top-level
// mapping. Most real JSON `rgit` runs against in practice (`package.json`,
// tsconfig, lockfiles) wants whole-path staging regardless
// (docs/ANCHORS.md) -- this adapter exists for the config-file case where a
// single nested key is the unit that actually changes, not to make every
// JSON file's breadth individually addressable.
func (j *jsonLanguage) Declarations(src []byte, root *ts.Node) []Declaration {
	value := topValue(root)
	if value == nil || value.Kind() != "object" {
		return nil
	}
	return objectDeclarations(value, "", src)
}

// topValue returns document's own single value child -- measured: "document"
// wraps exactly one named child, whatever JSON value the file's top level
// is.
func topValue(root *ts.Node) *ts.Node {
	if root.NamedChildCount() == 0 {
		return nil
	}
	return root.NamedChild(0)
}

// objectDeclarations walks object's own "pair" children. A pair whose value
// is itself an "object" recurses one level, qualified by container -- the
// pair's own bare key. A pair whose value is an "array" is not descended
// into: an array element has no name of its own to address it by, the same
// reasoning lang_yaml.go's block_sequence_item is left unaddressable for.
func objectDeclarations(object *ts.Node, container string, src []byte) []Declaration {
	var out []Declaration
	for _, child := range namedChildren(object) {
		pair := child
		if pair.Kind() != "pair" {
			continue
		}
		bare, ok := jsonKeyName(src, pair.ChildByFieldName("key"))
		if !ok {
			continue
		}
		out = append(out, Declaration{Node: &pair, Bare: bare, Container: container})
		if value := pair.ChildByFieldName("value"); value != nil && value.Kind() == "object" {
			out = append(out, objectDeclarations(value, bare, src)...)
		}
	}
	return out
}

// jsonKeyName reads a pair's own "key" field -- always a "string" node, the
// only key shape JSON's grammar permits -- as a bare name. The key's own
// text is quoted; string_content is the grammar's own unquoted inner node
// (measured: `"name"` parses as string wrapping a single string_content
// child spanning exactly `name`), so no manual quote-stripping is needed
// the way lang_yaml.go's quoted keys require. An empty string key (`""`)
// has no string_content child at all and is left unaddressable rather than
// resolved to an empty Bare, which would collide with itself the moment a
// second one appeared and give no useful anchor to type in the first
// place.
func jsonKeyName(src []byte, key *ts.Node) (string, bool) {
	if key == nil || key.Kind() != "string" {
		return "", false
	}
	if key.NamedChildCount() == 0 {
		return "", false
	}
	content := key.NamedChild(0)
	if content.Kind() != "string_content" {
		return "", false
	}
	return nodeText(src, content), true
}
