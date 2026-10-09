package resolve

import (
	"slices"
	"testing"
)

const scssSample = "// design tokens\n@use 'sass:math';\n@import 'base';\n\n$gap: 4px;\n\n// flexbox helper\n@mixin flex($d: row) {\n  display: flex;\n}\n\n@function double($n) { @return $n * 2; }\n\n%placeholder { color: red; }\n\n.btn, .card {\n  &:hover { color: blue; }\n  .icon { width: 1px; }\n}\n\n@media (min-width: 1px) { .a { b: c } }\n"

func TestSCSS_Declarations(t *testing.T) {
	t.Parallel()
	lang, ok := ForExtension(".scss")
	if !ok {
		t.Fatal(".scss is not registered")
	}
	got, err := DeclOrder(lang, []byte(scssSample))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"$gap", "flex", "double", "%placeholder",
		// A grouped rule indexes one anchor per selector; its nested rules
		// qualify by the first selector only (cssLanguage.ruleSetDeclarations).
		".btn", ".card", ".btn &:hover", ".btn .icon",
		"@media (min-width: 1px)",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("DeclOrder = %v;\n want %v", got, want)
	}
}

func TestSCSS_ExtentsAndDocComments(t *testing.T) {
	t.Parallel()
	lang, _ := ForExtension(".scss")
	src := []byte(scssSample)

	res, err := Resolve(lang, src, "flex")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(src[res.Extent.Start:res.Extent.End]); got != "// flexbox helper\n@mixin flex($d: row) {\n  display: flex;\n}" {
		t.Errorf("mixin extent = %q; want the // comment attached", got)
	}
	if got := string(src[res.DeclOnly.Start:res.DeclOnly.End]); got != "@mixin flex($d: row) {\n  display: flex;\n}" {
		t.Errorf("mixin declaration-only extent = %q", got)
	}

	res, err = Resolve(lang, src, "$gap")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(src[res.Extent.Start:res.Extent.End]); got != "$gap: 4px;" {
		t.Errorf("$gap extent = %q", got)
	}
}

func TestSCSS_Pseudo(t *testing.T) {
	t.Parallel()
	lang, _ := ForExtension(".scss")
	src := []byte(scssSample)

	res, err := Resolve(lang, src, "@imports")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(src[res.Extent.Start:res.Extent.End]); got != "@use 'sass:math';\n@import 'base';" {
		t.Errorf("@imports = %q; want @use and @import spanned together", got)
	}

	res, err = Resolve(lang, src, "@header")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(src[res.Extent.Start:res.Extent.End]); got != "// design tokens" {
		t.Errorf("@header = %q", got)
	}
}
