#!/bin/sh
# Downloads a release binary from GitHub, verifies it against the release's
# own SHA256SUMS, and installs it to PREFIX. See docs/INSTALL.md § Install
# script for what this does and does not cover. Language servers stay out of
# scope here on purpose.
set -eu

repo="Rethunk-Tech/rethunk-git-cli"
prefix="${PREFIX:-$HOME/.local/bin}"
version="${VERSION:-latest}"
dry_run=0

for arg in "$@"; do
  case "$arg" in
    --dry-run) dry_run=1 ;;
    *)
      echo "usage: install.sh [--dry-run]  (PREFIX and VERSION are env vars, not flags)" >&2
      exit 1
      ;;
  esac
done

os=$(uname -s)
case "$os" in
  Linux) os_tag=linux ;;
  Darwin) os_tag=darwin ;;
  *)
    echo "install.sh: unsupported OS '$os' -- download the release binary directly (docs/INSTALL.md § Cross builds), or build from source instead" >&2
    exit 1
    ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64) arch_tag=amd64 ;;
  aarch64 | arm64) arch_tag=arm64 ;;
  *)
    echo "install.sh: unsupported architecture '$arch' -- build from source instead (docs/INSTALL.md § Build)" >&2
    exit 1
    ;;
esac

if [ "$version" = "latest" ]; then
  tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" |
    grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4)
  if [ -z "$tag" ]; then
    echo "install.sh: could not determine the latest release tag" >&2
    exit 1
  fi
else
  tag="$version"
fi

asset="rgit-$tag-$os_tag-$arch_tag"
base_url="https://github.com/$repo/releases/download/$tag"

if [ "$dry_run" = 1 ]; then
  echo "would download: $base_url/$asset"
  echo "would verify against: $base_url/SHA256SUMS"
  if command -v cosign >/dev/null 2>&1; then
    echo "would verify with cosign/Sigstore bundle: $base_url/SHA256SUMS.sigstore.json"
  fi
  echo "would install to: $prefix/rgit"
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

curl -fsSL -o "$tmp/$asset" "$base_url/$asset"
curl -fsSL -o "$tmp/SHA256SUMS" "$base_url/SHA256SUMS"

if command -v cosign >/dev/null 2>&1; then
  curl -fsSL -o "$tmp/SHA256SUMS.sigstore.json" "$base_url/SHA256SUMS.sigstore.json"
  (
    cd "$tmp"
    cosign verify-blob \
      --bundle SHA256SUMS.sigstore.json \
      --certificate-identity-regexp 'https://github.com/Rethunk-Tech/rethunk-git-cli/.github/workflows/release.yml@.*' \
      --certificate-oidc-issuer https://token.actions.githubusercontent.com \
      SHA256SUMS
  )
fi

(
  cd "$tmp"
  case "$os_tag" in
    linux) grep " $asset\$" SHA256SUMS | sha256sum -c - ;;
    darwin) grep " $asset\$" SHA256SUMS | shasum -a 256 -c - ;;
  esac
)

mkdir -p "$prefix"
install -m 0755 "$tmp/$asset" "$prefix/rgit"

echo "installed to $prefix/rgit:"
"$prefix/rgit" --version

case ":$PATH:" in
  *":$prefix:"*) ;;
  *) echo "note: $prefix is not on PATH -- add it to use 'rgit' directly" ;;
esac

echo "optional language servers: go run ./cmd/rgit-install -with-servers (from a source checkout), or see docs/INSTALL.md § Language servers"
