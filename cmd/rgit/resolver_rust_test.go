package main

import (
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

// TestResolve_Rust covers the shapes the grammar actually has to get right:
// items named by their own field, an impl block that has no name field at
// all, members qualified with Rust's own "::" path separator, and the outer
// attributes that are siblings of the item they annotate rather than part of
// it.
func TestResolve_Rust(t *testing.T) {
	t.Parallel()
	src := []byte(`use std::fmt;

pub const LIMIT: usize = 10;

/// Doc comment.
#[inline]
pub fn classify(input: &str) -> bool {
    !input.is_empty()
}

pub struct Config {
    pub name: String,
}

pub enum Kind {
    A,
    B(u8),
}

pub trait Render {
    fn render(&self) -> String;
}

impl Render for Config {
    fn render(&self) -> String {
        self.name.clone()
    }
}

impl Config {
    pub fn new(name: String) -> Self {
        Self { name }
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn alpha() {
        assert!(true);
    }
}
`)

	// An item is named by its own name field, and "::" joins a member to
	// its container -- what a Rust caller would type, not the "." every
	// other adapter's convention produces.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "LIMIT"), "pub const LIMIT: usize = 10;"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "Config::name"), "pub name: String,"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "Kind::B"), "B(u8),"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "Render::render"), "fn render(&self) -> String;"))

	// An impl block has no name field. It is named the way Rust reads it,
	// so it cannot collide with the struct of the same name, and its
	// members qualify by the type rather than by that longer display name.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "impl Config"),
		"impl Config {\n    pub fn new(name: String) -> Self {\n        Self { name }\n    }\n}"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "Config::new"),
		"pub fn new(name: String) -> Self {\n        Self { name }\n    }"))

	lang, ok := resolve.ForExtension(".rs")
	qt.Assert(t, qt.IsTrue(ok))
	_, err := resolve.Resolve(lang, src, "impl Render for Config")
	qt.Assert(t, qt.IsNil(err))

	// A doc comment and an outer attribute both belong to the item. The
	// attribute is a sibling in this grammar, not a wrapper the way
	// Python's decorated_definition is, so without prefixAttacher deleting
	// an item left its attribute orphaned.
	classify := mustResolveExt(t, ".rs", src, "classify")
	qt.Assert(t, qt.Equals(classify,
		"/// Doc comment.\n#[inline]\npub fn classify(input: &str) -> bool {\n    !input.is_empty()\n}"))

	// A module is descended into -- that is where a Rust crate's tests
	// live -- while a function body is not.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "tests::alpha"),
		"#[test]\n    fn alpha() {\n        assert!(true);\n    }"))

	// @imports spans the use declarations, never an item.
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "@imports"), "use std::fmt;"))
}

// TestResolve_RustHeader pins @header to the inner attribute run a file
// opens with, stopping before the first item.
func TestResolve_RustHeader(t *testing.T) {
	t.Parallel()
	src := []byte("#![allow(dead_code)]\n#![no_std]\n\nuse core::fmt;\n\npub fn f() {}\n")
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "@header"), "#![allow(dead_code)]\n#![no_std]"))
}

// TestResolve_RustInnerDocBelongsToTheFile pins that a "//!" comment at the
// top of a file documents the enclosing module, so the first item below it
// does not take it as its own doc the way it takes a "///" comment.
func TestResolve_RustInnerDocBelongsToTheFile(t *testing.T) {
	t.Parallel()
	src := []byte("//! Generated. Do not edit.\n\npub(crate) mod first;\npub(crate) mod second;\n\n//! also module doc, inside a block.\n/// Outer doc.\npub fn f() {}\n")
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "first"), "pub(crate) mod first;"))
	tight := []byte("//! Module doc.\npub(crate) mod first;\n")
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", tight, "first"), "pub(crate) mod first;"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "f"), "/// Outer doc.\npub fn f() {}"))
}

// TestResolve_RustDocSeparatedByABlankLineDetaches pins the blank-line rule for
// Rust, whose line_comment node ends after its own newline: a "///" comment
// with a blank line before the item is not that item's doc.
func TestResolve_RustDocSeparatedByABlankLineDetaches(t *testing.T) {
	t.Parallel()
	src := []byte("/// Stranded.\n\n/// Attached.\npub fn g() {}\n\n/// Also stranded.\n\npub fn h() {}\n")
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "g"), "/// Attached.\npub fn g() {}"))
	qt.Assert(t, qt.Equals(mustResolveExt(t, ".rs", src, "h"), "pub fn h() {}"))
}
