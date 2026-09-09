//! Fixture carrying the shapes the Rust adapter has to get right.
use std::collections::HashMap;

pub const LIMIT: usize = 10;

/// A documented item: rust-analyzer ranges from this line, not from `pub fn`.
#[inline]
pub fn classify(input: &str) -> bool {
    !input.is_empty()
}

pub struct Config {
    pub name: String,
    pub size: usize,
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
        Self { name, size: 0 }
    }
}

pub fn index() -> HashMap<String, usize> {
    HashMap::new()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn classifies_non_empty() {
        assert!(classify("x"));
    }
}
