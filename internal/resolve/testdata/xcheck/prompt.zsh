#!/usr/bin/env zsh
# prompt helpers
source ~/.zshenv

# say hello
function greet {
  print hi
}

bye() {
  repeat 3 print bye
}

setopt extended_glob
