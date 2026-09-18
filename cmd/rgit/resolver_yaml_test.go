package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// TestResolve_YAML covers the grammar's own shapes in one pass: a realistic
// workflow fixture for nesting/qualification/comment-grafting/pseudo-anchors,
// then edge shapes a realistic fixture never exercises on its own (anchors/
// aliases, quoted keys, flow-style leaves, ordinal disambiguation across
// different grandparents, the no-addressable-mapping refusals, and the key
// spellings yamlKeyName does and does not turn into a Bare name).
func TestResolve_YAML(t *testing.T) {
	t.Parallel()
	// A realistic GitHub Actions workflow: several jobs, nested steps, a
	// block scalar `run: |`, a flow sequence, and a comment sitting between
	// the end of a nested job and the next, more shallowly indented one.
	src := []byte(`# leading header comment

name: CI

on:
  push:
    branches: [main]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Build
        run: |
          go build ./...
          go vet ./...

  # a comment between build and test
  test:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
`)

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "@header"), "# leading header comment"))

	// "jobs.build" is the nearest-ancestor qualification the target spelling
	// asks for: the whole job's subtree, block scalar included verbatim.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "jobs.build"),
		"build:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n"+
			"      - name: Build\n        run: |\n          go build ./...\n          go vet ./..."))

	// A third level of nesting is qualified by its own immediate parent
	// only, "build.runs-on", never the accumulated "jobs.build.runs-on" --
	// Declaration carries one Container field, not a path, the same
	// nearest-ancestor rule lang_markdown.go's sectionDeclarations already
	// uses for nested headings.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "build.runs-on"), "runs-on: ubuntu-latest"))

	// A sequence value is addressable as a whole -- "build.steps" claims the
	// entire list -- but never per item: there is no name to address one by,
	// the same reasoning Go's shared "A, B int" field line is left
	// unaddressable for.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "build.steps"),
		"steps:\n      - uses: actions/checkout@v4\n      - name: Build\n        run: |\n"+
			"          go build ./...\n          go vet ./..."))

	// The comment between "build"'s last nested line and "test:" is not
	// "test:"'s leading trivia: tree-sitter-yaml's own external scanner
	// grafts a comment preceding a multi-level dedent onto whichever block
	// was still open when it consumed the comment token, regardless of the
	// comment's own written column (lang_yaml.go's trimTrailingComment).
	// "jobs.build" excludes it (trimmed off
	// its trailing edge, so an edit to "build" alone never silently carries
	// a comment written for its neighbour), and "jobs.test" never had it as
	// a real sibling to begin with, so neither key's own extent claims it.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "jobs.test"),
		"test:\n    needs: build\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"))

	// @toplevel still reaches the orphaned comment: its widening bounds to
	// the enclosing document node (pseudo.go's shared "first through last
	// declaration" formula), which always spans to the document's own true
	// end regardless of where any interior comment got grafted.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", src, "@toplevel"),
		"name: CI\n\non:\n  push:\n    branches: [main]\n\njobs:\n  build:\n    runs-on: ubuntu-latest\n"+
			"    steps:\n      - uses: actions/checkout@v4\n      - name: Build\n        run: |\n"+
			"          go build ./...\n          go vet ./...\n\n"+
			"  # a comment between build and test\n  test:\n    needs: build\n"+
			"    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"))

	lang, ok := resolve.ForExtension(".yml")
	qt.Assert(t, qt.IsTrue(ok))

	// Naming a bare sequence item (no such anchor exists to type in the
	// first place) refuses honestly rather than silently matching something
	// else.
	_, err := resolve.Resolve(lang, src, "build.steps.0")
	var unresolvable *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// .yaml is claimed too.
	_, ok = resolve.ForExtension(".yaml")
	qt.Assert(t, qt.IsTrue(ok))

	// A second fixture isolates shapes the realistic workflow above never
	// exercises: a quoted key, an anchor/alias pair riding along verbatim
	// inside whatever key contains them, a flow-style mapping value left
	// undescended, and two containers of the same name at different
	// grandparents colliding the same way lang_markdown.go's two "Options"
	// headings under different parents already do.
	edgeShapes := []byte(`defaults: &defaults
  adapter: postgres

development:
  <<: *defaults
  "quoted key": ok
  flow: { a: 1, b: 2 }

a:
  common:
    port: 1
b:
  common:
    port: 2
`)

	// The anchor and its later alias are never split around: naming
	// "defaults" claims the "&defaults" marker as part of its own value,
	// and "<<: *defaults" is an ordinary key ("<<") whose value is the
	// alias, preserved byte-for-byte.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", edgeShapes, "defaults"),
		"defaults: &defaults\n  adapter: postgres"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", edgeShapes, "development.<<"), "<<: *defaults"))

	// A quoted key's surrounding quote byte is stripped from Bare -- a
	// best-effort unwrap, not full YAML unescaping.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", edgeShapes, "development.quoted key"), `"quoted key": ok`))

	// A flow-style mapping value is a leaf: "development.flow" claims the
	// whole `{ a: 1, b: 2 }`, but there is no "development.flow.a" to
	// address -- flow style is never descended into, at any depth.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", edgeShapes, "development.flow"), "flow: { a: 1, b: 2 }"))
	_, err = resolve.Resolve(lang, edgeShapes, "development.flow.a")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// "a.common" and "b.common" are different containers, so "common.port"
	// under each collides in qualified name even though the two are nowhere
	// near each other in the tree -- ordinal disambiguation, not a merge.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", edgeShapes, "common.port#1"), "port: 1"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yaml", edgeShapes, "common.port#2"), "port: 2"))

	_, err = resolve.Resolve(lang, edgeShapes, "common.port")
	var ambigErr *resolve.ResolveError
	qt.Assert(t, qt.ErrorAs(err, &ambigErr))
	qt.Assert(t, qt.Equals(ambigErr.Code, exitcode.AnchorAmbiguous))
	qt.Assert(t, qt.DeepEquals(ambigErr.Candidates, []string{"common.port#1", "common.port#2"}))

	// A "---"-separated multi-document stream has no addressable key at all,
	// rather than guessing which document a bare key path means.
	multiDoc := []byte("name: A\n---\nname: B\n")
	_, err = resolve.Resolve(lang, multiDoc, "name")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))

	// topBlockMapping refuses two more shapes with nothing addressable at
	// all: a document whose only content is a bare scalar, or a sequence
	// with no mapping anywhere in it -- neither has a key to qualify a
	// Declaration with.
	for _, noMapping := range []string{
		"just a scalar\n",
		"- one\n- two\n",
		"# only a comment, no document at all\n",
	} {
		_, err = resolve.Resolve(lang, []byte(noMapping), "one")
		qt.Assert(t, qt.ErrorAs(err, &unresolvable), qt.Commentf("src %q", noMapping))
		qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))
	}

	// The comment-only file still has no document at all (soleDocument's
	// doc==nil case), but @header does not depend on there being one.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", []byte("# only a comment, no document at all\n"), "@header"),
		"# only a comment, no document at all"))

	// The key spellings yamlKeyName does and does not turn into a Bare
	// name: both quote styles, and an explicit "?" key whose own value is
	// a nested block rather than a scalar -- its key field is a
	// "block_node", not the ordinary "flow_node" every plain or quoted key
	// parses as, so it is left unaddressable rather than resolved to an
	// invented spelling. "plain" is unaffected by its refused sibling.
	keyShapes := []byte("'single quoted': ok\n" +
		"?\n  a: 1\n  b: 2\n: value\n" +
		"plain: fine\n")

	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", keyShapes, "single quoted"), "'single quoted': ok"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".yml", keyShapes, "plain"), "plain: fine"))

	_, err = resolve.Resolve(lang, keyShapes, "a")
	qt.Assert(t, qt.ErrorAs(err, &unresolvable))
	qt.Assert(t, qt.Equals(unresolvable.Code, exitcode.AnchorUnresolvable))
}
